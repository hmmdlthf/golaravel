package queue

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"
)

// RedisConfig holds connection settings for the Redis queue backend.
type RedisConfig struct {
	Addr     string        // Host and port, e.g. "127.0.0.1:6379"
	Password string        // Optional AUTH password
	DB       int           // Redis database index (0-15)
	Prefix   string        // Key prefix, defaults to "golaravel:queue:"
	Timeout  time.Duration // Dial & read/write timeout
}

// RedisDriver implements Driver backed by Redis using pure Go RESP protocol.
type RedisDriver struct {
	cfg      RedisConfig
	pool     *redisPool
	prefix   string
	stopChan chan struct{}
}

// NewRedisDriver creates a new Redis queue driver.
func NewRedisDriver(cfg RedisConfig) (*RedisDriver, error) {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:6379"
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "golaravel:queue:"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}

	pool := newRedisPool(cfg, 10)

	// Test connection
	conn, err := pool.get()
	if err != nil {
		return nil, fmt.Errorf("queue: failed to connect to redis at %s: %w", cfg.Addr, err)
	}
	pool.put(conn)

	d := &RedisDriver{
		cfg:      cfg,
		pool:     pool,
		prefix:   cfg.Prefix,
		stopChan: make(chan struct{}),
	}

	go d.processDelayed()
	return d, nil
}

// Push enqueues a job payload into the Redis list via LPUSH.
func (d *RedisDriver) Push(queue string, payload *JobPayload) error {
	payload.Queue = queue
	payload.AvailableAt = time.Now()

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	key := d.prefix + queue
	conn, err := d.pool.get()
	if err != nil {
		return err
	}
	defer d.pool.put(conn)

	_, err = conn.cmd("LPUSH", key, string(data))
	return err
}

// Later enqueues a delayed job into a Redis Sorted Set via ZADD.
func (d *RedisDriver) Later(queue string, delay time.Duration, payload *JobPayload) error {
	payload.Queue = queue
	available := time.Now().Add(delay)
	payload.AvailableAt = available

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	key := d.prefix + queue + ":delayed"
	score := strconv.FormatInt(available.UnixNano(), 10)

	conn, err := d.pool.get()
	if err != nil {
		return err
	}
	defer d.pool.put(conn)

	_, err = conn.cmd("ZADD", key, score, string(data))
	return err
}

// Pop pops the next ready job from Redis via RPOP or BRPOP.
func (d *RedisDriver) Pop(queue string, timeout time.Duration) (*JobPayload, error) {
	key := d.prefix + queue
	conn, err := d.pool.get()
	if err != nil {
		return nil, err
	}
	defer d.pool.put(conn)

	var raw string
	if timeout > 0 {
		secs := int(timeout.Seconds())
		if secs <= 0 {
			secs = 1
		}
		// BRPOP returns [key, element]
		res, err := conn.cmd("BRPOP", key, strconv.Itoa(secs))
		if err != nil {
			return nil, err
		}
		arr, ok := res.([]any)
		if !ok || len(arr) < 2 || arr[1] == nil {
			return nil, ErrQueueEmpty
		}
		raw = fmt.Sprint(arr[1])
	} else {
		res, err := conn.cmd("RPOP", key)
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, ErrQueueEmpty
		}
		raw = fmt.Sprint(res)
	}

	if raw == "" {
		return nil, ErrQueueEmpty
	}

	var payload JobPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("queue: failed to deserialize job payload: %w", err)
	}

	return &payload, nil
}

// Size returns the count of ready jobs in queue via LLEN.
func (d *RedisDriver) Size(queue string) (int64, error) {
	key := d.prefix + queue
	conn, err := d.pool.get()
	if err != nil {
		return 0, err
	}
	defer d.pool.put(conn)

	res, err := conn.cmd("LLEN", key)
	if err != nil {
		return 0, err
	}
	if n, ok := res.(int64); ok {
		return n, nil
	}
	return 0, nil
}

