package validation

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/database"
)

func errsFor(t *testing.T, data map[string]any, rules Rules) ErrorBag {
	t.Helper()
	return Make(data, rules).Errors()
}

func expectValid(t *testing.T, data map[string]any, rules Rules) {
	t.Helper()
	if e := errsFor(t, data, rules); len(e) > 0 {
		t.Errorf("expected valid, got %v", e)
	}
}

func expectInvalid(t *testing.T, field string, data map[string]any, rules Rules) []string {
	t.Helper()
	e := errsFor(t, data, rules)
	if len(e[field]) == 0 {
		t.Errorf("expected an error on %q for %v with %v, got %v", field, data, rules, e)
	}
	return e[field]
}

func TestPresenceRules(t *testing.T) {
	expectInvalid(t, "name", map[string]any{}, Rules{"name": "required"})
	expectInvalid(t, "name", map[string]any{"name": "   "}, Rules{"name": "required"})
	expectValid(t, map[string]any{"name": "x"}, Rules{"name": "required"})
	// Non-required rules are skipped for empty values.
	expectValid(t, map[string]any{}, Rules{"age": "integer|min:18"})
	expectValid(t, map[string]any{"age": nil}, Rules{"age": "nullable|integer"})
	// sometimes: only when present.
	expectValid(t, map[string]any{}, Rules{"nick": "sometimes|required"})
	expectInvalid(t, "nick", map[string]any{"nick": ""}, Rules{"nick": "sometimes|required"})

	r := Rules{"company": "required_if:type,business"}
	expectInvalid(t, "company", map[string]any{"type": "business"}, r)
	expectValid(t, map[string]any{"type": "person"}, r)
	expectInvalid(t, "zip", map[string]any{"city": "Colombo"}, Rules{"zip": "required_with:city"})
	expectValid(t, map[string]any{}, Rules{"zip": "required_with:city"})
	expectInvalid(t, "phone", map[string]any{}, Rules{"phone": "required_without:email"})
	expectValid(t, map[string]any{"email": "a@b.co"}, Rules{"phone": "required_without:email"})
	expectInvalid(t, "admin", map[string]any{"admin": true}, Rules{"admin": "prohibited"})
	expectInvalid(t, "terms", map[string]any{"terms": "no"}, Rules{"terms": "accepted"})
	expectValid(t, map[string]any{"terms": true}, Rules{"terms": "accepted"})
}

