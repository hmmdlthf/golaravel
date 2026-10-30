package glhttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/goccy/go-json"
)

type fileAttachment struct {
	fieldName   string
	filename    string
	content     []byte
	contentType string
}

// RetryCondition defines a predicate determining whether a failed request should be retried.
type RetryCondition func(resp *Response, err error) bool

// PendingRequest represents a chainable, fluent outbound HTTP request builder.
type PendingRequest struct {
	httpClient *http.Client
	faker      *Faker
	baseURL    string
	headers    http.Header
	cookies    []*http.Cookie
	query      url.Values
	timeout    time.Duration
	retries    int
	retrySleep time.Duration
	retryWhen  RetryCondition
	body       io.Reader
	bodyBytes  []byte
	form       url.Values
	files      []fileAttachment
	ctx        context.Context
}

// NewPendingRequest initializes a new PendingRequest.
func NewPendingRequest(client *http.Client, faker *Faker) *PendingRequest {
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}
	return &PendingRequest{
		httpClient: client,
		faker:      faker,
		headers:    make(http.Header),
		query:      make(url.Values),
		ctx:        context.Background(),
	}
}

// WithToken adds a Bearer (or custom prefix) Authorization header.
func (r *PendingRequest) WithToken(token string, prefix ...string) *PendingRequest {
	p := "Bearer"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}
	return r.WithHeader("Authorization", fmt.Sprintf("%s %s", p, token))
}

// WithBasicAuth sets standard Basic HTTP authentication credentials.
func (r *PendingRequest) WithBasicAuth(username, password string) *PendingRequest {
	auth := username + ":" + password
	encoded := base64.StdEncoding.EncodeToString([]byte(auth))
	return r.WithHeader("Authorization", "Basic "+encoded)
}

// WithHeader sets an HTTP header key-value pair.
func (r *PendingRequest) WithHeader(key, value string) *PendingRequest {
	r.headers.Set(key, value)
	return r
}

// WithHeaders sets multiple HTTP headers.
func (r *PendingRequest) WithHeaders(headers map[string]string) *PendingRequest {
	for k, v := range headers {
		r.headers.Set(k, v)
	}
	return r
}

// WithCookie attaches a cookie to the request.
func (r *PendingRequest) WithCookie(name, value string) *PendingRequest {
	r.cookies = append(r.cookies, &http.Cookie{
		Name:  name,
		Value: value,
	})
	return r
}

// WithCookies attaches multiple cookies to the request.
func (r *PendingRequest) WithCookies(cookies []*http.Cookie) *PendingRequest {
	r.cookies = append(r.cookies, cookies...)
	return r
}

// WithQuery adds multiple URL query parameters.
func (r *PendingRequest) WithQuery(params map[string]string) *PendingRequest {
	for k, v := range params {
		r.query.Set(k, v)
	}
	return r
}

// WithQueryParam adds a single URL query parameter.
func (r *PendingRequest) WithQueryParam(key, value string) *PendingRequest {
	r.query.Set(key, value)
	return r
}

// WithContext assigns a context to the request.
func (r *PendingRequest) WithContext(ctx context.Context) *PendingRequest {
	r.ctx = ctx
	return r
}

// Timeout overrides the request timeout duration.
func (r *PendingRequest) Timeout(d time.Duration) *PendingRequest {
	r.timeout = d
	return r
}

// BaseURL sets the base URL prefix for subsequent relative paths.
func (r *PendingRequest) BaseURL(baseURL string) *PendingRequest {
	r.baseURL = strings.TrimRight(baseURL, "/")
	return r
}

// Accept sets the Accept header.
func (r *PendingRequest) Accept(mimeType string) *PendingRequest {
	return r.WithHeader("Accept", mimeType)
}

// AcceptJson sets Accept: application/json.
func (r *PendingRequest) AcceptJson() *PendingRequest {
	return r.Accept("application/json")
}

// ContentType sets the Content-Type header.
func (r *PendingRequest) ContentType(mimeType string) *PendingRequest {
	return r.WithHeader("Content-Type", mimeType)
}

