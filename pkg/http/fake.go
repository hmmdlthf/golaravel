package glhttp

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"
)

// ResponseSequence allows stubbing a sequence of responses in FIFO order.
type ResponseSequence struct {
	mu        sync.Mutex
	responses []*Response
	fallback  *Response
}

// Sequence creates a new empty ResponseSequence.
func Sequence() *ResponseSequence {
	return &ResponseSequence{}
}

// Push appends a response to the sequence.
func (s *ResponseSequence) Push(body any, statusAndHeaders ...any) *ResponseSequence {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses = append(s.responses, ResponseFactory(body, statusAndHeaders...))
	return s
}

// WhenEmpty sets a fallback response when all pushed responses have been exhausted.
func (s *ResponseSequence) WhenEmpty(resp *Response) *ResponseSequence {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fallback = resp
	return s
}

// Next pops the next response from the sequence.
func (s *ResponseSequence) Next() *Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.responses) > 0 {
		resp := s.responses[0]
		s.responses = s.responses[1:]
		return resp
	}
	if s.fallback != nil {
		return s.fallback
	}
	return ResponseFactory("", http.StatusOK)
}

type stubHandler struct {
	pattern  string
	callback func(req *Request) (*Response, error)
	sequence *ResponseSequence
	response *Response
}

// RecordedExchange stores a pair of outbound Request and received Response.
type RecordedExchange struct {
	Request  *Request
	Response *Response
	Err      error
}

// Faker intercepts and mocks outbound HTTP requests during testing.
type Faker struct {
	mu       sync.RWMutex
	enabled  bool
	handlers []stubHandler
	recorded []RecordedExchange
}

// NewFaker creates an unconfigured Faker instance.
func NewFaker() *Faker {
	return &Faker{}
}

// Enable activates fake mocking with the provided stub configuration.
func (f *Faker) Enable(stubs ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.enabled = true

	if len(stubs) == 0 {
		// Default stub: return 200 OK empty response for all URLs
		f.handlers = append(f.handlers, stubHandler{
			pattern:  "*",
			response: ResponseFactory("", http.StatusOK),
		})
		return
	}

	for i := 0; i < len(stubs); i++ {
		switch s := stubs[i].(type) {
		case string:
			pattern := s
			var targetResp *Response
			var seq *ResponseSequence
			var cb func(req *Request) (*Response, error)

			if i+1 < len(stubs) {
				i++
				switch next := stubs[i].(type) {
				case *Response:
					targetResp = next
				case *ResponseSequence:
					seq = next
				case func(req *Request) (*Response, error):
					cb = next
				default:
					targetResp = ResponseFactory(next, http.StatusOK)
				}
			} else {
				targetResp = ResponseFactory("", http.StatusOK)
			}

			f.handlers = append(f.handlers, stubHandler{
				pattern:  pattern,
				response: targetResp,
				sequence: seq,
				callback: cb,
			})

		case func(req *Request) (*Response, error):
			f.handlers = append(f.handlers, stubHandler{
				pattern:  "*",
				callback: s,
			})

		case map[string]any:
			for pat, respVal := range s {
				var targetResp *Response
				var seq *ResponseSequence
				var cb func(req *Request) (*Response, error)

				switch val := respVal.(type) {
				case *Response:
					targetResp = val
				case *ResponseSequence:
					seq = val
				case func(req *Request) (*Response, error):
					cb = val
				default:
					targetResp = ResponseFactory(val, http.StatusOK)
				}

				f.handlers = append(f.handlers, stubHandler{
					pattern:  pat,
					response: targetResp,
					sequence: seq,
					callback: cb,
				})
			}
		}
	}
}

// Reset clears all stubs, recordings, and disables the faker.
func (f *Faker) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled = false
	f.handlers = nil
	f.recorded = nil
}

// Record saves an exchange for later assertions.
func (f *Faker) Record(req *Request, resp *Response, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, RecordedExchange{
		Request:  req,
		Response: resp,
		Err:      err,
	})
}

