package glhttp

import (
	"context"
	"net/http"
	"time"
)

// Client represents an HTTP client factory with mocking and fluent request building capabilities.
type Client struct {
	httpClient *http.Client
	faker      *Faker
}

// Global default client instance
var globalClient = NewClient()

// NewClient creates a new Client instance.
func NewClient(httpClient ...*http.Client) *Client {
	c := &http.Client{
		Timeout: 30 * time.Second,
	}
	if len(httpClient) > 0 && httpClient[0] != nil {
		c = httpClient[0]
	}
	return &Client{
		httpClient: c,
		faker:      NewFaker(),
	}
}

// DefaultClient returns the global default Client.
func DefaultClient() *Client {
	return globalClient
}

// Request initializes a new PendingRequest.
func (c *Client) Request() *PendingRequest {
	return NewPendingRequest(c.httpClient, c.faker)
}

// WithToken adds a Bearer Authorization token to the request.
func (c *Client) WithToken(token string, prefix ...string) *PendingRequest {
	return c.Request().WithToken(token, prefix...)
}

// WithBasicAuth sets standard Basic HTTP authentication credentials.
func (c *Client) WithBasicAuth(username, password string) *PendingRequest {
	return c.Request().WithBasicAuth(username, password)
}

// WithHeader sets an HTTP header key-value pair.
func (c *Client) WithHeader(key, value string) *PendingRequest {
	return c.Request().WithHeader(key, value)
}

// WithHeaders sets multiple HTTP headers.
func (c *Client) WithHeaders(headers map[string]string) *PendingRequest {
	return c.Request().WithHeaders(headers)
}

// WithCookie attaches a cookie to the request.
func (c *Client) WithCookie(name, value string) *PendingRequest {
	return c.Request().WithCookie(name, value)
}

// WithCookies attaches multiple cookies to the request.
func (c *Client) WithCookies(cookies []*http.Cookie) *PendingRequest {
	return c.Request().WithCookies(cookies)
}

// WithQuery adds multiple URL query parameters.
func (c *Client) WithQuery(params map[string]string) *PendingRequest {
	return c.Request().WithQuery(params)
}

// WithQueryParam adds a single URL query parameter.
func (c *Client) WithQueryParam(key, value string) *PendingRequest {
	return c.Request().WithQueryParam(key, value)
}

// WithContext assigns a context to the request.
func (c *Client) WithContext(ctx context.Context) *PendingRequest {
	return c.Request().WithContext(ctx)
}

// Timeout overrides the request timeout duration.
func (c *Client) Timeout(d time.Duration) *PendingRequest {
	return c.Request().Timeout(d)
}

// BaseURL sets the base URL prefix for subsequent relative paths.
func (c *Client) BaseURL(baseURL string) *PendingRequest {
	return c.Request().BaseURL(baseURL)
}

// Accept sets the Accept header.
func (c *Client) Accept(mimeType string) *PendingRequest {
	return c.Request().Accept(mimeType)
}

// AcceptJson sets Accept: application/json.
func (c *Client) AcceptJson() *PendingRequest {
	return c.Request().AcceptJson()
}

// ContentType sets the Content-Type header.
func (c *Client) ContentType(mimeType string) *PendingRequest {
	return c.Request().ContentType(mimeType)
}

// AsJson sets Content-Type & Accept to application/json.
func (c *Client) AsJson() *PendingRequest {
	return c.Request().AsJson()
}

// AsForm sets Content-Type to application/x-www-form-urlencoded.
func (c *Client) AsForm() *PendingRequest {
	return c.Request().AsForm()
}

// AsMultipart sets Content-Type to multipart/form-data.
func (c *Client) AsMultipart() *PendingRequest {
	return c.Request().AsMultipart()
}

// Attach adds a file upload attachment.
func (c *Client) Attach(fieldName string, content []byte, filename string, contentType ...string) *PendingRequest {
	return c.Request().Attach(fieldName, content, filename, contentType...)
}

// Retry configures retry attempts with backoff on failure.
func (c *Client) Retry(times int, sleep time.Duration, when ...RetryCondition) *PendingRequest {
	return c.Request().Retry(times, sleep, when...)
}

// Get performs a GET request.
func (c *Client) Get(url string, query ...map[string]string) (*Response, error) {
	return c.Request().Get(url, query...)
}

// Post performs a POST request.
func (c *Client) Post(url string, body ...any) (*Response, error) {
	return c.Request().Post(url, body...)
}

// Put performs a PUT request.
func (c *Client) Put(url string, body ...any) (*Response, error) {
	return c.Request().Put(url, body...)
}

// Patch performs a PATCH request.
func (c *Client) Patch(url string, body ...any) (*Response, error) {
	return c.Request().Patch(url, body...)
}

// Delete performs a DELETE request.
func (c *Client) Delete(url string, body ...any) (*Response, error) {
	return c.Request().Delete(url, body...)
}

// Head performs a HEAD request.
func (c *Client) Head(url string, query ...map[string]string) (*Response, error) {
	return c.Request().Head(url, query...)
}