// AsJson sets Content-Type & Accept to application/json.
func (r *PendingRequest) AsJson() *PendingRequest {
	r.ContentType("application/json")
	return r.AcceptJson()
}

// AsForm sets Content-Type to application/x-www-form-urlencoded.
func (r *PendingRequest) AsForm() *PendingRequest {
	return r.ContentType("application/x-www-form-urlencoded")
}

// AsMultipart sets Content-Type to multipart/form-data.
func (r *PendingRequest) AsMultipart() *PendingRequest {
	return r
}

// Attach adds a file upload attachment.
func (r *PendingRequest) Attach(fieldName string, content []byte, filename string, contentType ...string) *PendingRequest {
	ct := "application/octet-stream"
	if len(contentType) > 0 && contentType[0] != "" {
		ct = contentType[0]
	}
	r.files = append(r.files, fileAttachment{
		fieldName:   fieldName,
		filename:    filename,
		content:     content,
		contentType: ct,
	})
	return r
}

// Retry configures retry attempts with backoff on failure.
func (r *PendingRequest) Retry(times int, sleep time.Duration, when ...RetryCondition) *PendingRequest {
	r.retries = times
	r.retrySleep = sleep
	if len(when) > 0 {
		r.retryWhen = when[0]
	} else {
		// Default retry condition: network error or 5xx server error
		r.retryWhen = func(resp *Response, err error) bool {
			return err != nil || (resp != nil && resp.ServerError())
		}
	}
	return r
}

// --- HTTP Verbs ---

// Get performs a GET request.
func (r *PendingRequest) Get(targetURL string, query ...map[string]string) (*Response, error) {
	if len(query) > 0 {
		r.WithQuery(query[0])
	}
	return r.Send(http.MethodGet, targetURL)
}

// Post performs a POST request.
func (r *PendingRequest) Post(targetURL string, body ...any) (*Response, error) {
	return r.Send(http.MethodPost, targetURL, body...)
}

// Put performs a PUT request.
func (r *PendingRequest) Put(targetURL string, body ...any) (*Response, error) {
	return r.Send(http.MethodPut, targetURL, body...)
}

// Patch performs a PATCH request.
func (r *PendingRequest) Patch(targetURL string, body ...any) (*Response, error) {
	return r.Send(http.MethodPatch, targetURL, body...)
}

// Delete performs a DELETE request.
func (r *PendingRequest) Delete(targetURL string, body ...any) (*Response, error) {
	return r.Send(http.MethodDelete, targetURL, body...)
}

// Head performs a HEAD request.
func (r *PendingRequest) Head(targetURL string, query ...map[string]string) (*Response, error) {
	if len(query) > 0 {
		r.WithQuery(query[0])
	}
	return r.Send(http.MethodHead, targetURL)
}

