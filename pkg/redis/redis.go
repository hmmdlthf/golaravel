// Package redis is a small, dependency-free Redis client (RESP2) with a
// connection pool, shared by GoLaravel's Redis-backed stores (sessions,
// cache). It speaks only what those stores need: send a command, read the
// reply.
package redis

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config configures a Client.
type Config struct {
	Addr     string        // host:port, default "127.0.0.1:6379"
	Password string        // optional AUTH password
	Username string        // optional ACL user (Redis 6+), used with Password
	DB       int           // database index
	Timeout  time.Duration // dial and per-command read/write timeout, default 5s
	PoolSize int           // idle connections kept, default 10
}

// Error is an error reply sent by the server (for example WRONGTYPE).
type Error string

func (e Error) Error() string { return "redis: " + string(e) }

// ErrClosed is returned by Do after Close.
var ErrClosed = errors.New("redis: client is closed")

// Client is a pooled Redis connection. It is safe for concurrent use.
type Client struct {
	cfg    Config
	idle   chan *conn
	mu     sync.Mutex
	closed bool
}

type conn struct {
	nc net.Conn
	r  *bufio.Reader
	w  *bufio.Writer
}

// New creates a client and checks the server answers PING.
func New(cfg Config) (*Client, error) {
	c := Open(cfg)
	if _, err := c.Do("PING"); err != nil {
		return nil, fmt.Errorf("redis: connect to %s: %w", c.cfg.Addr, err)
	}
	return c, nil
}

// Open creates a client without contacting the server; connections are
// made on first use.
func Open(cfg Config) *Client {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:6379"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = 10
	}
	return &Client{cfg: cfg, idle: make(chan *conn, cfg.PoolSize)}
}

func (c *Client) dial() (*conn, error) {
	nc, err := net.DialTimeout("tcp", c.cfg.Addr, c.cfg.Timeout)
	if err != nil {
		return nil, err
	}
	cn := &conn{nc: nc, r: bufio.NewReader(nc), w: bufio.NewWriter(nc)}
	if c.cfg.Password != "" {
		args := []string{"AUTH", c.cfg.Password}
		if c.cfg.Username != "" {
			args = []string{"AUTH", c.cfg.Username, c.cfg.Password}
		}
		if _, err := c.roundTrip(cn, args); err != nil {
			nc.Close()
			return nil, err
		}
	}
	if c.cfg.DB != 0 {
		if _, err := c.roundTrip(cn, []string{"SELECT", strconv.Itoa(c.cfg.DB)}); err != nil {
			nc.Close()
			return nil, err
		}
	}
	return cn, nil
}

func (c *Client) get() (*conn, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	select {
	case cn := <-c.idle:
		return cn, nil
	default:
		return c.dial()
	}
}

func (c *Client) put(cn *conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		cn.nc.Close()
		return
	}
	select {
	case c.idle <- cn:
	default:
		cn.nc.Close()
	}
}

// Do sends one command and returns its reply: string (simple and bulk
// strings), int64, []any, or nil for a null reply. A server error reply is
// returned as Error.
func (c *Client) Do(args ...string) (any, error) {
	return c.DoTimeout(0, args...)
}

// DoTimeout is Do for blocking commands (BRPOP, BLPOP, ...): the reply may
// take up to block longer than the normal command timeout.
func (c *Client) DoTimeout(block time.Duration, args ...string) (any, error) {
	cn, err := c.get()
	if err != nil {
		return nil, err
	}
	reply, err := c.roundTripTimeout(cn, args, c.cfg.Timeout+block)
	var replyErr Error
	if err != nil && !errors.As(err, &replyErr) {
		// Network or protocol failure: the connection state is unknown.
		cn.nc.Close()
		return nil, err
	}
	c.put(cn)
	return reply, err
}

func (c *Client) roundTrip(cn *conn, args []string) (any, error) {
	return c.roundTripTimeout(cn, args, c.cfg.Timeout)
}

func (c *Client) roundTripTimeout(cn *conn, args []string, timeout time.Duration) (any, error) {
	_ = cn.nc.SetDeadline(time.Now().Add(timeout))
	fmt.Fprintf(cn.w, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(cn.w, "$%d\r\n%s\r\n", len(a), a)
	}
	if err := cn.w.Flush(); err != nil {
		return nil, err
	}
	return readReply(cn.r)
}

func readReply(r *bufio.Reader) (any, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil, io.ErrUnexpectedEOF
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return nil, Error(line[1:])
	case ':':
		return strconv.ParseInt(line[1:], 10, 64)
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		out := make([]any, n)
		for i := range out {
			if out[i], err = readReply(r); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("redis: unexpected reply %q", line)
}

// Close closes idle connections; later commands fail with ErrClosed.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	for {
		select {
		case cn := <-c.idle:
			cn.nc.Close()
		default:
			return nil
		}
	}
}

// String returns a string reply ("", false for a null reply).
func String(reply any, err error) (string, bool, error) {
	if err != nil || reply == nil {
		return "", false, err
	}
	switch v := reply.(type) {
	case string:
		return v, true, nil
	case int64:
		return strconv.FormatInt(v, 10), true, nil
	}
	return "", false, fmt.Errorf("redis: unexpected reply type %T", reply)
}

// Int returns an integer reply.
func Int(reply any, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	switch v := reply.(type) {
	case int64:
		return v, nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	case nil:
		return 0, nil
	}
	return 0, fmt.Errorf("redis: unexpected reply type %T", reply)
}

// Scan calls fn for every key matching pattern (SCAN, not KEYS, so large
// databases are not blocked).
func (c *Client) Scan(pattern string, fn func(keys []string) error) error {
	cursor := "0"
	for {
		reply, err := c.Do("SCAN", cursor, "MATCH", pattern, "COUNT", "500")
		if err != nil {
			return err
		}
		parts, ok := reply.([]any)
		if !ok || len(parts) != 2 {
			return fmt.Errorf("redis: unexpected SCAN reply %v", reply)
		}
		cursor, _ = parts[0].(string)
		items, _ := parts[1].([]any)
		keys := make([]string, 0, len(items))
		for _, it := range items {
			if s, ok := it.(string); ok {
				keys = append(keys, s)
			}
		}
		if len(keys) > 0 {
			if err := fn(keys); err != nil {
				return err
			}
		}
		if cursor == "0" || cursor == "" {
			return nil
		}
	}
}