func TestTypeAndFormatRules(t *testing.T) {
	for _, tc := range []struct {
		rule string
		good []any
		bad  []any
	}{
		{"string", []any{"x"}, []any{5, true}},
		{"integer", []any{5, int64(7), "12", json.Number("3"), 4.0}, []any{"1.5", 2.5, "x"}},
		{"numeric", []any{"1.5", 3, json.Number("2.25")}, []any{"abc"}},
		{"boolean", []any{true, "0", "1", "true"}, []any{"yes", 2}},
		{"array", []any{[]any{1}, map[string]any{"a": 1}}, []any{"x"}},
		{"email", []any{"ann@example.com"}, []any{"ann", "ann@localhost", "Ann <ann@example.com>"}},
		{"url", []any{"https://example.com/x"}, []any{"example.com", "javascript:alert(1)", "ftp://x.org"}},
		{"uuid", []any{"123e4567-e89b-12d3-a456-426614174000"}, []any{"123e4567"}},
		{"ip", []any{"10.0.0.1", "::1"}, []any{"999.1.1.1"}},
		{"ipv4", []any{"10.0.0.1"}, []any{"::1"}},
		{"ipv6", []any{"::1"}, []any{"10.0.0.1"}},
		{"json", []any{`{"a":1}`}, []any{`{a:1}`}},
		{"alpha", []any{"Zoë"}, []any{"a1"}},
		{"alpha_num", []any{"a1"}, []any{"a-1"}},
		{"alpha_dash", []any{"a-1_b"}, []any{"a 1"}},
		{"lowercase", []any{"abc"}, []any{"Abc"}},
		{"regex:^[A-Z]{3}-\\d+$", []any{"INV-12"}, []any{"inv-12"}},
		{"regex:/^inv-\\d+$/i", []any{"INV-12"}, []any{"X"}},
		{"not_regex:\\s", []any{"nospace"}, []any{"has space"}},
		{"starts_with:INV-,PUR-", []any{"PUR-1"}, []any{"X-1"}},
		{"in:draft,posted", []any{"draft", []any{"draft", "posted"}}, []any{"void", []any{"draft", "void"}}},
		{"not_in:root,admin", []any{"ann"}, []any{"root"}},
		{"date", []any{"2026-11-16", "2026-11-16T10:00:00Z", time.Now()}, []any{"16/11/2026"}},
		{"date_format:02/01/2006", []any{"16/11/2026"}, []any{"2026-11-16"}},
		{"timezone", []any{"Asia/Colombo"}, []any{"Mars/Base"}},
		{"digits:4", []any{"2026", 1234}, []any{"20.6", "123"}},
		{"digits_between:2,4", []any{"123"}, []any{"1", "12345"}},
		{"decimal:2", []any{"1.25", json.Number("3.10")}, []any{"1.2", "1"}},
		{"multiple_of:5", []any{10, "15"}, []any{7}},
		{"distinct", []any{[]any{1, 2}}, []any{[]any{"a", "a"}}},
	} {
		for _, g := range tc.good {
			if e := errsFor(t, map[string]any{"f": g}, Rules{"f": []string{tc.rule}}); len(e) > 0 {
				t.Errorf("%s: %v should pass: %v", tc.rule, g, e)
			}
		}
		for _, b := range tc.bad {
			if e := errsFor(t, map[string]any{"f": b}, Rules{"f": []string{tc.rule}}); len(e) == 0 {
				t.Errorf("%s: %v should fail", tc.rule, b)
			}
		}
	}
}

func TestSizeRulesDependOnType(t *testing.T) {
	// Strings: characters.
	expectInvalid(t, "name", map[string]any{"name": "ab"}, Rules{"name": "string|min:3"})
	expectValid(t, map[string]any{"name": "Zoë"}, Rules{"name": "string|min:3|max:3"})
	// Numbers: value, once the field is numeric.
	expectValid(t, map[string]any{"age": "30"}, Rules{"age": "integer|between:18,120"})
	msgs := expectInvalid(t, "age", map[string]any{"age": "17"}, Rules{"age": "integer|min:18"})
	if msgs[0] != "The age field must be at least 18." {
		t.Errorf("numeric min message: %q", msgs[0])
	}
	// Lists: item count.
	msgs = expectInvalid(t, "tags", map[string]any{"tags": []any{1, 2, 3}}, Rules{"tags": "array|max:2"})
	if !strings.Contains(msgs[0], "more than 2 items") {
		t.Errorf("array max message: %q", msgs[0])
	}
	// Comparing with another field.
	expectInvalid(t, "end", map[string]any{"start": 10, "end": 5}, Rules{"end": "numeric|gt:start"})
	expectValid(t, map[string]any{"start": 10, "end": 15}, Rules{"end": "numeric|gte:start"})
}

func TestComparisonAndDateRules(t *testing.T) {
	expectInvalid(t, "password", map[string]any{"password": "a", "password_confirmation": "b"}, Rules{"password": "confirmed"})
	expectValid(t, map[string]any{"password": "a", "password_confirmation": "a"}, Rules{"password": "confirmed"})
	expectInvalid(t, "b", map[string]any{"a": "x", "b": "y"}, Rules{"b": "same:a"})
	expectInvalid(t, "b", map[string]any{"a": "x", "b": "x"}, Rules{"b": "different:a"})
	expectInvalid(t, "due", map[string]any{"issued": "2026-11-10", "due": "2026-11-01"}, Rules{"due": "date|after_or_equal:issued"})
	expectValid(t, map[string]any{"issued": "2026-11-10", "due": "2026-11-10"}, Rules{"due": "date|after_or_equal:issued"})
	expectInvalid(t, "dob", map[string]any{"dob": time.Now().AddDate(0, 0, 2).Format("2006-01-02")}, Rules{"dob": "date|before:today"})
}

