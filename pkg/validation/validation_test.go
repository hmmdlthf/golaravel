package validation

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
)

type RegisterRequest struct {
	Name                 string `json:"name"`
	Email                string `json:"email"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
}

func (r *RegisterRequest) Validate(v *Validator) {
	v.Required("name", r.Name).
		Required("email", r.Email).
		Email("email", r.Email).
		Required("password", r.Password).
		MinLength("password", r.Password, 8).
		Confirmed("password", r.Password, r.PasswordConfirmation)
}

func TestValidation_ValidRequest(t *testing.T) {
	req := &RegisterRequest{
		Name:                 "Taylor Otwell",
		Email:                "taylor@laravel.com",
		Password:             "secret1234",
		PasswordConfirmation: "secret1234",
	}

	w := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/register", nil)
	c := context.NewContext()
	c.Reset(w, httpReq)

	passes := ValidateAndRespond(c, req)
	if !passes {
		t.Fatal("expected validation to pass")
	}
}

func TestValidation_422Response(t *testing.T) {
	req := &RegisterRequest{
		Name:                 "",
		Email:                "not-an-email",
		Password:             "short",
		PasswordConfirmation: "mismatch",
	}

	body := []byte(`{"name":"","email":"not-an-email","password":"short","password_confirmation":"mismatch"}`)
	w := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	c := context.NewContext()
	c.Reset(w, httpReq)

	passes := ValidateAndRespond(c, req)
	if passes {
		t.Fatal("expected validation to fail")
	}

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}

	if !c.IsAborted() {
		t.Fatal("expected context to be aborted on validation failure")
	}
}
