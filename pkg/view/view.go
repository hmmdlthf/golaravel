package view

import (
	"context"
	"io"

	"github.com/a-h/templ"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/middleware"
)

// Render renders a templ component into the GoLaravel context with status code.
func Render(c *gcontext.Context, status int, cmp templ.Component) error {
	return c.Templ(status, cmp)
}

// CSRFToken retrieves the active CSRF token for the request.
func CSRFToken(c *gcontext.Context) string {
	return middleware.Token(c)
}

// RawHTML renders raw unescaped HTML inside a templ component.
func RawHTML(html string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, html)
		return err
	})
}