// Send executes the HTTP request with configured retry and fake handling.
func (r *PendingRequest) Send(method, targetURL string, body ...any) (*Response, error) {
	fullURL := targetURL
	if r.baseURL != "" && !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		cleanRel := strings.TrimLeft(targetURL, "/")
		fullURL = fmt.Sprintf("%s/%s", r.baseURL, cleanRel)
	}

	// Prepare payload body
	if len(body) > 0 && body[0] != nil {
		r.applyBody(body[0])
	}

	// Append query parameters
	if len(r.query) > 0 {
		parsedURL, err := url.Parse(fullURL)
		if err != nil {
			return nil, fmt.Errorf("http: invalid URL '%s': %w", fullURL, err)
		}
		q := parsedURL.Query()
		for k, vals := range r.query {
			for _, v := range vals {
				q.Add(k, v)
			}
		}
		parsedURL.RawQuery = q.Encode()
		fullURL = parsedURL.String()
	}

	// Build request payload reader
	var reqBody io.Reader
	var reqBodyBytes []byte

	if len(r.files) > 0 {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		if r.form != nil {
			for k, values := range r.form {
				for _, v := range values {
					_ = writer.WriteField(k, v)
				}
			}
		}

		for _, f := range r.files {
			part, err := writer.CreateFormFile(f.fieldName, f.filename)
			if err != nil {
				return nil, fmt.Errorf("http: failed to create multipart file part: %w", err)
			}
			if _, err := part.Write(f.content); err != nil {
				return nil, fmt.Errorf("http: failed to write multipart content: %w", err)
			}
		}
		_ = writer.Close()

		reqBodyBytes = buf.Bytes()
		reqBody = bytes.NewReader(reqBodyBytes)
		r.headers.Set("Content-Type", writer.FormDataContentType())

	} else if r.form != nil {
		formData := r.form.Encode()
		reqBodyBytes = []byte(formData)
		reqBody = strings.NewReader(formData)
		r.headers.Set("Content-Type", "application/x-www-form-urlencoded")

	} else if r.bodyBytes != nil {
		reqBodyBytes = r.bodyBytes
		reqBody = bytes.NewReader(reqBodyBytes)
	}

	// Create request wrapper for inspection / fakes
	stdReq, err := http.NewRequestWithContext(r.ctx, strings.ToUpper(method), fullURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("http: failed to create request: %w", err)
	}

	for k, vals := range r.headers {
		for _, v := range vals {
			stdReq.Header.Add(k, v)
		}
	}
	for _, cookie := range r.cookies {
		stdReq.AddCookie(cookie)
	}

	wrappedReq := NewRequest(stdReq, reqBodyBytes)

	// Check if Mocking Faker is active
	if r.faker != nil {
		if mockResp, mockErr, ok := r.faker.Intercept(wrappedReq); ok {
			if mockResp != nil {
				mockResp.request = wrappedReq
			}
			r.faker.Record(wrappedReq, mockResp, mockErr)
			return mockResp, mockErr
		}
	}

	// Execution with retries
	attempts := 1 + r.retries
	var lastResp *Response
	var lastErr error

	for i := 0; i < attempts; i++ {
		if i > 0 && r.retrySleep > 0 {
			time.Sleep(r.retrySleep)
		}

		// Rewind body reader for retry attempts
		if reqBodyBytes != nil {
			stdReq.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
		}

		client := r.httpClient
		if r.timeout > 0 && r.timeout != client.Timeout {
			customClient := *client
			customClient.Timeout = r.timeout
			client = &customClient
		}

		start := time.Now()
		httpResp, execErr := client.Do(stdReq)
		duration := time.Since(start)

		if execErr != nil {
			lastErr = execErr
			lastResp = nil
			if r.retryWhen != nil && r.retryWhen(nil, execErr) {
				continue
			}
			break
		}

		respBody, readErr := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()

		if readErr != nil {
			lastErr = readErr
			lastResp = nil
			break
		}

		lastResp = NewResponse(httpResp, respBody, duration, wrappedReq)
		lastErr = nil

		if r.retryWhen != nil && r.retryWhen(lastResp, nil) {
			continue
		}

		break
	}

	if r.faker != nil {
		r.faker.Record(wrappedReq, lastResp, lastErr)
	}

	return lastResp, lastErr
}

func (r *PendingRequest) applyBody(body any) {
	switch v := body.(type) {
	case string:
		r.bodyBytes = []byte(v)
		if r.headers.Get("Content-Type") == "" {
			r.headers.Set("Content-Type", "text/plain; charset=utf-8")
		}
	case []byte:
		r.bodyBytes = v
		if r.headers.Get("Content-Type") == "" {
			r.headers.Set("Content-Type", "application/octet-stream")
		}
	case url.Values:
		r.form = v
	case map[string]string:
		if r.headers.Get("Content-Type") == "application/x-www-form-urlencoded" {
			r.form = make(url.Values)
			for fk, fv := range v {
				r.form.Set(fk, fv)
			}
		} else {
			r.serializeJSON(v)
		}
	default:
		r.serializeJSON(v)
	}
}

func (r *PendingRequest) serializeJSON(v any) {
	b, err := json.Marshal(v)
	if err == nil {
		r.bodyBytes = b
		r.headers.Set("Content-Type", "application/json; charset=utf-8")
		if r.headers.Get("Accept") == "" {
			r.headers.Set("Accept", "application/json")
		}
	}
}
