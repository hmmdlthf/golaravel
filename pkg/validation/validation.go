package validation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	errors    ErrorBag
	data      map[string]any
	db        DBChecker
	translate func(key string, params map[string]any) string
}

// RulesProvider is implemented by form requests that declare rule strings
// instead of (or as well as) code in Validate. ValidateAndRespond runs them
// against the raw request input, so "required" sees whether a key was sent.
type RulesProvider interface {
	Rules() Rules
}

// MessagesProvider and AttributesProvider customise messages and field
// names for a RulesProvider.
type MessagesProvider interface {
	Messages() map[string]string
}

type AttributesProvider interface {
	Attributes() map[string]string
}

var (
	defaultDB DBChecker
)

// SetDefaultDB sets the checker used by unique / exists rules when a
// validator has none (for example SQLChecker(db, dialect) at boot).
func SetDefaultDB(db DBChecker) { defaultDB = db }

// WithData gives Check access to the whole input, for rules that look at
// other fields (confirmed, same, required_with, ...).
func (v *Validator) WithData(data map[string]any) *Validator {
	v.data = data
	return v
}

// Data returns the request input when the validator was created by
// ValidateAndRespond.
func (v *Validator) Data() map[string]any { return v.data }

// Check validates one value against a rule string (or rule list), for
// example v.Check("age", r.Age, "required|integer|between:18,120").
func (v *Validator) Check(field string, value any, rules any) *Validator {
	data := map[string]any{}
	for k, x := range v.data {
		data[k] = x
	}
	set(data, field, value)
	rv := Make(data, Rules{field: rules}).DB(v.dbChecker())
	if v.translate != nil {
		rv.Translator(v.translate)
	}
	for f, msgs := range rv.Errors() {
		v.errors[f] = append(v.errors[f], msgs...)
	}
	return v
}

func (v *Validator) dbChecker() DBChecker {
	if v.db != nil {
		return v.db
	}
	return defaultDB
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

// RequestData reads the request input into a map: the JSON body (numbers
// as json.Number), or form fields and uploaded files for form posts. The
// JSON body is buffered, so it can still be bound afterwards.
func RequestData(c *context.Context) (map[string]any, error) {
	data := map[string]any{}
	ct := c.Header("Content-Type")
	switch {
	case strings.Contains(ct, "application/json"):
		if c.Request.Body == nil {
			return data, nil
		}
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return nil, err
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		if len(bytes.TrimSpace(raw)) == 0 {
			return data, nil
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&data); err != nil {
			return nil, err
		}
	case strings.Contains(ct, "multipart/form-data"):
		if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
			return nil, err
		}
		addForm(data, c.Request.MultipartForm.Value)
		for k, fs := range c.Request.MultipartForm.File {
			key := strings.TrimSuffix(k, "[]")
			if len(fs) == 1 && !strings.HasSuffix(k, "[]") {
				data[key] = fs[0]
			} else {
				data[key] = fs
			}
		}
	default:
		if err := c.Request.ParseForm(); err != nil {
			return nil, err
		}
		addForm(data, c.Request.PostForm)
		if len(data) == 0 && c.Request.Method == http.MethodGet {
			addForm(data, c.Request.URL.Query())
		}
	}
	return data, nil
}

func addForm(data map[string]any, values map[string][]string) {
	for k, vs := range values {
		key := strings.TrimSuffix(k, "[]")
		if len(vs) == 1 && !strings.HasSuffix(k, "[]") {
			data[key] = vs[0]
			continue
		}
		list := make([]any, len(vs))
		for i, x := range vs {
			list[i] = x
		}
		data[key] = list
	}
}

const validatedKey = "validation.validated"

// Validated returns the input covered by rules after a successful
// ValidateAndRespond / ValidateRequest on c.
func Validated(c *context.Context) map[string]any {
	if v, ok := c.Get(validatedKey); ok {
		if m, ok := v.(map[string]any); ok {
			return m
		}
	}
	return map[string]any{}
}

func respond422(c *context.Context, errs ErrorBag) {
	c.AbortWithStatus(http.StatusUnprocessableEntity)
	_ = c.JSON(http.StatusUnprocessableEntity, ValidationError{Message: "The given data was invalid.", Errors: errs})
}

func translatorFor(c *context.Context) func(string, map[string]any) string {
	return func(key string, params map[string]any) string { return c.Trans(key, params) }
}

// ValidateRequest validates the request input against rules, answering 422
// (and returning false) when it fails. The validated input is returned and
// also available through Validated(c).
func ValidateRequest(c *context.Context, rules Rules, messages ...map[string]string) (map[string]any, bool) {
	data, err := RequestData(c)
	if err != nil {
		_ = c.JSON(http.StatusBadRequest, map[string]string{"message": "Malformed request body", "error": err.Error()})
		c.Abort()
		return nil, false
	}
	rv := Make(data, rules).WithContext(c.Request.Context()).DB(defaultDB).Translator(translatorFor(c))
	if len(messages) > 0 {
		rv.Messages(messages[0])
	}
	if rv.Fails() {
		respond422(c, rv.Errors())
		return nil, false
	}
	c.Set(validatedKey, rv.Validated())
	return rv.Validated(), true
}

// ValidateAndRespond binds and validates a FormRequest, automatically sending
// a 422 Unprocessable Entity response if invalid. Requests that implement
// RulesProvider also have their Rules() checked against the raw input.
func ValidateAndRespond(c *context.Context, req FormRequest) bool {
	data, err := RequestData(c)
	if err != nil {
		_ = c.JSON(http.StatusBadRequest, map[string]string{
			"message": "Malformed request body",
			"error":   err.Error(),
		})
		c.Abort()
		return false
	}
	// Attempt JSON binding if request content-type is application/json
	if strings.Contains(c.Header("Content-Type"), "application/json") {
		if err := c.BindJSON(req); err != nil && !errors.Is(err, io.EOF) {
			_ = c.JSON(http.StatusBadRequest, map[string]string{
				"message": "Malformed JSON request body",
				"error":   err.Error(),
			})
			c.Abort()
			return false
		}
	}

	v := New()
	v.data = data
	v.translate = translatorFor(c)
	errs := ErrorBag{}
	if rp, ok := req.(RulesProvider); ok {
		rv := Make(data, rp.Rules()).WithContext(c.Request.Context()).DB(defaultDB).Translator(v.translate)
		if mp, ok := req.(MessagesProvider); ok {
			rv.Messages(mp.Messages())
		}
		if ap, ok := req.(AttributesProvider); ok {
			rv.Attributes(ap.Attributes())
		}
		for f, m := range rv.Errors() {
			errs[f] = append(errs[f], m...)
		}
		c.Set(validatedKey, rv.Validated())
	}
	req.Validate(v)
	for f, m := range v.Errors() {
		errs[f] = append(errs[f], m...)
	}

	if len(errs) > 0 {
		respond422(c, errs)
		return false
	}
	return true
}
