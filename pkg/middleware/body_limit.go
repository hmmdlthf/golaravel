package middleware

import (
	"io"
	"net/http"

	"github.com/golaravel/golaravel/pkg/context"
)

// LimitedBody is the request body reader golaravel's engine installs to
// cap request sizes (Engine.MaxBodyBytes). Unlike http.MaxBytesReader its
// limit can change while the request runs, which MaxBodySize uses to give
// one route a bigger (or smaller) limit than the rest of the app.
type LimitedBody struct {
	rc    io.ReadCloser
	limit int64 // <= 0: unlimited
	n     int64
	err   error
}

// NewLimitedBody caps rc at limit bytes (limit <= 0: no cap). Reading past
// the cap fails with *http.MaxBytesError.
func NewLimitedBody(rc io.ReadCloser, limit int64) *LimitedBody {
	return &LimitedBody{rc: rc, limit: limit}
}

// SetLimit changes the cap. n <= 0 removes it.
func (b *LimitedBody) SetLimit(n int64) { b.limit = n }

// Limit returns the current cap (<= 0: none).
func (b *LimitedBody) Limit() int64 { return b.limit }

func (b *LimitedBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.limit <= 0 {
		n, err := b.rc.Read(p)
		b.n += int64(n)
		return n, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	remaining := b.limit - b.n
	if remaining < 0 {
		remaining = 0
	}
	// read one byte past the limit to tell "exactly at" from "over"
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := b.rc.Read(p)
	if int64(n) <= remaining {
		b.n += int64(n)
		b.err = err
		return n, err
	}
	b.n += remaining
	b.err = &http.MaxBytesError{Limit: b.limit}
	return int(remaining), b.err
}

func (b *LimitedBody) Close() error { return b.rc.Close() }

// BodyLimitKey is the context key the engine stores the request's
// *LimitedBody under.
const BodyLimitKey = "golaravel.body_limit"

// MaxBodySize sets the largest request body the routes it is attached to
// accept, replacing the app-wide Engine.MaxBodyBytes for them. Use it to
// allow large uploads on one route only:
//
//	r.POST("/videos", middleware.MaxBodySize(2<<30),
//		middleware.BodyReadTimeout(30*time.Minute), upload)
//
// Bigger bodies fail to read with *http.MaxBytesError (c.File returns nil
// and form parsing errors). n <= 0 removes the limit. Outside the engine
// (plain net/http) it can only lower the limit.
func MaxBodySize(n int64) context.HandlerFunc {
	return func(c *context.Context) error {
		if v, ok := c.Get(BodyLimitKey); ok {
			if lb, ok := v.(*LimitedBody); ok {
				lb.SetLimit(n)
				return c.Next()
			}
		}
		if n > 0 && c.Request.Body != nil && c.Request.Body != http.NoBody {
			c.Request.Body = http.MaxBytesReader(&c.Response, c.Request.Body, n)
		}
		return c.Next()
	}
}
