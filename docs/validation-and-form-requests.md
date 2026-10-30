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