// Clear purges the queue and delayed set via DEL.
func (d *RedisDriver) Clear(queue string) error {
	conn, err := d.pool.get()
	if err != nil {
		return err
	}
	defer d.pool.put(conn)

	_, _ = conn.cmd("DEL", d.prefix+queue)
	_, _ = conn.cmd("DEL", d.prefix+queue+":delayed")
	return nil
}

// Close gracefully closes the Redis driver and worker connection pool.
func (d *RedisDriver) Close() {
	select {
	case <-d.stopChan:
	default:
		close(d.stopChan)
	}
	d.pool.close()
}

func (d *RedisDriver) processDelayed() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopChan:
			return
		case now := <-ticker.C:
			conn, err := d.pool.get()
			if err != nil {
				continue
			}

			// Poll delayed items ready to run
			scoreLimit := strconv.FormatInt(now.UnixNano(), 10)
			// Check standard queue names
			for _, qName := range []string{"default", "high", "low"} {
				delayedKey := d.prefix + qName + ":delayed"
				destKey := d.prefix + qName

				res, err := conn.cmd("ZRANGEBYSCORE", delayedKey, "-inf", scoreLimit, "LIMIT", "0", "50")
				if err != nil {
					continue
				}

				arr, ok := res.([]any)
				if !ok || len(arr) == 0 {
					continue
				}

				for _, item := range arr {
					itemStr := fmt.Sprint(item)
					// Remove from sorted set and push to ready queue
					_, _ = conn.cmd("ZREM", delayedKey, itemStr)
					_, _ = conn.cmd("LPUSH", destKey, itemStr)
				}
			}

			d.pool.put(conn)
		}
	}
}

// RedisFailedJobStore stores Dead-Letter Queue items in a Redis Hash.
type RedisFailedJobStore struct {
	pool   *redisPool
	prefix string
}

// NewRedisFailedJobStore creates a Redis DLQ store.
func NewRedisFailedJobStore(cfg RedisConfig) *RedisFailedJobStore {
	if cfg.Prefix == "" {
		cfg.Prefix = "golaravel:queue:"
	}
	return &RedisFailedJobStore{
		pool:   newRedisPool(cfg, 5),
		prefix: cfg.Prefix + "failed",
	}
}

func (s *RedisFailedJobStore) Log(job *FailedJob) error {
	conn, err := s.pool.get()
	if err != nil {
		return err
	}
	defer s.pool.put(conn)

	data, err := json.Marshal(job)
	if err != nil {
		return err
	}

	_, err = conn.cmd("HSET", s.prefix, job.ID, string(data))
	return err
}

func (s *RedisFailedJobStore) All(limit, offset int) ([]*FailedJob, error) {
	conn, err := s.pool.get()
	if err != nil {
		return nil, err
	}
	defer s.pool.put(conn)

	res, err := conn.cmd("HGETALL", s.prefix)
	if err != nil {
		return nil, err
	}

	arr, ok := res.([]any)
	if !ok {
		return []*FailedJob{}, nil
	}

	var allJobs []*FailedJob
	for i := 1; i < len(arr); i += 2 {
		valStr := fmt.Sprint(arr[i])
		var fj FailedJob
		if err := json.Unmarshal([]byte(valStr), &fj); err == nil {
			allJobs = append(allJobs, &fj)
		}
	}

	if offset >= len(allJobs) {
		return []*FailedJob{}, nil
	}
	end := offset + limit
	if end > len(allJobs) || limit <= 0 {
		end = len(allJobs)
	}

	return allJobs[offset:end], nil
}

func (s *RedisFailedJobStore) Find(id string) (*FailedJob, error) {
	conn, err := s.pool.get()
	if err != nil {
		return nil, err
	}
	defer s.pool.put(conn)

	res, err := conn.cmd("HGET", s.prefix, id)
	if err != nil || res == nil {
		return nil, ErrJobNotFound
	}

	var fj FailedJob
	if err := json.Unmarshal([]byte(fmt.Sprint(res)), &fj); err != nil {
		return nil, err
	}
	return &fj, nil
}

