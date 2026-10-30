package ratelimit

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RedisConfig holds connection parameters for the Redis rate limit backend.
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	Prefix   string
	Timeout  time.Duration
}

// DefaultRedisConfig generates configuration from environment variables.
func DefaultRedisConfig() RedisConfig {
	addr := os.Getenv("REDIS_HOST")
	if addr == "" {
		addr = "127.0.0.1"
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}

	return RedisConfig{
		Addr:     addr + ":" + port,
		Password: os.Getenv("REDIS_PASSWORD"),
		Prefix:   "golaravel:ratelimit:",
		Timeout:  3 * time.Second,
	}
}

type redisConn struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

func dialRedis(cfg RedisConfig) (*redisConn, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	c, err := net.DialTimeout("tcp", cfg.Addr, timeout)
	if err != nil {
		return nil, err
	}

	rc := &redisConn{
		conn:   c,
		reader: bufio.NewReader(c),
		writer: bufio.NewWriter(c),
	}

	if cfg.Password != "" {
		if _, err := rc.cmd("AUTH", cfg.Password); err != nil {
			c.Close()
			return nil, err
		}
	}

	if cfg.DB != 0 {
		if _, err := rc.cmd("SELECT", strconv.Itoa(cfg.DB)); err != nil {
			c.Close()
			return nil, err
		}
	}

	return rc, nil
}

func (rc *redisConn) cmd(args ...string) (any, error) {
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
	case '+':
		return line[1:], nil
	case '-':
		return nil, fmt.Errorf("redis: %s", line[1:])
	case ':':
		return strconv.ParseInt(line[1:], 10, 64)
	case '$':
		length, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if length == -1 {
			return nil, nil
		}
		buf := make([]byte, length+2)
		if _, err := io.ReadFull(rc.reader, buf); err != nil {
			return nil, err
		}
		return string(buf[:length]), nil
	case '*':
		count, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if count == -1 {
			return nil, nil
		}
		items := make([]any, count)
		for i := 0; i < count; i++ {
			item, err := rc.readReply()
			if err != nil {
				return nil, err
			}
			items[i] = item
		}
		return items, nil
	default:
		return nil, fmt.Errorf("redis: unknown reply type '%c'", line[0])
	}
}

func (rc *redisConn) close() {
	if rc.conn != nil {
		_ = rc.conn.Close()
	}
}

// RedisStore provides distributed sliding-window rate limiting backed by Redis sorted sets (ZSET).
type RedisStore struct {
	cfg   RedisConfig
	mu    sync.Mutex
	conns []*redisConn
}

// NewRedisStore creates a new Redis rate limiting store.
func NewRedisStore(cfg RedisConfig) (*RedisStore, error) {
	if cfg.Prefix == "" {
		cfg.Prefix = "golaravel:ratelimit:"
	}

	// Verify connection
	conn, err := dialRedis(cfg)
	if err != nil {
		return nil, err
	}
	conn.close()

	return &RedisStore{
		cfg:   cfg,
		conns: make([]*redisConn, 0, 8),
	}, nil
}

