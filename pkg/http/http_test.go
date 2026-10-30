package glhttp_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	glhttp "github.com/golaravel/golaravel/pkg/http"
)

func TestHttpClient_RealHttpServer(t *testing.T) {
	// Setup test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users":
			if r.Method == http.MethodGet {
				role := r.URL.Query().Get("role")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[{"id": 1, "name": "Alice", "role": "` + role + `"}]`))
				return
			}
			if r.Method == http.MethodPost {
				auth := r.Header.Get("Authorization")
				if auth != "Bearer secret_api_token" {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
				body, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write(body)
				return
			}

		case "/upload":
			if r.Method == http.MethodPost {
				file, header, err := r.FormFile("receipt")
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				defer file.Close()
				data, _ := io.ReadAll(file)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(header.Filename + ":" + string(data)))
				return
			}

		case "/error/404":
			http.Error(w, "User not found", http.StatusNotFound)
			return

		case "/error/500":
			http.Error(w, "Database failure", http.StatusInternalServerError)
			return

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := glhttp.NewClient()

	// 1. GET with Query parameters & JSON decoding
	type User struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}

	resp, err := client.BaseURL(server.URL).
		WithQueryParam("role", "admin").
		Get("/users")
	if err != nil {
		t.Fatalf("GET /users failed: %v", err)
	}

	if !resp.Successful() || !resp.OK() {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode())
	}

	var users []User
	if err := resp.JSON(&users); err != nil {
		t.Fatalf("failed decoding JSON: %v", err)
	}
	if len(users) != 1 || users[0].Name != "Alice" || users[0].Role != "admin" {
		t.Fatalf("unexpected users response: %+v", users)
	}

	// 2. POST with Bearer Token and JSON body
	newUserData := map[string]any{
		"name":  "Bob",
		"email": "bob@example.com",
	}

	postResp, err := client.BaseURL(server.URL).
		WithToken("secret_api_token").
		Post("/users", newUserData)
	if err != nil {
		t.Fatalf("POST /users failed: %v", err)
	}

	if !postResp.Created() {
		t.Fatalf("expected 201 Created, got %d: %s", postResp.StatusCode(), postResp.String())
	}

	var created map[string]any
	_ = postResp.JSON(&created)
	if created["name"] != "Bob" {
		t.Fatalf("expected Bob, got %v", created["name"])
	}

	// 3. Status checks and Throw() on 404
	errResp, err := client.Get(server.URL + "/error/404")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !errResp.NotFound() || !errResp.ClientError() || !errResp.Failed() {
		t.Fatalf("expected 404 client error")
	}

	throwErr := errResp.Throw()
	if throwErr == nil {
		t.Fatal("expected Throw() to return HTTPError")
	}

	// 4. Multipart File Attachment
	uploadResp, err := client.Attach("receipt", []byte("RECEIPT_DATA_123"), "inv_99.pdf").
		Post(server.URL + "/upload")
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	if !uploadResp.OK() || uploadResp.String() != "inv_99.pdf:RECEIPT_DATA_123" {
		t.Fatalf("unexpected upload response: %s", uploadResp.String())
	}
}

func TestHttpClient_Retry(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&attempts, 1)
		if current < 3 {
			// Fail first two attempts
			http.Error(w, "Temporary Flake", http.StatusServiceUnavailable)
			return
		}
		// Succeed on 3rd attempt
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Recovered"))
	}))
	defer server.Close()

	client := glhttp.NewClient()

	resp, err := client.
		Retry(3, 10*time.Millisecond).
		Get(server.URL + "/flaky")

	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}

	if !resp.OK() || resp.String() != "Recovered" {
		t.Fatalf("expected 200 OK Recovered, got: %d %s", resp.StatusCode(), resp.String())
	}

	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", attempts)
	}
}

func TestHttpClient_Faking_And_Assertions(t *testing.T) {
	client := glhttp.NewClient()

	// 1. Stub specific wildcard pattern
	client.Fake("https://api.github.com/users/*", glhttp.MockResponse(map[string]any{
		"login": "octocat",
		"id":    583231,
	}, 200, map[string]string{
		"X-RateLimit-Remaining": "4999",
	}))

	// 2. Stub exact domain pattern with sequence
	seq := glhttp.Sequence().
		Push(map[string]string{"status": "queued"}, 202).
		Push(map[string]string{"status": "completed"}, 200)

	client.Fake("https://api.stripe.com/v1/charges", seq)

	// Test Github call
	ghResp, err := client.Get("https://api.github.com/users/octocat")
	if err != nil {
		t.Fatalf("fake GET failed: %v", err)
	}
	if !ghResp.OK() {
		t.Fatalf("expected 200 OK, got %d", ghResp.StatusCode())
	}
	if ghResp.Header("X-RateLimit-Remaining") != "4999" {
		t.Fatalf("expected header 4999, got %s", ghResp.Header("X-RateLimit-Remaining"))
	}

	var ghUser map[string]any
	_ = ghResp.JSON(&ghUser)
	if ghUser["login"] != "octocat" {
		t.Fatalf("expected octocat, got %v", ghUser["login"])
	}

	// Test Stripe sequence
	stripe1, _ := client.Post("https://api.stripe.com/v1/charges")
	if !stripe1.Accepted() {
		t.Fatalf("expected 202 Accepted, got %d", stripe1.StatusCode())
	}

	stripe2, _ := client.Post("https://api.stripe.com/v1/charges")
	if !stripe2.OK() {
		t.Fatalf("expected 200 OK, got %d", stripe2.StatusCode())
	}

	// Verify Assertions
	if err := client.AssertSentCount(3); err != nil {
		t.Fatalf("AssertSentCount failed: %v", err)
	}

	err = client.AssertSent(func(req *glhttp.Request) bool {
		return req.URL() == "https://api.github.com/users/octocat" && req.Method() == "GET"
	})
	if err != nil {
		t.Fatalf("AssertSent failed: %v", err)
	}

	err = client.AssertNotSent(func(req *glhttp.Request) bool {
		return req.URL() == "https://api.twitter.com"
	})
	if err != nil {
		t.Fatalf("AssertNotSent failed: %v", err)
	}

	// Reset clears recordings
	client.Reset()
	if err := client.AssertNothingSent(); err != nil {
		t.Fatalf("AssertNothingSent after reset failed: %v", err)
	}
}

func TestHttpClient_GlobalFacade(t *testing.T) {
	defer glhttp.Reset()

	glhttp.Fake("https://jsonplaceholder.typicode.com/posts/1", glhttp.MockResponse(`{"id": 1, "title": "Test Post"}`, 200))

	resp, err := glhttp.Get("https://jsonplaceholder.typicode.com/posts/1")
	if err != nil {
		t.Fatalf("global Get failed: %v", err)
	}

	if !resp.OK() {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode())
	}

	if err := glhttp.AssertSentCount(1); err != nil {
		t.Fatalf("global AssertSentCount failed: %v", err)
	}
}

func TestHttpClient_TimeoutContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := glhttp.NewClient()

	// Should fail with 20ms timeout
	_, err := client.Timeout(20 * time.Millisecond).Get(server.URL)
	if err == nil {
		t.Fatal("expected request to timeout")
	}

	// Should fail with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err = client.WithContext(ctx).Get(server.URL)
	if err == nil {
		t.Fatal("expected request to fail with cancelled context")
	}
}
