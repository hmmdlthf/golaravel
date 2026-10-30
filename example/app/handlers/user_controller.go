package handlers

import (
	"net/http"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/views"
	"github.com/golaravel/golaravel/pkg/container"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/view"
)

// UserController handles HTTP requests for users.
type UserController struct {
	client *app.Client
}

// NewUserController creates a new UserController with the injected Ent client.
func NewUserController(client *app.Client) *UserController {
	return &UserController{
		client: client,
	}
}

// HomeAction renders the framework homepage.
func (ctrl *UserController) HomeAction(c *context.Context) error {
	return c.Templ(http.StatusOK, views.Home())
}

// InvoiceAction renders a standalone customer invoice without downloading heavy client SPA bundles.
func (ctrl *UserController) InvoiceAction(c *context.Context) error {
	token := c.Param("token")
	if token == "" {
		token = "INV-2026-8891"
	}
	return c.Templ(http.StatusOK, views.Invoice(token, "Acme International Corp", 2499.00, true))
}

// Index renders the user directory HTML view.
func (ctrl *UserController) Index(c *context.Context) error {
	users, err := ctrl.client.User.Query().All(c.Context())
	if err != nil {
		return c.String(http.StatusInternalServerError, "Failed to fetch users: %v", err)
	}

	csrf := view.CSRFToken(c)
	return c.Templ(http.StatusOK, views.UsersList(users, csrf))
}

// Store creates a new user from form submission.
func (ctrl *UserController) Store(c *context.Context) error {
	name := c.Request.FormValue("name")
	email := c.Request.FormValue("email")

	if name == "" || email == "" {
		return c.String(http.StatusBadRequest, "Name and Email are required")
	}

	_, err := ctrl.client.User.Create().
		SetName(name).
		SetEmail(email).
		Save(c.Context())
	if err != nil {
		return c.String(http.StatusBadRequest, "Failed to create user: %v", err)
	}

	return c.Redirect(http.StatusSeeOther, "/users")
}

// ApiIndex returns the list of users as high-speed JSON.
func (ctrl *UserController) ApiIndex(c *context.Context) error {
	users, err := ctrl.client.User.Query().All(c.Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, users)
}

// RegisterUserController registers the controller in the DI container.
func RegisterUserController(c *container.Container) {
	container.Singleton(c, func(c *container.Container) (*UserController, error) {
		entClient, err := container.Resolve[*app.Client](c)
		if err != nil {
			return nil, err
		}
		return NewUserController(entClient), nil
	})
}
