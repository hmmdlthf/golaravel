# Validation & Form Requests 🛡️

GoLaravel provides a clean, declarative validation engine that automatically binds incoming HTTP request data and formats errors into Laravel-standard **HTTP 422 Unprocessable Entity** error bags.

---

## 1. Defining a Form Request

Create a struct representing the expected input and implement the `validation.FormRequest` interface:

```go
package requests

import "github.com/golaravel/golaravel/pkg/validation"

type StoreUserRequest struct {
	Name                 string `json:"name"`
	Email                string `json:"email"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
	Age                  string `json:"age"`
}

func (r *StoreUserRequest) Validate(v *validation.Validator) {
	v.Required("name", r.Name).
		MinLength("name", r.Name, 2).
		Required("email", r.Email).
		Email("email", r.Email).
		Required("password", r.Password).
		MinLength("password", r.Password, 8).
		Confirmed("password", r.Password, r.PasswordConfirmation).
		Numeric("age", r.Age)
}
```

---

## 2. Using Form Requests in Handlers

Use `validation.ValidateAndRespond(c, &request)` in your handler. If validation fails, it immediately sends a 422 response, aborts the pipeline, and returns `false`:

```go
func (ctrl *UserController) Store(c *golaravel.Context) error {
	var req requests.StoreUserRequest
	if !validation.ValidateAndRespond(c, &req) {
		return nil // Automatically responded with 422
	}

	// Data is valid! Proceed with Ent ORM creation:
	user, err := ctrl.client.User.Create().
		SetName(req.Name).
		SetEmail(req.Email).
		Save(c.Context())
	if err != nil {
		return c.JSON(500, map[string]string{"error": err.Error()})
	}

	return c.JSON(201, user)
}
```

---

## 3. Laravel-Compatible 422 Error Bag

When validation fails, GoLaravel responds with the exact JSON structure expected by Laravel frontends (such as Axios, Inertia, or Flutter):

```json
{
  "message": "The given data was invalid.",
  "errors": {
    "email": [
      "The email field is required."
    ],
    "password": [
      "The password must be at least 8 characters.",
      "The password confirmation does not match."
    ]
  }
}
```

---

## 4. Built-in Validation Rules

| Rule | Description |
|---|---|
| `v.Required(field, val)` | Field must not be empty or whitespace |
| `v.Email(field, val)` | Value must be a valid email address |
| `v.MinLength(field, val, min)` | String length must be at least `min` characters |
| `v.MaxLength(field, val, max)` | String length must not exceed `max` characters |
| `v.Confirmed(field, val, confirmation)` | Field must match `field_confirmation` |
| `v.Numeric(field, val)` | Value must be parseable as a number |
| `v.Rule(field, msg, passesBool)` | Custom boolean predicate (ideal for Ent `exists` / `unique` queries) |

---

## Rule strings (Laravel-style)

The fluent methods above cover simple checks. For everything else, describe each field with a rule
string. Rules run against the raw input, so `required` can tell whether a key was sent at all.

```go
type StoreOrderRequest struct {
	CustomerID int         `json:"customer_id"`
	Items      []OrderItem `json:"items"`
}

func (r *StoreOrderRequest) Rules() validation.Rules {
	return validation.Rules{
		"customer_id": "required|integer|exists:customers,id",
		"email":       "required|email|unique:users,email",
		"due_date":    "nullable|date|after_or_equal:today",
		"items":       "required|array|min:1",
		"items.*.sku": "required|string|max:32",
		"items.*.qty": "required|integer|between:1,999",
		"coupon":      []any{"sometimes", "alpha_num", myCouponRule},
	}
}

// Optional:
func (r *StoreOrderRequest) Messages() map[string]string {
	return map[string]string{"items.*.qty.between": "Each quantity must be 1–999."}
}
func (r *StoreOrderRequest) Attributes() map[string]string {
	return map[string]string{"customer_id": "customer"}
}