func TestNestedAndWildcardFields(t *testing.T) {
	data := map[string]any{
		"customer": map[string]any{"email": "bad"},
		"items": []any{
			map[string]any{"sku": "A1", "qty": 2},
			map[string]any{"sku": "", "qty": 0},
		},
	}
	e := errsFor(t, data, Rules{
		"customer.email": "required|email",
		"items":          "required|array|min:1",
		"items.*.sku":    "required|string",
		"items.*.qty":    "integer|min:1",
	})
	for _, f := range []string{"customer.email", "items.1.sku", "items.1.qty"} {
		if len(e[f]) == 0 {
			t.Errorf("missing error for %s: %v", f, e)
		}
	}
	if len(e["items.0.sku"]) > 0 || len(e["items.0.qty"]) > 0 {
		t.Errorf("valid item flagged: %v", e)
	}
	// Missing parents: wildcard rules have nothing to check.
	expectValid(t, map[string]any{}, Rules{"items.*.sku": "required"})
}

func TestValidatedOnlyReturnsRuledFields(t *testing.T) {
	v := Make(map[string]any{"name": "Ann", "is_admin": true, "items": []any{map[string]any{"sku": "A", "x": 1}}},
		Rules{"name": "required", "items.*.sku": "required"})
	if v.Fails() {
		t.Fatal(v.Errors())
	}
	got := v.Validated()
	if _, ok := got["is_admin"]; ok {
		t.Fatalf("unruled field leaked into Validated: %v", got)
	}
	if got["name"] != "Ann" {
		t.Fatalf("validated: %v", got)
	}
}

func TestMessagesAttributesTranslatorAndBail(t *testing.T) {
	v := Make(map[string]any{"dob": "x"}, Rules{"dob": "bail|date|before:today", "email": "required"}).
		Attributes(map[string]string{"dob": "date of birth"}).
		Messages(map[string]string{"email.required": "We need your :attribute."})
	e := v.Errors()
	if len(e["dob"]) != 1 || e["dob"][0] != "The date of birth field must be a valid date." {
		t.Errorf("bail / attribute: %v", e["dob"])
	}
	if e["email"][0] != "We need your email." {
		t.Errorf("custom message: %v", e["email"])
	}
	tr := Make(map[string]any{}, Rules{"name": "required"}).Translator(func(key string, p map[string]any) string {
		if key == "validation.required" {
			return "El campo " + p["attribute"].(string) + " es obligatorio."
		}
		return key
	})
	if got := tr.Errors()["name"][0]; got != "El campo name es obligatorio." {
		t.Errorf("translator: %q", got)
	}
	if got := errsFor(t, map[string]any{"x": "1"}, Rules{"x": "frobnicate"})["x"][0]; !strings.Contains(got, "unknown rule") {
		t.Errorf("unknown rules must be reported: %q", got)
	}
}

func TestCustomRule(t *testing.T) {
	even := RuleFunc("The :attribute must be even.", func(v any) bool { f, _ := toFloat(v); return int(f)%2 == 0 })
	e := errsFor(t, map[string]any{"n": 3}, Rules{"n": []any{"required", "integer", even}})
	if len(e["n"]) != 1 || e["n"][0] != "The n must be even." {
		t.Errorf("custom rule: %v", e)
	}
}

