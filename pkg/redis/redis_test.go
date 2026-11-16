package redis_test

import (
	"errors"
	"testing"

	"github.com/golaravel/golaravel/pkg/redis"
	"github.com/golaravel/golaravel/pkg/redis/redistest"
)

func TestClientBasics(t *testing.T) {
	srv := redistest.Start(t)
	c, err := redis.New(redis.Config{Addr: srv.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Do("SET", "k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := redis.String(c.Do("GET", "k")); err != nil || !ok || v != "v" {
		t.Fatalf("GET: %q %v %v", v, ok, err)
	}
	if _, ok, _ := redis.String(c.Do("GET", "missing")); ok {
		t.Fatal("missing key reported present")
	}
	if n, err := redis.Int(c.Do("INCRBY", "n", "5")); err != nil || n != 5 {
		t.Fatalf("INCRBY: %d %v", n, err)
	}
	var re redis.Error
	if _, err := c.Do("NOPE"); !errors.As(err, &re) {
		t.Fatalf("server error reply: %v", err)
	}
	// The connection survives an error reply.
	if _, err := c.Do("PING"); err != nil {
		t.Fatal(err)
	}
	var seen []string
	_ = c.Scan("*", func(keys []string) error { seen = append(seen, keys...); return nil })
	if len(seen) != 2 {
		t.Fatalf("scan: %v", seen)
	}
	c.Close()
	if _, err := c.Do("PING"); !errors.Is(err, redis.ErrClosed) {
		t.Fatalf("after Close: %v", err)
	}
}

func TestNewFailsWithoutServer(t *testing.T) {
	if _, err := redis.New(redis.Config{Addr: "127.0.0.1:1"}); err == nil {
		t.Fatal("expected a connection error")
	}
}
