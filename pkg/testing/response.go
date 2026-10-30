package gltest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-json"
)

// TestResponse wraps an HTTP test response recorder with rich, fluent assertion methods.
type TestResponse struct {
	t        testing.TB
	recorder *httptest.ResponseRecorder
	request  *http.Request
	parsed   any // cached parsed JSON body
}

// NewTestResponse creates a TestResponse wrapper.
func NewTestResponse(t testing.TB, req *http.Request, rec *httptest.ResponseRecorder) *TestResponse {
	return &TestResponse{
		t:        t,
		recorder: rec,
		request:  req,
	}
}

// Recorder returns the underlying ResponseRecorder.
func (r *TestResponse) Recorder() *httptest.ResponseRecorder {
	return r.recorder
}

// StatusCode returns the HTTP response status code.
func (r *TestResponse) StatusCode() int {
	return r.recorder.Code
}

// Body returns the raw response body bytes.
func (r *TestResponse) Body() []byte {
	return r.recorder.Body.Bytes()
}

// BodyString returns the response body as a string.
func (r *TestResponse) BodyString() string {
	return r.recorder.Body.String()
}

// Header returns the response headers.
func (r *TestResponse) Header() http.Header {
	return r.recorder.Header()
}

// Response returns the parsed http.Response.
func (r *TestResponse) Response() *http.Response {
	return r.recorder.Result()
}

// --- Status Assertions ---

// AssertStatus asserts that the response has the given status code.
func (r *TestResponse) AssertStatus(expected int) *TestResponse {
	r.t.Helper()
	if r.recorder.Code != expected {
		r.t.Fatalf("expected HTTP status %d, got %d. Response: %s", expected, r.recorder.Code, r.BodyString())
	}
	return r
}

// AssertOK asserts that the response has status 200 OK.
func (r *TestResponse) AssertOK() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusOK)
}

// AssertCreated asserts that the response has status 201 Created.
func (r *TestResponse) AssertCreated() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusCreated)
}

// AssertAccepted asserts that the response has status 202 Accepted.
func (r *TestResponse) AssertAccepted() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusAccepted)
}

// AssertNoContent asserts that the response has status 204 No Content.
func (r *TestResponse) AssertNoContent() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusNoContent)
}

// AssertMovedPermanently asserts that the response has status 301 Moved Permanently.
func (r *TestResponse) AssertMovedPermanently() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusMovedPermanently)
}

// AssertFound asserts that the response has status 302 Found.
func (r *TestResponse) AssertFound() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusFound)
}

// AssertRedirect asserts that the response is a redirect (3xx) and optionally checks Location.
func (r *TestResponse) AssertRedirect(targetURI ...string) *TestResponse {
	r.t.Helper()
	code := r.recorder.Code
	if code < 300 || code > 308 {
		r.t.Fatalf("expected redirect status (3xx), got %d. Response: %s", code, r.BodyString())
	}
	if len(targetURI) > 0 && targetURI[0] != "" {
		loc := r.recorder.Header().Get("Location")
		if loc != targetURI[0] {
			r.t.Fatalf("expected redirect location '%s', got '%s'", targetURI[0], loc)
		}
	}
	return r
}

// AssertBadRequest asserts that the response has status 400 Bad Request.
func (r *TestResponse) AssertBadRequest() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusBadRequest)
}

// AssertUnauthorized asserts that the response has status 401 Unauthorized.
func (r *TestResponse) AssertUnauthorized() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusUnauthorized)
}

// AssertForbidden asserts that the response has status 403 Forbidden.
func (r *TestResponse) AssertForbidden() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusForbidden)
}

// AssertNotFound asserts that the response has status 404 Not Found.
func (r *TestResponse) AssertNotFound() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusNotFound)
}

// AssertMethodNotAllowed asserts that the response has status 405 Method Not Allowed.
func (r *TestResponse) AssertMethodNotAllowed() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusMethodNotAllowed)
}

// AssertUnprocessable asserts that the response has status 422 Unprocessable Entity.
func (r *TestResponse) AssertUnprocessable() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusUnprocessableEntity)
}

// AssertServerError asserts that the response has status 500 Internal Server Error.
func (r *TestResponse) AssertServerError() *TestResponse {
	r.t.Helper()
	return r.AssertStatus(http.StatusInternalServerError)
}

// AssertSuccessful asserts that the response has a 2xx status code.
func (r *TestResponse) AssertSuccessful() *TestResponse {
	r.t.Helper()
	if r.recorder.Code < 200 || r.recorder.Code >= 300 {
		r.t.Fatalf("expected 2xx successful status, got %d. Response: %s", r.recorder.Code, r.BodyString())
	}
	return r
}

