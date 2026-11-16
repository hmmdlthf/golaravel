// Package redistest runs a tiny in-process Redis stand-in for tests. It
// understands the commands GoLaravel's Redis stores use: PING, AUTH,
// SELECT, GET, SET (NX / XX / PX / EX), DEL, INCRBY, PEXPIRE, SCAN (MATCH
// with a trailing *) and the compare-and-delete EVAL used to release locks.
// It is not a Redis implementation.
package redistest

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type entry struct {
	val string
	exp time.Time
}

// Server is a running fake.
type Server struct {
	ln   net.Listener
	mu   sync.Mutex
	data map[string]entry
}

// Start starts a server on a random local port, stopped when t ends.
func Start(t testing.TB) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{ln: ln, data: map[string]entry{}}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

// Addr is the host:port to connect to.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Keys returns the live keys (for assertions).
func (s *Server) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.data {
		if _, ok := s.lookup(k); ok {
			out = append(out, k)
		}
	}
	return out
}

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	r, w := bufio.NewReader(c), bufio.NewWriter(c)
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		s.exec(w, args)
		if w.Flush() != nil {
			return
		}
	}
}

func readCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("bad command %q", line)
	}
	n, _ := strconv.Atoi(line[1:])
	args := make([]string, n)
	for i := range args {
		hdr, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		l, _ := strconv.Atoi(strings.TrimRight(hdr, "\r\n")[1:])
		buf := make([]byte, l+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args[i] = string(buf[:l])
	}
	return args, nil
}

func (s *Server) lookup(k string) (entry, bool) {
	e, ok := s.data[k]
	if ok && !e.exp.IsZero() && time.Now().After(e.exp) {
		delete(s.data, k)
		return entry{}, false
	}
	return e, ok
}

func bulk(w *bufio.Writer, v string) { fmt.Fprintf(w, "$%d\r\n%s\r\n", len(v), v) }

func (s *Server) exec(w *bufio.Writer, args []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(args) == 0 {
		w.WriteString("-ERR empty command\r\n")
		return
	}
	switch strings.ToUpper(args[0]) {
	case "PING", "AUTH", "SELECT":
		w.WriteString("+OK\r\n")
	case "GET":
		if e, ok := s.lookup(args[1]); ok {
			bulk(w, e.val)
		} else {
			w.WriteString("$-1\r\n")
		}
	case "SET":
		k, v := args[1], args[2]
		var exp time.Time
		nx, xx := false, false
		for i := 3; i < len(args); i++ {
			switch strings.ToUpper(args[i]) {
			case "NX":
				nx = true
			case "XX":
				xx = true
			case "PX":
				ms, _ := strconv.ParseInt(args[i+1], 10, 64)
				exp = time.Now().Add(time.Duration(ms) * time.Millisecond)
				i++
			case "EX":
				sec, _ := strconv.ParseInt(args[i+1], 10, 64)
				exp = time.Now().Add(time.Duration(sec) * time.Second)
				i++
			}
		}
		_, exists := s.lookup(k)
		if (nx && exists) || (xx && !exists) {
			w.WriteString("$-1\r\n")
			return
		}
		s.data[k] = entry{val: v, exp: exp}
		w.WriteString("+OK\r\n")
	case "DEL":
		n := 0
		for _, k := range args[1:] {
			if _, ok := s.lookup(k); ok {
				delete(s.data, k)
				n++
			}
		}
		fmt.Fprintf(w, ":%d\r\n", n)
	case "INCRBY":
		e, _ := s.lookup(args[1])
		cur := int64(0)
		if e.val != "" {
			v, err := strconv.ParseInt(e.val, 10, 64)
			if err != nil {
				w.WriteString("-ERR value is not an integer or out of range\r\n")
				return
			}
			cur = v
		}
		d, _ := strconv.ParseInt(args[2], 10, 64)
		cur += d
		s.data[args[1]] = entry{val: strconv.FormatInt(cur, 10), exp: e.exp}
		fmt.Fprintf(w, ":%d\r\n", cur)
	case "PEXPIRE":
		e, ok := s.lookup(args[1])
		if !ok {
			w.WriteString(":0\r\n")
			return
		}
		ms, _ := strconv.ParseInt(args[2], 10, 64)
		e.exp = time.Now().Add(time.Duration(ms) * time.Millisecond)
		s.data[args[1]] = e
		w.WriteString(":1\r\n")
	case "SCAN":
		pattern := "*"
		for i := 2; i < len(args)-1; i++ {
			if strings.ToUpper(args[i]) == "MATCH" {
				pattern = args[i+1]
			}
		}
		prefix := strings.TrimSuffix(pattern, "*")
		var keys []string
		for k := range s.data {
			if _, ok := s.lookup(k); ok && strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		fmt.Fprintf(w, "*2\r\n")
		bulk(w, "0")
		fmt.Fprintf(w, "*%d\r\n", len(keys))
		for _, k := range keys {
			bulk(w, k)
		}
	case "EVAL":
		// Only the compare-and-delete script: EVAL <script> 1 <key> <value>.
		if len(args) < 5 {
			w.WriteString("-ERR wrong number of arguments\r\n")
			return
		}
		if e, ok := s.lookup(args[3]); ok && e.val == args[4] {
			delete(s.data, args[3])
			w.WriteString(":1\r\n")
			return
		}
		w.WriteString(":0\r\n")
	default:
		fmt.Fprintf(w, "-ERR unknown command '%s'\r\n", args[0])
	}
}
