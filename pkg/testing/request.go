package gltest

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/goccy/go-json"
)

type testFileUpload struct {
	fieldName   string
	filename    string
	content     []byte
	contentType string
}

// TestRequestBuilder provides a fluent builder for configuring and executing test requests.
type TestRequestBuilder struct {
	client  *TestClient
	method  string
	path    string
	headers http.Header
	cookies []*http.Cookie
	body    io.Reader
	form    url.Values
	files   []testFileUpload
	ctx     context.Context
}

// NewRequestBuilder initializes a TestRequestBuilder.
func NewRequestBuilder(client *TestClient, method, path string) *TestRequestBuilder {
	return &TestRequestBuilder{
		client:  client,
		method:  strings.ToUpper(method),
		path:    path,
		headers: make(http.Header),
		ctx:     context.Background(),
	}
}

// WithHeader adds a single header to the request.
func (b *TestRequestBuilder) WithHeader(key, value string) *TestRequestBuilder {
	b.headers.Set(key, value)
	return b
}

// WithHeaders sets multiple headers at once.
func (b *TestRequestBuilder) WithHeaders(headers map[string]string) *TestRequestBuilder {
	for k, v := range headers {
		b.headers.Set(k, v)
	}
	return b
}

// WithToken adds a Bearer Authorization header to the request.
func (b *TestRequestBuilder) WithToken(token string) *TestRequestBuilder {
	return b.WithHeader("Authorization", "Bearer "+token)
}

// WithBasicAuth adds Basic Authentication credentials to the request.
func (b *TestRequestBuilder) WithBasicAuth(username, password string) *TestRequestBuilder {
	auth := username + ":" + password
	encoded := base64.StdEncoding.EncodeToString([]byte(auth))
	return b.WithHeader("Authorization", "Basic "+encoded)
}

// WithCookie adds a cookie to the request.
func (b *TestRequestBuilder) WithCookie(name, value string) *TestRequestBuilder {
	b.cookies = append(b.cookies, &http.Cookie{
		Name:  name,
		Value: value,
	})
	return b
}

// WithCookies adds multiple cookies to the request.
func (b *TestRequestBuilder) WithCookies(cookies ...*http.Cookie) *TestRequestBuilder {
	b.cookies = append(b.cookies, cookies...)
	return b
}

// WithContext sets the standard context for the request.
func (b *TestRequestBuilder) WithContext(ctx context.Context) *TestRequestBuilder {
	b.ctx = ctx
	return b
}

// WithJSON serializes data to JSON and sets Content-Type & Accept headers.
func (b *TestRequestBuilder) WithJSON(data any) *TestRequestBuilder {
	bodyBytes, err := json.Marshal(data)
	if err != nil {
		b.client.t.Fatalf("failed to marshal JSON payload in test request: %v", err)
	}
	b.body = bytes.NewReader(bodyBytes)
	b.headers.Set("Content-Type", "application/json")
	if b.headers.Get("Accept") == "" {
		b.headers.Set("Accept", "application/json")
	}
	return b
}

// WithForm sets url-encoded form values.
func (b *TestRequestBuilder) WithForm(data map[string]string) *TestRequestBuilder {
	if b.form == nil {
		b.form = make(url.Values)
	}
	for k, v := range data {
		b.form.Set(k, v)
	}
	b.headers.Set("Content-Type", "application/x-www-form-urlencoded")
	return b
}

// WithFile attaches a file upload to the request, turning it into a multipart request.
func (b *TestRequestBuilder) WithFile(fieldName, filename string, content []byte, contentType ...string) *TestRequestBuilder {
	ct := "application/octet-stream"
	if len(contentType) > 0 && contentType[0] != "" {
		ct = contentType[0]
	}
	b.files = append(b.files, testFileUpload{
		fieldName:   fieldName,
		filename:    filename,
		content:     content,
		contentType: ct,
	})
	return b
}

// Send executes the HTTP request against the client's handler and returns the response wrapper.
func (b *TestRequestBuilder) Send() *TestResponse {
	b.client.t.Helper()

	var reqBody io.Reader
	if len(b.files) > 0 {
		// Construct multipart form data
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		// Form text fields
		if b.form != nil {
			for k, values := range b.form {
				for _, v := range values {
					_ = writer.WriteField(k, v)
				}
			}
		}

		// Attached files
		for _, f := range b.files {
			part, err := writer.CreatePart(FakeFile{Name: f.filename, ContentType: f.contentType}.partHeader(f.fieldName))
			if err != nil {
				b.client.t.Fatalf("failed to create multipart file part: %v", err)
			}
			if _, err := part.Write(f.content); err != nil {
				b.client.t.Fatalf("failed to write multipart content: %v", err)
			}
		}

		_ = writer.Close()
		reqBody = &buf
		b.headers.Set("Content-Type", writer.FormDataContentType())

	} else if b.form != nil {
		reqBody = strings.NewReader(b.form.Encode())
	} else if b.body != nil {
		reqBody = b.body
	}

	req := httptest.NewRequest(b.method, b.path, reqBody)
	if b.ctx != nil {
		req = req.WithContext(b.ctx)
	}

	// Apply headers
	for k, vals := range b.headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}

	// Apply cookies
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}

	// Execute through handler
	rec := httptest.NewRecorder()
	b.client.handler.ServeHTTP(rec, req)

	return NewTestResponse(b.client.t, req, rec)
}

// Get executes a GET request using this builder.
func (b *TestRequestBuilder) Get() *TestResponse {
	b.method = http.MethodGet
	return b.Send()
}

// Post executes a POST request using this builder.
func (b *TestRequestBuilder) Post() *TestResponse {
	b.method = http.MethodPost
	return b.Send()
}

// Put executes a PUT request using this builder.
func (b *TestRequestBuilder) Put() *TestResponse {
	b.method = http.MethodPut
	return b.Send()
}

// Patch executes a PATCH request using this builder.
func (b *TestRequestBuilder) Patch() *TestResponse {
	b.method = http.MethodPatch
	return b.Send()
}

// Delete executes a DELETE request using this builder.
func (b *TestRequestBuilder) Delete() *TestResponse {
	b.method = http.MethodDelete
	return b.Send()
}