// AssertClientError asserts that the response has a 4xx status code.
func (r *TestResponse) AssertClientError() *TestResponse {
	r.t.Helper()
	if r.recorder.Code < 400 || r.recorder.Code >= 500 {
		r.t.Fatalf("expected 4xx client error status, got %d. Response: %s", r.recorder.Code, r.BodyString())
	}
	return r
}

// --- Header & Cookie Assertions ---

// AssertHeader asserts that the response header matches the expected value.
func (r *TestResponse) AssertHeader(headerName, expectedValue string) *TestResponse {
	r.t.Helper()
	got := r.recorder.Header().Get(headerName)
	if got != expectedValue {
		r.t.Fatalf("expected header '%s' to be '%s', got '%s'", headerName, expectedValue, got)
	}
	return r
}

// AssertHeaderMissing asserts that the response does not contain the given header.
func (r *TestResponse) AssertHeaderMissing(headerName string) *TestResponse {
	r.t.Helper()
	if _, exists := r.recorder.Header()[headerName]; exists {
		r.t.Fatalf("expected header '%s' to be missing, but it was present", headerName)
	}
	return r
}

// AssertContentType asserts that the Content-Type header starts with or contains the expected string.
func (r *TestResponse) AssertContentType(expected string) *TestResponse {
	r.t.Helper()
	ct := r.recorder.Header().Get("Content-Type")
	if !strings.Contains(ct, expected) {
		r.t.Fatalf("expected Content-Type containing '%s', got '%s'", expected, ct)
	}
	return r
}