func (s *RedisFailedJobStore) Delete(id string) error {
	conn, err := s.pool.get()
	if err != nil {
		return err
	}
	defer s.pool.put(conn)

	_, err = conn.cmd("HDEL", s.prefix, id)
	return err
}

func (s *RedisFailedJobStore) Flush() error {
	conn, err := s.pool.get()
	if err != nil {
		return err
	}
	defer s.pool.put(conn)

	_, err = conn.cmd("DEL", s.prefix)
	return err
}

func (s *RedisFailedJobStore) Count() (int64, error) {
	conn, err := s.pool.get()
	if err != nil {
		return 0, err
	}
	defer s.pool.put(conn)

	res, err := conn.cmd("HLEN", s.prefix)
	if err != nil {
		return 0, err
	}
	if n, ok := res.(int64); ok {
		return n, nil
	}
	return 0, nil
}

// --- Zero-Dependency RESP Protocol Client ---

type redisConn struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

func dialRedis(cfg RedisConfig) (*redisConn, error) {
	c, err := net.DialTimeout("tcp", cfg.Addr, cfg.Timeout)
	if err != nil {
		return nil, err
	}

	rc := &redisConn{
		conn:   c,
		reader: bufio.NewReader(c),
		writer: bufio.NewWriter(c),
	}

	// AUTH if required
	if cfg.Password != "" {
		if _, err := rc.cmd("AUTH", cfg.Password); err != nil {
			c.Close()
			return nil, err
		}
	}

	// SELECT DB if != 0
	if cfg.DB != 0 {
		if _, err := rc.cmd("SELECT", strconv.Itoa(cfg.DB)); err != nil {
			c.Close()
			return nil, err
		}
	}

	return rc, nil
}

func (rc *redisConn) cmd(args ...string) (any, error) {
	// Write RESP Array: *<len>\r\n$<len>\r\n<arg>\r\n...
	fmt.Fprintf(rc.writer, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(rc.writer, "$%d\r\n%s\r\n", len(arg), arg)
	}
	if err := rc.writer.Flush(); err != nil {
		return nil, err
	}

	return rc.readReply()
}

func (rc *redisConn) readReply() (any, error) {
	line, err := rc.reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) == 0 {
		return nil, io.EOF
	}

	switch line[0] {
	case '+': // Simple string
		return line[1:], nil
	case '-': // Error
		return nil, fmt.Errorf("redis: %s", line[1:])
	case ':': // Integer
		return strconv.ParseInt(line[1:], 10, 64)
	case '$': // Bulk string
		length, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if length == -1 {
			return nil, nil // Null bulk string
		}
		buf := make([]byte, length+2)
		if _, err := io.ReadFull(rc.reader, buf); err != nil {
			return nil, err
		}
		return string(buf[:length]), nil
	case '*': // Array
		count, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if count == -1 {
			return nil, nil // Null array
		}
		arr := make([]any, count)
		for i := 0; i < count; i++ {
			elem, err := rc.readReply()
			if err != nil {
				return nil, err
			}
			arr[i] = elem
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("redis: unexpected response prefix '%c'", line[0])
	}
}

type redisPool struct {
	cfg   RedisConfig
	conns chan *redisConn
	mu    sync.Mutex
}

func newRedisPool(cfg RedisConfig, size int) *redisPool {
	return &redisPool{
		cfg:   cfg,
		conns: make(chan *redisConn, size),
	}
}

func (p *redisPool) get() (*redisConn, error) {
	select {
	case c := <-p.conns:
		return c, nil
	default:
		return dialRedis(p.cfg)
	}
}

func (p *redisPool) put(c *redisConn) {
	if c == nil {
		return
	}
	select {
	case p.conns <- c:
	default:
		c.conn.Close()
	}
}

func (p *redisPool) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		select {
		case c := <-p.conns:
			c.conn.Close()
		default:
			return
		}
	}
}
