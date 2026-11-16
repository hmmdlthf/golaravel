package golaravel

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/middleware"
)

// slowPost sends headers and part of a body, then stalls.
func slowPost(t *testing.T, addr, path string) (string, time.Duration) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: x\r\nContent-Type: text/plain\r\nContent-Length: 100\r\n\r\npartial", path)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(line), time.Since(start)
}

func newBodyApp(t *testing.T, timeout time.Duration) *httptest.Server {
	app := New().BodyReadTimeout(timeout)
	read := func(c *gcontext.Context) error {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			return c.String(http.StatusRequestTimeout, "body too slow")
		}
		return c.String(http.StatusOK, "ok")
	}
	app.POST("/upload", read)
	app.POST("/big", middleware.BodyReadTimeout(time.Hour), read)
	app.POST("/slow-handler", func(c *gcontext.Context) error {
		_, _ = io.ReadAll(c.Request.Body)
		time.Sleep(3 * timeout) // longer than the body deadline
		if err := c.Request.Context().Err(); err != nil {
			return c.String(http.StatusInternalServerError, "context cancelled: %v", err)
		}
		return c.String(http.StatusOK, "ok")
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

func TestBodyReadTimeoutStopsTricklingClients(t *testing.T) {
	srv := newBodyApp(t, 200*time.Millisecond)
	addr := strings.TrimPrefix(srv.URL, "http://")
	status, took := slowPost(t, addr, "/upload")
	if !strings.Contains(status, "408") || took > 2*time.Second {
		t.Fatalf("stalled body: %q after %v, want 408 quickly", status, took)
	}
}

func TestBodyReadTimeoutPerRouteOverride(t *testing.T) {
	srv := newBodyApp(t, 200*time.Millisecond)
	addr := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /big HTTP/1.1\r\nHost: x\r\nContent-Length: 4\r\n\r\nab")
	time.Sleep(500 * time.Millisecond) // longer than the default deadline
	fmt.Fprint(conn, "cd")
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	if !strings.Contains(line, "200") {
		t.Fatalf("per-route timeout not applied: %q", line)
	}
}

func TestBodyReadTimeoutDoesNotCancelSlowHandlers(t *testing.T) {
	srv := newBodyApp(t, 100*time.Millisecond)
	resp, err := http.Post(srv.URL+"/slow-handler", "text/plain", strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("slow handler: %d %s", resp.StatusCode, b)
	}
}