func TestDatabaseRules(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, _ = db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT); INSERT INTO users (id, email) VALUES (1, 'ann@example.com'), (2, 'bob@example.com')`)
	chk := SQLChecker(db, database.SQLite)
	run := func(data map[string]any, rules Rules) ErrorBag { return Make(data, rules).DB(chk).Errors() }

	if e := run(map[string]any{"email": "ann@example.com"}, Rules{"email": "unique:users,email"}); e["email"][0] != "The email has already been taken." {
		t.Errorf("unique: %v", e)
	}
	if e := run(map[string]any{"email": "new@example.com"}, Rules{"email": "unique:users"}); len(e) > 0 {
		t.Errorf("unique (column from field name): %v", e)
	}
	if e := run(map[string]any{"email": "ann@example.com"}, Rules{"email": "unique:users,email,1"}); len(e) > 0 {
		t.Errorf("unique ignoring own row: %v", e)
	}
	if e := run(map[string]any{"user_id": 9}, Rules{"user_id": "exists:users,id"}); len(e) == 0 {
		t.Error("exists should fail for a missing row")
	}
	if e := run(map[string]any{"ids": []any{1, 2}}, Rules{"ids": "exists:users,id"}); len(e) > 0 {
		t.Errorf("exists over a list: %v", e)
	}
	if e := run(map[string]any{"email": "x"}, Rules{"email": "unique:users;drop,email"}); len(e) == 0 {
		t.Error("unsafe table name must fail")
	}
	if e := Make(map[string]any{"email": "x"}, Rules{"email": "unique:users"}).Errors(); !strings.Contains(e["email"][0], "no database checker") {
		t.Errorf("missing checker must be reported: %v", e)
	}
}

func multipartFile(t *testing.T, name string, content []byte) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("f", name)
	fw.Write(content)
	w.Close()
	req := httptest.NewRequest("POST", "/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	return req.MultipartForm.File["f"][0]
}

func TestFileRules(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 2048)...)
	img := multipartFile(t, "a.png", png)
	fake := multipartFile(t, "evil.png", []byte("<?php echo 1; ?>"))
	csv := multipartFile(t, "list.csv", []byte("a,b\n1,2\n"))

	expectValid(t, map[string]any{"f": img}, Rules{"f": "file|image|mimes:png,jpg|max:10"})
	expectInvalid(t, "f", map[string]any{"f": fake}, Rules{"f": "image"})
	expectInvalid(t, "f", map[string]any{"f": fake}, Rules{"f": "mimes:png"})
	expectValid(t, map[string]any{"f": csv}, Rules{"f": "mimes:csv,txt"})
	expectInvalid(t, "f", map[string]any{"f": csv}, Rules{"f": "mimes:png"})
	msgs := expectInvalid(t, "f", map[string]any{"f": img}, Rules{"f": "file|max:1"})
	if !strings.Contains(msgs[0], "kilobytes") {
		t.Errorf("file size message: %q", msgs[0])
	}
	expectInvalid(t, "f", map[string]any{"f": "not a file"}, Rules{"f": "file"})
}

type signupRequest struct {
	Email string `json:"email"`
	Age   int    `json:"age"`
}

func (r *signupRequest) Validate(v *Validator) {
	v.Check("age", r.Age, "integer|min:18")
}

func (r *signupRequest) Rules() Rules {
	return Rules{"email": "required|email", "age": "required"}
}

func TestValidateAndRespondWithRules(t *testing.T) {
	do := func(body string) (*httptest.ResponseRecorder, *gcontext.Context) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		c := &gcontext.Context{}
		c.Reset(rec, req)
		ValidateAndRespond(c, &signupRequest{})
		return rec, c
	}
	rec, _ := do(`{"email":"bad","age":16}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	var body ValidationError
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Errors["email"]) == 0 || len(body.Errors["age"]) == 0 {
		t.Fatalf("errors from Rules() and Validate() must be merged: %v", body.Errors)
	}
	rec, c := do(`{"email":"ann@example.com","age":30,"is_admin":true}`)
	if rec.Body.Len() > 0 {
		t.Fatalf("valid request must not be answered by the validator: %d %s", rec.Code, rec.Body.String())
	}
	if _, leaked := Validated(c)["is_admin"]; leaked {
		t.Fatal("Validated(c) must hold only ruled fields")
	}
}

func TestValidateRequestForm(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader("qty=0&tags[]=a&tags[]=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c := &gcontext.Context{}
	c.Reset(rec, req)
	_, ok := ValidateRequest(c, Rules{"qty": "required|integer|min:1", "tags": "array|max:1"})
	if ok || rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("form validation: ok=%v code=%d", ok, rec.Code)
	}
	var body ValidationError
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Errors["qty"]) == 0 || len(body.Errors["tags"]) == 0 {
		t.Fatalf("form errors: %v", body.Errors)
	}
}
