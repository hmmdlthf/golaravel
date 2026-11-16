package ratelimit

import (
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/golaravel/golaravel/pkg/redis"
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

// RedisStore is a sliding-window rate limit store in Redis sorted sets,
// shared by every instance using the same Redis.
type RedisStore struct {
	cfg    RedisConfig
	client *redis.Client
}

// NewRedisStore creates a new Redis rate limiting store.
func NewRedisStore(cfg RedisConfig) (*RedisStore, error) {
	if cfg.Prefix == "" {
		cfg.Prefix = "golaravel:ratelimit:"
	}
	client, err := redis.New(redis.Config{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB, Timeout: cfg.Timeout, PoolSize: 16})
	if err != nil {
		return nil, err
	}
	return &RedisStore{cfg: cfg, client: client}, nil
}

// Close releases the store's connections.
func (s *RedisStore) Close() error { return s.client.Close() }

// window trims attempts older than the window and returns the count and
// the oldest remaining timestamp (ns, 0 when empty).
func (s *RedisStore) window(redisKey string, nowNano, windowNano int64) (int64, int64, error) {
	if _, err := s.client.Do("ZREMRANGEBYSCORE", redisKey, "-inf", strconv.FormatInt(nowNano-windowNano, 10)); err != nil {
		return 0, 0, err
	}
	count, err := redis.Int(s.client.Do("ZCARD", redisKey))
	if err != nil {
		return 0, 0, err
	}
	var oldest int64
	if list, ok := mustList(s.client.Do("ZRANGE", redisKey, "0", "0", "WITHSCORES")); ok && len(list) >= 2 {
		if score, ok := list[1].(string); ok {
			f, _ := strconv.ParseFloat(score, 64)
			oldest = int64(f)
		}
	}
	return count, oldest, nil
}

func mustList(reply any, err error) ([]any, bool) {
	if err != nil {
		return nil, false
	}
	l, ok := reply.([]any)
	return l, ok
}

func denied(maxAttempts int, oldestNano, windowNano, nowNano int64) Result {
	resetNano := oldestNano + windowNano
	retry := resetNano - nowNano
	if retry < 0 {
		retry = 0
	}
	return Result{Allowed: false, Limit: maxAttempts, Remaining: 0, ResetTime: time.Unix(0, resetNano), RetryAfter: time.Duration(retry)}
}

// hitScript trims the window, counts, and adds the attempt only when under
// the limit, atomically: concurrent requests on several instances can never
// be admitted past maxAttempts. KEYS[1] = key; ARGV = cutoff, now, max,
// member, expire seconds. Returns {allowed, count, oldest score}.
const hitScript = `
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
local count = redis.call('ZCARD', KEYS[1])
local first = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
local oldest = first[2] or ''
if count >= tonumber(ARGV[3]) then
  return {0, count, oldest}
end
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[4])
redis.call('EXPIRE', KEYS[1], ARGV[5])
return {1, count + 1, oldest}
`

// Hit records an attempt using a Redis ZSET sliding window, atomically
// (one Lua script), so a burst of concurrent requests never gets past the
// limit.
func (s *RedisStore) Hit(key string, maxAttempts int, window time.Duration) (Result, error) {
	redisKey := s.cfg.Prefix + key
	nowNano := time.Now().UnixNano()
	windowNano := window.Nanoseconds()
	expireSecs := int(math.Ceil(window.Seconds())) + 2
	member := fmt.Sprintf("%d:%s", nowNano, randomHex(8))

	reply, err := s.client.Do("EVAL", hitScript, "1", redisKey,
		strconv.FormatInt(nowNano-windowNano, 10), strconv.FormatInt(nowNano, 10),
		strconv.Itoa(maxAttempts), member, strconv.Itoa(expireSecs))
	if err != nil {
		return Result{}, err
	}
	parts, ok := reply.([]any)
	if !ok || len(parts) != 3 {
		return Result{}, fmt.Errorf("ratelimit: unexpected script reply %v", reply)
	}
	allowed, _ := redis.Int(parts[0], nil)
	count, _ := redis.Int(parts[1], nil)
	var oldest int64
	if str, ok := parts[2].(string); ok && str != "" {
		f, _ := strconv.ParseFloat(str, 64)
		oldest = int64(f)
	}
	if oldest == 0 {
		oldest = nowNano
	}
	if allowed != 1 {
		return denied(maxAttempts, oldest, windowNano, nowNano), nil
	}
	return Result{
		Allowed:   true,
		Limit:     maxAttempts,
		Remaining: maxAttempts - int(count),
		ResetTime: time.Unix(0, oldest+windowNano),
	}, nil
}

// Status inspects the current rate limit without appending a new attempt.
func (s *RedisStore) Status(key string, maxAttempts int, window time.Duration) (Result, error) {
	redisKey := s.cfg.Prefix + key
	nowNano := time.Now().UnixNano()
	windowNano := window.Nanoseconds()
	count, oldest, err := s.window(redisKey, nowNano, windowNano)
	if err != nil {
		return Result{}, err
	}
	if count >= int64(maxAttempts) {
		return denied(maxAttempts, oldest, windowNano, nowNano), nil
	}
	resetNano := nowNano + windowNano
	if oldest > 0 {
		resetNano = oldest + windowNano
	}
	return Result{Allowed: true, Limit: maxAttempts, Remaining: maxAttempts - int(count), ResetTime: time.Unix(0, resetNano)}, nil
}

// Reset clears the Redis key.
func (s *RedisStore) Reset(key string) error {
	_, err := s.client.Do("DEL", s.cfg.Prefix+key)
	return err
}

// Clear removes all keys under the prefix (SCAN, not KEYS, so a large
// Redis is not blocked).
func (s *RedisStore) Clear() error {
	return s.client.Scan(s.cfg.Prefix+"*", func(keys []string) error {
		_, err := s.client.Do(append([]string{"DEL"}, keys...)...)
		return err
	})
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = io.ReadFull(rand.Reader, b)
	return fmt.Sprintf("%x", b)
}