func (r *StoreOrderRequest) Validate(v *validation.Validator) {
	// Code checks can still be added here, and rule strings used inline:
	v.Check("items", r.Items, "max:50")
}
```

`ValidateAndRespond` runs `Rules()` and `Validate(v)`, then merges both sets of errors into one 422
response. It reads JSON, urlencoded and multipart input. Without a struct:

```go
data, ok := validation.ValidateRequest(c, validation.Rules{"name": "required|string|max:100"})
if !ok {
	return nil // 422 already sent
}
// data (also validation.Validated(c)) holds only the fields listed in the rules.
```

Outside HTTP, use `validation.Make(data, rules)` with `.Fails()`, `.Errors()`, `.Validate()` and
`.Validated()`.

### Available rules

| Group | Rules |
|---|---|
| Exclusion | `exclude`, `exclude_if:f,v…`, `exclude_unless:f,v…`, `exclude_with:f`, `exclude_without:f` (the field is skipped and left out of `Validated()`) |
| Presence | `required`, `required_if:f,v…`, `required_unless:f,v…`, `required_with:f…`, `required_with_all`, `required_without`, `required_without_all`, `filled`, `present`, `sometimes`, `nullable`, `prohibited`, `prohibited_if`, `prohibited_unless`, `accepted`, `declined`, `bail` |
| Types | `string`, `integer`, `numeric`, `decimal:min[,max]`, `boolean`, `array[:keys]`, `required_array_keys:k…`, `list`, `json` |
| Formats | `email`, `url[:schemes]`, `uuid`, `ulid`, `ip`, `ipv4`, `ipv6`, `alpha`, `alpha_num`, `alpha_dash`, `lowercase`, `uppercase`, `regex:pattern`, `not_regex:pattern`, `starts_with`, `ends_with`, `doesnt_start_with`, `doesnt_end_with`, `timezone` |
| Size | `min`, `max`, `size`, `between`, `gt`, `gte`, `lt`, `lte` (a number or another field), `digits`, `digits_between`, `multiple_of` |
| Comparison | `confirmed`, `same:f`, `different:f`, `in:a,b`, `not_in:a,b`, `distinct` |
| Dates | `date`, `date_format:<Go layout>`, `before`, `after`, `before_or_equal`, `after_or_equal`, `date_equals` (a date, `today`/`tomorrow`/`yesterday`/`now`, or another field) |
| Files | `file`, `image`, `mimes:png,jpg`, `mimetypes:image/*`, `extensions:pdf`, `dimensions:min_width=100,max_height=800,ratio=3/2` (PNG, JPEG, GIF) |
| Database | `unique:table[,column[,ignoreID[,idColumn]]]`, `exists:table[,column]` |

Things to know:

- **Empty values skip most rules.** A missing, `null` or blank value skips every rule except the
  `required*`, `present`, `filled`, `accepted`, `declined` and `prohibited*` rules. Add `required`
  if a field must be sent.
- **Size rules follow the field's type.** `min`, `max`, `size` and `between` measure a number's
  value (once the field has `numeric` or `integer`), a list's item count, a file's size in
  kilobytes, or a string's length in characters.
- **Files are checked by content.** `image` and `mimes` look at the file's actual content, not the
  file name or the type the client sent. `image` rejects SVG, which can contain scripts.
- **Database rules** need a checker. Set it once with
  `validation.SetDefaultDB(validation.SQLChecker(db, database.Postgres))`, or per validator with
  `.DB(...)`.
- **Regexes** use Go syntax, optionally as `/.../i`. A `regex:` rule must come last in a rule
  string; use a rule list if the pattern contains `|`.
- **Translations:** messages are looked up as `validation.<rule>` (`validation.min.string` for size
  rules) in your `lang/<locale>/validation.json`, falling back to English.
- **Custom rules:** implement `validation.Rule`, or use `validation.RuleFunc(message, fn)`.

### Password strength

```go
"password": []any{"required", "confirmed",
	validation.Password().Min(12).MixedCase().Numbers().Symbols().NotIn(commonPasswords...)},
```

Every unmet requirement gets its own message ("must contain at least one number", ...). Other
options: `Letters()` and `Max(n)`. Custom rules can report several messages in the same way by
implementing `validation.MultiRule`.
