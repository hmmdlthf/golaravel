# Templating with Templ (Blade Equivalent) 🎨

Traditional template engines parse strings at runtime and rely on reflection to evaluate values, resulting in high CPU usage and dozens of allocations per request.

In GoLaravel, **Templ** serves as the compiled, type-safe replacement for Laravel Blade.

---

## 1. Why Templ?

- **Compiled Go Code**: `.templ` files compile directly to Go functions at build time.
- **Zero Runtime Reflection**: Static strings are written as constant byte slices; dynamic values are HTML-escaped and written directly to the response writer.
- **25.6x Faster**: Benchmark tests show Templ rendering in **44.8 nanoseconds with 0 heap allocations**, compared to 1,147 ns and 13 allocations for standard `html/template`.

---

## 2. Layouts & Components

In Templ, layouts are components that accept `{ children... }`:

```templ
// app/views/layout.templ
package views

templ Layout(title string) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="UTF-8"/>
			<title>{ title }</title>
			<script src="https://cdn.tailwindcss.com"></script>
		</head>
		<body class="bg-slate-900 text-white min-h-screen">
			<nav class="p-4 border-b border-slate-800">
				<a href="/">Home</a>
			</nav>
			<main class="p-8">
				{ children... }
			</main>
		</body>
	</html>
}
```

Views compose layouts just like `@extends` in Blade:

```templ
// app/views/home.templ
package views

templ Home(name string) {
	@Layout("Welcome") {
		<h1 class="text-3xl font-bold">Hello, { name }!</h1>
		<p class="text-slate-400">Rendered via GoLaravel Templ Engine.</p>
	}
}
```

---

## 3. Blade-Equivalent Directives

GoLaravel provides built-in component helpers matching Blade directives:

```templ
import "github.com/golaravel/golaravel/pkg/view"

templ CreateUserForm(csrfToken string) {
	<form action="/users" method="POST">
		<!-- Laravel equivalent of @csrf -->
		@view.CSRF(csrfToken)

		<!-- Laravel equivalent of @method('PUT') -->
		@view.Method("PUT")

		<input type="text" name="name" />
		<button type="submit">Save</button>
	</form>
}
```

---

## 4. Rendering Views from Handlers

Render components directly from any handler using `c.Templ()`:

```go
app.GET("/", func(c *golaravel.Context) error {
	csrf := view.CSRFToken(c)
	return c.Templ(http.StatusOK, views.Home("Taylor"))
})
```

---

## 5. Compiling Views

Use Artisan to compile all `.templ` files across your project:

```bash
artisan generate
```
