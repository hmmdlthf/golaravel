package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// APIError is a gateway's error answer.
type APIError struct {
	Gateway string
	Status  int
	Message string
	Body    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s (HTTP %d)", e.Gateway, e.Message, e.Status)
	}
	return fmt.Sprintf("%s: HTTP %d", e.Gateway, e.Status)
}

// Call is a small JSON/form HTTP helper for drivers.
type Call struct {
	Gateway string // for error messages
	Client  *http.Client
	Method  string
	URL     string
	Header  http.Header
	JSON    any        // sent as application/json
	Form    url.Values // sent as application/x-www-form-urlencoded
}

// Do sends the call and decodes a 2xx JSON answer into out (when not nil).
// A non-2xx answer becomes an *APIError carrying the gateway's message.
func (c Call) Do(ctx context.Context, out any) error {
	var body io.Reader
	ctype := ""
	switch {
	case c.JSON != nil:
		b, err := json.Marshal(c.JSON)
		if err != nil {
			return err
		}
		body, ctype = bytes.NewReader(b), "application/json"
	case c.Form != nil:
		body, ctype = strings.NewReader(c.Form.Encode()), "application/x-www-form-urlencoded"
	}
	req, err := http.NewRequestWithContext(ctx, c.Method, c.URL, body)
	if err != nil {
		return err
	}
	for k, vs := range c.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("Accept", "application/json")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", c.Gateway, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("%s: %w", c.Gateway, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Gateway: c.Gateway, Status: resp.StatusCode, Message: errorMessage(raw), Body: string(raw)}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s: unexpected answer: %w", c.Gateway, err)
		}
	}
	return nil
}

// errorMessage digs the human message out of the usual error shapes:
// {"error":{"message":..}}, {"message":..}, {"error":".."}, {"errors":[{"message"|"error_message":..}]}.
func errorMessage(raw []byte) string {
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		s := strings.TrimSpace(string(raw))
		if len(s) > 200 {
			s = s[:200]
		}
		return s
	}
	if e, ok := v["error"].(map[string]any); ok {
		if m, ok := e["message"].(string); ok {
			return m
		}
	}
	for _, k := range []string{"message", "error_message", "error", "msg"} {
		if m, ok := v[k].(string); ok && m != "" {
			return m
		}
	}
	if errs, ok := v["errors"].([]any); ok && len(errs) > 0 {
		if e, ok := errs[0].(map[string]any); ok {
			for _, k := range []string{"message", "error_message", "error_code"} {
				if m, ok := e[k].(string); ok {
					return m
				}
			}
		}
	}
	return ""
}

// Bearer is an Authorization header with a bearer token.
func Bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

// Pick returns the first non-empty string.
func Pick(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// FirstLast splits a full name for gateways that want both parts.
func FirstLast(c Customer, fallback string) (string, string) {
	first, last := strings.TrimSpace(c.FirstName), strings.TrimSpace(c.LastName)
	if first == "" && last == "" {
		return fallback, fallback
	}
	if last == "" {
		last = first
	}
	if first == "" {
		first = last
	}
	return first, last
}