// Intercept executes a stubbed response if a matching fake is found.
func (f *Faker) Intercept(req *Request) (*Response, error, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if !f.enabled {
		return nil, nil, false
	}

	urlStr := req.URL()
	for _, h := range f.handlers {
		if matchPattern(h.pattern, urlStr) {
			if h.callback != nil {
				resp, err := h.callback(req)
				return resp, err, true
			}
			if h.sequence != nil {
				return h.sequence.Next(), nil, true
			}
			if h.response != nil {
				return h.response, nil, true
			}
		}
	}

	// If faker is enabled but no handler matched, default to 200 OK empty response
	return ResponseFactory("", http.StatusOK), nil, true
}

func matchPattern(pattern, target string) bool {
	if pattern == "*" || pattern == "" || pattern == target {
		return true
	}

	// Simple wildcard glob matching
	matched, err := filepath.Match(pattern, target)
	if err == nil && matched {
		return true
	}

	// Substring wildcard matching (e.g. *stripe.com*)
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		sub := strings.Trim(pattern, "*")
		return strings.Contains(target, sub)
	}
	if strings.HasPrefix(pattern, "*") {
		sub := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(target, sub)
	}
	if strings.HasSuffix(pattern, "*") {
		sub := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(target, sub)
	}

	return false
}

// ResponseFactory creates a mocked *Response for testing and fake stubbing.
func ResponseFactory(body any, statusAndHeaders ...any) *Response {
	statusCode := http.StatusOK
	headers := make(http.Header)

	for _, opt := range statusAndHeaders {
		switch v := opt.(type) {
		case int:
			statusCode = v
		case map[string]string:
			for hk, hv := range v {
				headers.Set(hk, hv)
			}
		case http.Header:
			headers = v
		}
	}

	var bodyBytes []byte
	switch v := body.(type) {
	case string:
		bodyBytes = []byte(v)
		if headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "text/plain; charset=utf-8")
		}
	case []byte:
		bodyBytes = v
		if headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "application/octet-stream")
		}
	default:
		// Auto-serialize to JSON
		b, _ := json.Marshal(v)
		bodyBytes = b
		if headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "application/json; charset=utf-8")
		}
	}

	raw := &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Header:     headers,
		Body:       io.NopCloser(bytes.NewReader(bodyBytes)),
	}

	return NewResponse(raw, bodyBytes, 5*time.Millisecond, nil)
}

// Assertions

// AssertSent verifies that at least one request matching the callback was recorded.
func (f *Faker) AssertSent(callback func(req *Request) bool) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, ex := range f.recorded {
		if callback(ex.Request) {
			return nil
		}
	}
	return fmt.Errorf("expected matching HTTP request was not sent. Recorded %d requests", len(f.recorded))
}

// AssertSentCount verifies the total number of outbound HTTP requests recorded.
func (f *Faker) AssertSentCount(expectedCount int) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	actual := len(f.recorded)
	if actual != expectedCount {
		return fmt.Errorf("expected %d requests to be sent, but %d were recorded", expectedCount, actual)
	}
	return nil
}

// AssertNotSent verifies that NO request matching the callback was sent.
func (f *Faker) AssertNotSent(callback func(req *Request) bool) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, ex := range f.recorded {
		if callback(ex.Request) {
			return fmt.Errorf("unexpected HTTP request matching callback was sent to %s", ex.Request.URL())
		}
	}
	return nil
}

// AssertNothingSent verifies that zero requests were sent.
func (f *Faker) AssertNothingSent() error {
	return f.AssertSentCount(0)
}

// Recorded returns a copy of all recorded request/response exchanges.
func (f *Faker) Recorded() []RecordedExchange {
	f.mu.RLock()
	defer f.mu.RUnlock()
	res := make([]RecordedExchange, len(f.recorded))
	copy(res, f.recorded)
	return res
}
