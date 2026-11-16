package cache

import (
	"strconv"
	"time"

	"github.com/golaravel/golaravel/pkg/redis"
)

// RedisOptions configures NewRedisStore.
type RedisOptions struct {
	// Prefix for every key, default "golaravel:cache:". Flush only removes
	// keys with this prefix.
	Prefix string
}

// RedisBackend keeps cache entries and locks in Redis.
type RedisBackend struct {
	c      *redis.Client
	prefix string
}

// NewRedisStore returns a Store backed by Redis.
func NewRedisStore(client *redis.Client, opts ...RedisOptions) *BackendStore {
	b := &RedisBackend{c: client, prefix: "golaravel:cache:"}
	if len(opts) > 0 && opts[0].Prefix != "" {
		b.prefix = opts[0].Prefix
	}
	return NewBackendStore(b)
}

func px(ttl time.Duration) []string {
	if ttl <= 0 {
		return nil
	}
	ms := ttl.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return []string{"PX", strconv.FormatInt(ms, 10)}
}

func (b *RedisBackend) GetRaw(key string) ([]byte, bool, error) {
	v, ok, err := redis.String(b.c.Do("GET", b.prefix+key))
	if err != nil || !ok {
		return nil, false, err
	}
	return []byte(v), true, nil
}

func (b *RedisBackend) SetRaw(key string, val []byte, ttl time.Duration) error {
	_, err := b.c.Do(append([]string{"SET", b.prefix + key, string(val)}, px(ttl)...)...)
	return err
}

func (b *RedisBackend) AddRaw(key string, val []byte, ttl time.Duration) (bool, error) {
	reply, err := b.c.Do(append([]string{"SET", b.prefix + key, string(val), "NX"}, px(ttl)...)...)
	return reply != nil, err
}

func (b *RedisBackend) IncrBy(key string, by int64) (int64, error) {
	return redis.Int(b.c.Do("INCRBY", b.prefix+key, strconv.FormatInt(by, 10)))
}

func (b *RedisBackend) Delete(key string) error {
	_, err := b.c.Do("DEL", b.prefix+key)
	return err
}

// Flush deletes every key under the prefix (SCAN + DEL, never FLUSHDB).
func (b *RedisBackend) Flush() error {
	return b.c.Scan(b.prefix+"*", func(keys []string) error {
		_, err := b.c.Do(append([]string{"DEL"}, keys...)...)
		return err
	})
}

func (b *RedisBackend) lockKey(name string) string { return b.prefix + "lock:" + name }

// releaseScript deletes the lock only if the caller still owns it.
const releaseScript = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`

// Acquire takes the lock, or refreshes it when owner already holds it.
func (b *RedisBackend) Acquire(name, owner string, duration time.Duration) (bool, error) {
	key := b.lockKey(name)
	reply, err := b.c.Do(append([]string{"SET", key, owner, "NX"}, px(duration)...)...)
	if err != nil {
		return false, err
	}
	if reply != nil {
		return true, nil
	}
	cur, ok, err := redis.String(b.c.Do("GET", key))
	if err != nil || !ok || cur != owner {
		return false, err
	}
	if p := px(duration); p != nil {
		if _, err := b.c.Do("PEXPIRE", key, p[1]); err != nil {
			return false, err
		}
	}
	return true, nil
}

// Release frees the lock; ErrLockNotOwned if another owner holds it.
func (b *RedisBackend) Release(name, owner string) error {
	key := b.lockKey(name)
	n, err := redis.Int(b.c.Do("EVAL", releaseScript, "1", key, owner))
	if err != nil || n == 1 {
		return err
	}
	cur, ok, err := redis.String(b.c.Do("GET", key))
	if err != nil {
		return err
	}
	if ok && cur != owner {
		return ErrLockNotOwned
	}
	return nil
}
