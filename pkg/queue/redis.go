package queue

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/goccy/go-json"

	"github.com/golaravel/golaravel/pkg/redis"
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
	client   *redis.Client
	queues   sync.Map // queue names that have delayed jobs
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

	client, err := redis.New(redis.Config{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB, Timeout: cfg.Timeout, PoolSize: 10})
	if err != nil {
		return nil, fmt.Errorf("queue: failed to connect to redis at %s: %w", cfg.Addr, err)
	}

	d := &RedisDriver{
		cfg:      cfg,
		client:   client,
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

	_, err = d.client.Do("LPUSH", key, string(data))
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
	d.queues.Store(queue, struct{}{})
	score := strconv.FormatInt(available.UnixNano(), 10)

	_, err = d.client.Do("ZADD", key, score, string(data))
	return err
}

// Pop pops the next ready job from Redis via RPOP or BRPOP.
func (d *RedisDriver) Pop(queue string, timeout time.Duration) (*JobPayload, error) {
	key := d.prefix + queue

	var raw string
	if timeout > 0 {
		secs := int(timeout.Seconds())
		if secs <= 0 {
			secs = 1
		}
		// BRPOP returns [key, element]
		res, err := d.client.DoTimeout(time.Duration(secs)*time.Second, "BRPOP", key, strconv.Itoa(secs))
		if err != nil {
			return nil, err
		}
		arr, ok := res.([]any)
		if !ok || len(arr) < 2 || arr[1] == nil {
			return nil, ErrQueueEmpty
		}
		raw = fmt.Sprint(arr[1])
	} else {
		res, err := d.client.Do("RPOP", key)
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

	res, err := d.client.Do("LLEN", key)
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

	_, _ = d.client.Do("DEL", d.prefix+queue)
	_, _ = d.client.Do("DEL", d.prefix+queue+":delayed")
	return nil
}

// Close gracefully closes the Redis driver and worker connection pool.
func (d *RedisDriver) Close() {
	select {
	case <-d.stopChan:
	default:
		close(d.stopChan)
	}
	_ = d.client.Close()
}

func (d *RedisDriver) processDelayed() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopChan:
			return
		case now := <-ticker.C:
			d.moveDueJobs(now)
		}
	}
}

// moveDueJobs moves delayed jobs whose time has come onto their ready
// queue: the standard queues plus every queue Later was called for.
func (d *RedisDriver) moveDueJobs(now time.Time) {
	names := map[string]struct{}{"default": {}, "high": {}, "low": {}}
	d.queues.Range(func(k, _ any) bool {
		names[k.(string)] = struct{}{}
		return true
	})
	scoreLimit := strconv.FormatInt(now.UnixNano(), 10)
	for qName := range names {
		delayedKey := d.prefix + qName + ":delayed"
		destKey := d.prefix + qName

		res, err := d.client.Do("ZRANGEBYSCORE", delayedKey, "-inf", scoreLimit, "LIMIT", "0", "50")
		if err != nil {
			continue
		}
		arr, ok := res.([]any)
		if !ok {
			continue
		}
		for _, item := range arr {
			itemStr := fmt.Sprint(item)
			// Only the worker whose ZREM removed the job pushes it, so two
			// workers sharing Redis never run it twice.
			if n, err := redis.Int(d.client.Do("ZREM", delayedKey, itemStr)); err != nil || n != 1 {
				continue
			}
			_, _ = d.client.Do("LPUSH", destKey, itemStr)
		}
	}
}

// RedisFailedJobStore stores Dead-Letter Queue items in a Redis Hash.
type RedisFailedJobStore struct {
	client *redis.Client
	prefix string
}

// NewRedisFailedJobStore creates a Redis DLQ store.
func NewRedisFailedJobStore(cfg RedisConfig) *RedisFailedJobStore {
	if cfg.Prefix == "" {
		cfg.Prefix = "golaravel:queue:"
	}
	return &RedisFailedJobStore{
		client: redis.Open(redis.Config{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB, Timeout: cfg.Timeout, PoolSize: 5}),
		prefix: cfg.Prefix + "failed",
	}
}

func (s *RedisFailedJobStore) Log(job *FailedJob) error {

	data, err := json.Marshal(job)
	if err != nil {
		return err
	}

	_, err = s.client.Do("HSET", s.prefix, job.ID, string(data))
	return err
}

func (s *RedisFailedJobStore) All(limit, offset int) ([]*FailedJob, error) {

	res, err := s.client.Do("HGETALL", s.prefix)
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

	res, err := s.client.Do("HGET", s.prefix, id)
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
	_, err := s.client.Do("HDEL", s.prefix, id)
	return err
}

func (s *RedisFailedJobStore) Flush() error {
	_, err := s.client.Do("DEL", s.prefix)
	return err
}

func (s *RedisFailedJobStore) Count() (int64, error) {
	res, err := s.client.Do("HLEN", s.prefix)
	if err != nil {
		return 0, err
	}
	if n, ok := res.(int64); ok {
		return n, nil
	}
	return 0, nil
}