// Send executes the request with the given method and URL.
func (c *Client) Send(method, url string, body ...any) (*Response, error) {
	return c.Request().Send(method, url, body...)
}

// --- Mocking & Fakes ---

// Fake activates fake mocking for this client with the provided stub configuration.
func (c *Client) Fake(stubs ...any) {
	c.faker.Enable(stubs...)
}

// AssertSent verifies that a matching HTTP request was sent.
func (c *Client) AssertSent(callback func(req *Request) bool) error {
	return c.faker.AssertSent(callback)
}

// AssertSentCount verifies the total number of outbound HTTP requests recorded.
func (c *Client) AssertSentCount(expectedCount int) error {
	return c.faker.AssertSentCount(expectedCount)
}

// AssertNotSent verifies that NO request matching the callback was sent.
func (c *Client) AssertNotSent(callback func(req *Request) bool) error {
	return c.faker.AssertNotSent(callback)
}

// AssertNothingSent verifies that zero requests were sent.
func (c *Client) AssertNothingSent() error {
	return c.faker.AssertNothingSent()
}

// Recorded returns all recorded requests and responses.
func (c *Client) Recorded() []RecordedExchange {
	return c.faker.Recorded()
}

// Reset clears all stubs, recordings, and disables mocking.
func (c *Client) Reset() {
	c.faker.Reset()
}

// --- Top-Level Package Functions operating on globalClient ---

func WithToken(token string, prefix ...string) *PendingRequest {
	return globalClient.WithToken(token, prefix...)
}

func WithBasicAuth(username, password string) *PendingRequest {
	return globalClient.WithBasicAuth(username, password)
}

func WithHeader(key, value string) *PendingRequest {
	return globalClient.WithHeader(key, value)
}

func WithHeaders(headers map[string]string) *PendingRequest {
	return globalClient.WithHeaders(headers)
}

func WithCookie(name, value string) *PendingRequest {
	return globalClient.WithCookie(name, value)
}

func WithCookies(cookies []*http.Cookie) *PendingRequest {
	return globalClient.WithCookies(cookies)
}

func WithQuery(params map[string]string) *PendingRequest {
	return globalClient.WithQuery(params)
}

func WithQueryParam(key, value string) *PendingRequest {
	return globalClient.WithQueryParam(key, value)
}

func WithContext(ctx context.Context) *PendingRequest {
	return globalClient.WithContext(ctx)
}

func Timeout(d time.Duration) *PendingRequest {
	return globalClient.Timeout(d)
}

func BaseURL(baseURL string) *PendingRequest {
	return globalClient.BaseURL(baseURL)
}

func Accept(mimeType string) *PendingRequest {
	return globalClient.Accept(mimeType)
}

func AcceptJson() *PendingRequest {
	return globalClient.AcceptJson()
}

func ContentType(mimeType string) *PendingRequest {
	return globalClient.ContentType(mimeType)
}

func AsJson() *PendingRequest {
	return globalClient.AsJson()
}

func AsForm() *PendingRequest {
	return globalClient.AsForm()
}

func AsMultipart() *PendingRequest {
	return globalClient.AsMultipart()
}

func Attach(fieldName string, content []byte, filename string, contentType ...string) *PendingRequest {
	return globalClient.Attach(fieldName, content, filename, contentType...)
}

func Retry(times int, sleep time.Duration, when ...RetryCondition) *PendingRequest {
	return globalClient.Retry(times, sleep, when...)
}

func Get(url string, query ...map[string]string) (*Response, error) {
	return globalClient.Get(url, query...)
}

func Post(url string, body ...any) (*Response, error) {
	return globalClient.Post(url, body...)
}

func Put(url string, body ...any) (*Response, error) {
	return globalClient.Put(url, body...)
}

func Patch(url string, body ...any) (*Response, error) {
	return globalClient.Patch(url, body...)
}

func Delete(url string, body ...any) (*Response, error) {
	return globalClient.Delete(url, body...)
}

func Head(url string, query ...map[string]string) (*Response, error) {
	return globalClient.Head(url, query...)
}

func Send(method, url string, body ...any) (*Response, error) {
	return globalClient.Send(method, url, body...)
}

func Fake(stubs ...any) {
	globalClient.Fake(stubs...)
}

// MockResponse returns a mocked *Response for stubbing.
func MockResponse(body any, statusAndHeaders ...any) *Response {
	return ResponseFactory(body, statusAndHeaders...)
}

func AssertSent(callback func(req *Request) bool) error {
	return globalClient.AssertSent(callback)
}

func AssertSentCount(expectedCount int) error {
	return globalClient.AssertSentCount(expectedCount)
}

func AssertNotSent(callback func(req *Request) bool) error {
	return globalClient.AssertNotSent(callback)
}

func AssertNothingSent() error {
	return globalClient.AssertNothingSent()
}

func Recorded() []RecordedExchange {
	return globalClient.Recorded()
}

func Reset() {
	globalClient.Reset()
}