// Cookie retrieves a cookie by name from the response Set-Cookie headers.
func (r *TestResponse) Cookie(name string) *http.Cookie {
	cookies := r.recorder.Result().Cookies()
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Cookies returns all cookies from the response Set-Cookie headers.
func (r *TestResponse) Cookies() []*http.Cookie {
	return r.recorder.Result().Cookies()
}

// AssertCookie asserts that the response sets the given cookie with the expected value.
func (r *TestResponse) AssertCookie(cookieName, expectedValue string) *TestResponse {
	r.t.Helper()
	c := r.Cookie(cookieName)
	if c == nil {
		r.t.Fatalf("expected cookie '%s' to be set in response, but it was not found", cookieName)
	}
	if c.Value != expectedValue {
		r.t.Fatalf("expected cookie '%s' value to be '%s', got '%s'", cookieName, expectedValue, c.Value)
	}
	return r
}

// AssertCookieMissing asserts that the response does not set the given cookie.
func (r *TestResponse) AssertCookieMissing(cookieName string) *TestResponse {
	r.t.Helper()
	if c := r.Cookie(cookieName); c != nil {
		r.t.Fatalf("expected cookie '%s' to be missing, but found value '%s'", cookieName, c.Value)
	}
	return r
}

// --- Content & Text Assertions ---

// AssertSee asserts that the response body contains the given substring.
func (r *TestResponse) AssertSee(needle string) *TestResponse {
	r.t.Helper()
	if !strings.Contains(r.BodyString(), needle) {
		r.t.Fatalf("expected response body to contain '%s', got: %s", needle, r.BodyString())
	}
	return r
}

// AssertSeeExact asserts that the response body matches the given string exactly.
func (r *TestResponse) AssertSeeExact(expected string) *TestResponse {
	r.t.Helper()
	if r.BodyString() != expected {
		r.t.Fatalf("expected exact body '%s', got '%s'", expected, r.BodyString())
	}
	return r
}

// AssertDontSee asserts that the response body does not contain the given substring.
func (r *TestResponse) AssertDontSee(needle string) *TestResponse {
	r.t.Helper()
	if strings.Contains(r.BodyString(), needle) {
		r.t.Fatalf("expected response body NOT to contain '%s', but it was found", needle)
	}
	return r
}

// AssertSeeInOrder asserts that the given strings appear in the response body in chronological order.
func (r *TestResponse) AssertSeeInOrder(needles ...string) *TestResponse {
	r.t.Helper()
	body := r.BodyString()
	lastPos := 0
	for _, needle := range needles {
		pos := strings.Index(body[lastPos:], needle)
		if pos == -1 {
			r.t.Fatalf("expected to see '%s' after position %d in body", needle, lastPos)
		}
		lastPos += pos + len(needle)
	}
	return r
}

// --- JSON Assertions ---

func (r *TestResponse) parseJSON() any {
	if r.parsed != nil {
		return r.parsed
	}
	var data any
	if err := json.Unmarshal(r.Body(), &data); err != nil {
		r.t.Fatalf("failed to parse response as JSON: %v. Body was: %s", err, r.BodyString())
	}
	r.parsed = data
	return data
}

// AssertJson asserts that the response JSON contains the given expected subset.
func (r *TestResponse) AssertJson(expected any) *TestResponse {
	r.t.Helper()
	actual := r.parseJSON()
	if !isSubset(actual, expected) {
		expJSON, _ := json.Marshal(expected)
		r.t.Fatalf("expected JSON to contain subset %s, but actual was %s", string(expJSON), r.BodyString())
	}
	return r
}

// AssertExactJson asserts that the response JSON matches the expected structure exactly.
func (r *TestResponse) AssertExactJson(expected any) *TestResponse {
	r.t.Helper()
	actual := normalizeJSONValue(r.parseJSON())
	exp := normalizeJSONValue(expected)
	if !reflect.DeepEqual(actual, exp) {
		expJSON, _ := json.Marshal(exp)
		actJSON, _ := json.Marshal(actual)
		r.t.Fatalf("expected exact JSON %s, got %s", string(expJSON), string(actJSON))
	}
	return r
}

// AssertJsonPath asserts that the JSON path resolves to the expected value.
func (r *TestResponse) AssertJsonPath(path string, expected any) *TestResponse {
	r.t.Helper()
	actualData := r.parseJSON()
	val, ok := getJSONPath(actualData, path)
	if !ok {
		r.t.Fatalf("expected JSON path '%s' not found in response: %s", path, r.BodyString())
	}
	if !valuesEqual(val, expected) {
		r.t.Fatalf("expected JSON path '%s' to be '%v', got '%v'", path, expected, val)
	}
	return r
}

// AssertJsonMissingPath asserts that the JSON path does not exist in the response.
func (r *TestResponse) AssertJsonMissingPath(path string) *TestResponse {
	r.t.Helper()
	actualData := r.parseJSON()
	if _, ok := getJSONPath(actualData, path); ok {
		r.t.Fatalf("expected JSON path '%s' to be missing, but it exists in response", path)
	}
	return r
}

// AssertJsonCount asserts that the JSON array at the given path (or root) has the expected length.
func (r *TestResponse) AssertJsonCount(expectedCount int, path ...string) *TestResponse {
	r.t.Helper()
	targetPath := ""
	if len(path) > 0 {
		targetPath = path[0]
	}

	actualData := r.parseJSON()
	val, ok := getJSONPath(actualData, targetPath)
	if !ok {
		r.t.Fatalf("JSON path '%s' not found for count assertion", targetPath)
	}

	arr, ok := val.([]any)
	if !ok {
		r.t.Fatalf("expected JSON value at path '%s' to be an array, got %T", targetPath, val)
	}

	if len(arr) != expectedCount {
		r.t.Fatalf("expected JSON array at '%s' to have %d items, got %d", targetPath, expectedCount, len(arr))
	}

	return r
}

// AssertJsonValidationErrors asserts that the response is a 422 Unprocessable Entity
// containing error keys for the specified fields.
func (r *TestResponse) AssertJsonValidationErrors(fields ...string) *TestResponse {
	r.t.Helper()
	r.AssertUnprocessable()

	actualData := r.parseJSON()
	// GoLaravel validation error bags format as: {"message": "...", "errors": {"email": ["..."]}}
	errorsVal, ok := getJSONPath(actualData, "errors")
	if !ok {
		// Try root object
		errorsVal = actualData
	}

	errMap, ok := errorsVal.(map[string]any)
	if !ok {
		r.t.Fatalf("expected validation errors dictionary in response: %s", r.BodyString())
	}

	for _, field := range fields {
		if _, exists := errMap[field]; !exists {
			r.t.Fatalf("expected validation error for field '%s', but none was found in %s", field, r.BodyString())
		}
	}

	return r
}

// DecodeJson deserializes the JSON response body into the provided destination pointer.
func (r *TestResponse) DecodeJson(dest any) error {
	return json.Unmarshal(r.Body(), dest)
}

// --- Debugging ---

// Dump prints the HTTP status, response headers, and response body to the test log.
func (r *TestResponse) Dump() *TestResponse {
	r.t.Helper()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n--- Response Dump [%d] ---\n", r.recorder.Code))
	for k, v := range r.recorder.Header() {
		b.WriteString(fmt.Sprintf("%s: %s\n", k, strings.Join(v, ", ")))
	}
	b.WriteString("\n")
	b.WriteString(r.BodyString())
	b.WriteString("\n--------------------------\n")
	r.t.Log(b.String())
	return r
}

// DumpBody prints only the response body to the test log.
func (r *TestResponse) DumpBody() *TestResponse {
	r.t.Helper()
	r.t.Logf("\n--- Response Body ---\n%s\n--------------------\n", r.BodyString())
	return r
}
