package validation

import (
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/golaravel/golaravel/pkg/context"
)

// ErrorBag maps field names to a list of human-readable error messages.
type ErrorBag map[string][]string

// ValidationError represents an HTTP 422 validation failure matching Laravel's format.
type ValidationError struct {
	Message string   `json:"message"`
	Errors  ErrorBag `json:"errors"`
}

func (v *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %s (%d fields with errors)", v.Message, len(v.Errors))
}

// FormRequest is an interface that structs can implement to define custom validation rules.
type FormRequest interface {
	Validate(v *Validator)
}

// Validator validates inputs and accumulates errors.
type Validator struct {
	errors ErrorBag
}

// New creates a new Validator.
func New() *Validator {
	return &Validator{
		errors: make(ErrorBag),
	}
}

// AddError adds a validation error message to a field.
func (v *Validator) AddError(field, message string) {
	v.errors[field] = append(v.errors[field], message)
}

// HasErrors returns true if any validation rules failed.
func (v *Validator) HasErrors() bool {
	return len(v.errors) > 0
}

// Errors returns the accumulated error bag.
func (v *Validator) Errors() ErrorBag {
	return v.errors
}

// Required checks if a string field is not empty.
func (v *Validator) Required(field, val string) *Validator {
	if strings.TrimSpace(val) == "" {
		v.AddError(field, fmt.Sprintf("The %s field is required.", field))
	}
	return v
}

// Email checks if the string is a valid email address.
func (v *Validator) Email(field, val string) *Validator {
	if val == "" {
		return v
	}
	_, err := mail.ParseAddress(val)
	if err != nil || !strings.Contains(val, "@") || !strings.Contains(val, ".") {
		v.AddError(field, fmt.Sprintf("The %s must be a valid email address.", field))
	}
	return v
}

// MinLength checks the minimum string length in runes.
func (v *Validator) MinLength(field, val string, min int) *Validator {
	if val != "" && utf8.RuneCountInString(val) < min {
		v.AddError(field, fmt.Sprintf("The %s must be at least %d characters.", field, min))
	}
	return v
}

// MaxLength checks the maximum string length in runes.
func (v *Validator) MaxLength(field, val string, max int) *Validator {
	if utf8.RuneCountInString(val) > max {
		v.AddError(field, fmt.Sprintf("The %s may not be greater than %d characters.", field, max))
	}
	return v
}

// Confirmed checks if field matches field_confirmation (e.g. password & password_confirmation).
func (v *Validator) Confirmed(field, val, confirmationVal string) *Validator {
	if val != confirmationVal {
		v.AddError(field, fmt.Sprintf("The %s confirmation does not match.", field))
	}
	return v
}

// Numeric checks if string value can be parsed as a number.
func (v *Validator) Numeric(field, val string) *Validator {
	if val != "" {
		if _, err := strconv.ParseFloat(val, 64); err != nil {
			v.AddError(field, fmt.Sprintf("The %s must be a number.", field))
		}
	}
	return v
}

// Rule executes a custom boolean predicate function.
// Useful for checking unique/exists queries with Ent ORM.
func (v *Validator) Rule(field, message string, passes bool) *Validator {
	if !passes {
		v.AddError(field, message)
	}
	return v
}

// ValidateAndRespond binds and validates a FormRequest, automatically sending
// a 422 Unprocessable Entity response if invalid.
func ValidateAndRespond(c *context.Context, req FormRequest) bool {
	// Attempt JSON binding if request content-type is application/json
	if strings.Contains(c.Header("Content-Type"), "application/json") {
		if err := c.BindJSON(req); err != nil {
			_ = c.JSON(http.StatusBadRequest, map[string]string{
				"message": "Malformed JSON request body",
				"error":   err.Error(),
			})
			c.Abort()
			return false
		}
	}

	v := New()
	req.Validate(v)

	if v.HasErrors() {
		errResp := ValidationError{
			Message: "The given data was invalid.",
			Errors:  v.Errors(),
		}
		c.AbortWithStatus(http.StatusUnprocessableEntity)
		_ = c.JSON(http.StatusUnprocessableEntity, errResp)
		return false
	}

	return true
}