func (s *RedisStore) acquireConn() (*redisConn, error) {
	s.mu.Lock()
	if len(s.conns) > 0 {
		c := s.conns[len(s.conns)-1]
		s.conns = s.conns[:len(s.conns)-1]
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()

	return dialRedis(s.cfg)
}

func (s *RedisStore) releaseConn(c *redisConn, err error) {
	if err != nil {
		c.close()
		return
	}
	s.mu.Lock()
	if len(s.conns) < 16 {
		s.conns = append(s.conns, c)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	c.close()
}

// Hit records an attempt using Redis ZSET sliding-window algorithm.
func (s *RedisStore) Hit(key string, maxAttempts int, window time.Duration) (Result, error) {
	conn, err := s.acquireConn()
	if err != nil {
		return Result{}, err
	}
	defer func() { s.releaseConn(conn, err) }()

	redisKey := s.cfg.Prefix + key
	now := time.Now()
	nowNano := now.UnixNano()
	windowNano := window.Nanoseconds()
	cutoffNano := nowNano - windowNano

	// 1. Remove expired timestamps: ZREMRANGEBYSCORE key -inf cutoff
	_, err = conn.cmd("ZREMRANGEBYSCORE", redisKey, "-inf", strconv.FormatInt(cutoffNano, 10))
	if err != nil {
		return Result{}, err
	}

	// 2. Count active elements in window: ZCARD key
	cardReply, err := conn.cmd("ZCARD", redisKey)
	if err != nil {
		return Result{}, err
	}
	count, _ := cardReply.(int64)

	// 3. Inspect oldest timestamp to compute reset and retryAfter
	var oldestNano int64 = 0
	oldestReply, err := conn.cmd("ZRANGE", redisKey, "0", "0", "WITHSCORES")
	if err == nil {
		if list, ok := oldestReply.([]any); ok && len(list) >= 2 {
			if scoreStr, ok := list[1].(string); ok {
				oldestNano, _ = strconv.ParseInt(scoreStr, 10, 64)
			}
		}
	}

	if count >= int64(maxAttempts) {
		resetNano := oldestNano + windowNano
		retryAfterNano := resetNano - nowNano
		if retryAfterNano < 0 {
			retryAfterNano = 0
		}
		return Result{
			Allowed:    false,
			Limit:      maxAttempts,
			Remaining:  0,
			ResetTime:  time.Unix(0, resetNano),
			RetryAfter: time.Duration(retryAfterNano),
		}, nil
	}

	// 4. Under limit: append attempt ZADD key nowNano nowNano:rand
	member := fmt.Sprintf("%d:%s", nowNano, randomHex(4))
	_, err = conn.cmd("ZADD", redisKey, strconv.FormatInt(nowNano, 10), member)
	if err != nil {
		return Result{}, err
	}

	// 5. Update TTL: EXPIRE key ceil(window.Seconds)+1
	expireSecs := int(math.Ceil(window.Seconds())) + 2
	_, _ = conn.cmd("EXPIRE", redisKey, strconv.Itoa(expireSecs))

	newCount := int(count + 1)
	remaining := maxAttempts - newCount
	if oldestNano == 0 {
		oldestNano = nowNano
	}
	resetNano := oldestNano + windowNano

	return Result{
		Allowed:    true,
		Limit:      maxAttempts,
		Remaining:  remaining,
		ResetTime:  time.Unix(0, resetNano),
		RetryAfter: 0,
	}, nil
}

// Status inspects the current rate limit without appending a new attempt.
func (s *RedisStore) Status(key string, maxAttempts int, window time.Duration) (Result, error) {
	conn, err := s.acquireConn()
	if err != nil {
		return Result{}, err
	}
	defer func() { s.releaseConn(conn, err) }()

	redisKey := s.cfg.Prefix + key
	now := time.Now()
	nowNano := now.UnixNano()
	windowNano := window.Nanoseconds()
	cutoffNano := nowNano - windowNano

	// Remove expired
	_, err = conn.cmd("ZREMRANGEBYSCORE", redisKey, "-inf", strconv.FormatInt(cutoffNano, 10))
	if err != nil {
		return Result{}, err
	}

	// Count
	cardReply, err := conn.cmd("ZCARD", redisKey)
	if err != nil {
		return Result{}, err
	}
	count, _ := cardReply.(int64)

	var oldestNano int64 = 0
	oldestReply, err := conn.cmd("ZRANGE", redisKey, "0", "0", "WITHSCORES")
	if err == nil {
		if list, ok := oldestReply.([]any); ok && len(list) >= 2 {
			if scoreStr, ok := list[1].(string); ok {
				oldestNano, _ = strconv.ParseInt(scoreStr, 10, 64)
			}
		}
	}

	if count >= int64(maxAttempts) {
		resetNano := oldestNano + windowNano
		retryAfterNano := resetNano - nowNano
		if retryAfterNano < 0 {
			retryAfterNano = 0
		}
		return Result{
			Allowed:    false,
			Limit:      maxAttempts,
			Remaining:  0,
			ResetTime:  time.Unix(0, resetNano),
			RetryAfter: time.Duration(retryAfterNano),
		}, nil
	}

	remaining := maxAttempts - int(count)
	resetNano := nowNano + windowNano
	if oldestNano > 0 {
		resetNano = oldestNano + windowNano
	}

	return Result{
		Allowed:    true,
		Limit:      maxAttempts,
		Remaining:  remaining,
		ResetTime:  time.Unix(0, resetNano),
		RetryAfter: 0,
	}, nil
}

// Reset clears the Redis key.
func (s *RedisStore) Reset(key string) error {
	conn, err := s.acquireConn()
	if err != nil {
		return err
	}
	defer func() { s.releaseConn(conn, err) }()

	_, err = conn.cmd("DEL", s.cfg.Prefix+key)
	return err
}

// Clear removes all keys matching the prefix.
func (s *RedisStore) Clear() error {
	conn, err := s.acquireConn()
	if err != nil {
		return err
	}
	defer func() { s.releaseConn(conn, err) }()

	keysReply, err := conn.cmd("KEYS", s.cfg.Prefix+"*")
	if err != nil {
		return err
	}
	if keys, ok := keysReply.([]any); ok && len(keys) > 0 {
		args := []string{"DEL"}
		for _, k := range keys {
			if kStr, ok := k.(string); ok {
				args = append(args, kStr)
			}
		}
		_, err = conn.cmd(args...)
		return err
	}
	return nil
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = io.ReadFull(rand.Reader, b)
	return fmt.Sprintf("%x", b)
}
