# Localization & Translation Guide (pkg/translation) 🌐

GoLaravel provides a complete, high-performance, and type-safe localization (`i18n`) and translation engine inspired by Laravel's `Illuminate\Translation`. It features:

- **JSON Dictionaries**: Both nested group files (`lang/{locale}/{group}.json`) and flat root translation files (`lang/{locale}.json`).
- **Parameter Interpolation**: Laravel-style `:placeholder` syntax with case matching (`:name`, `:Name`, `:NAME`).
- **Pluralization / Choice**: Dynamic singular/plural selection (`apple|apples`), exact counts (`{0}`, `{1}`), and bounded ranges (`[1,5] Few|[6,*] Many`).
- **Embedded & Runtime Loading**: Seamlessly embed files with `//go:embed` via standard `io/fs.FS` or load from disk directories (`os.DirFS`).
- **Automated HTTP Middleware**: Resolves user locale across query strings (`?lang=es`), route parameters (`:lang`), session keys, cookies, and HTTP `Accept-Language` headers (with weighted quality values).
- **Request Context Helpers**: Native `c.Trans()`, `c.TransChoice()`, `c.GetLocale()`, and `c.SetLocale()`.
- **Artisan Generator**: Scaffolds full multi-file language directories via `artisan make:lang <locale>`.

---

## 1. Directory Structure & Translation Files

Language files reside in the `lang/` directory of your project:

```
my-app/
├── lang/
│   ├── en/
│   │   ├── auth.json
│   │   ├── validation.json
│   │   ├── pagination.json
│   │   └── messages.json
│   ├── es/
│   │   ├── auth.json
│   │   ├── validation.json
│   │   ├── pagination.json
│   │   └── messages.json
│   ├── es.json                 # Flat direct string translations
│   └── fr.json
```

### Group JSON (`lang/en/auth.json`)
Keys are prefixed with the file's group name (e.g., `auth.failed`):

```json
{
  "failed": "These credentials do not match our records.",
  "password": "The provided password is incorrect.",
  "throttle": "Too many login attempts. Please try again in :seconds seconds."
}
```

Nested objects automatically flatten to dot-notation (e.g., `messages.nested.title`):

```json
{
  "welcome": "Welcome, :name!",
  "nested": {
    "title": "Application Dashboard"
  }
}
```

### Flat Root JSON (`lang/es.json`)
Enables direct string lookups without group prefixes:

```json
{
  "Welcome back!": "¡Bienvenido de nuevo!",
  "Save": "Guardar",
  "Cancel": "Cancelar"
}
```

---

## 2. Basic Usage & Translation Lookups

### Global Translation Helpers
Use `golaravel.Trans` or `translation.Trans`:

```go
import "github.com/golaravel/golaravel"

// Basic lookup
msg := golaravel.Trans("auth.failed")
// => "These credentials do not match our records."

// Direct root string lookup
save := golaravel.Trans("Save")
// => "Guardar" (if active locale is "es")
```

### Parameter Interpolation
Pass a `map[string]any` to replace `:key` placeholders:

```go
text := golaravel.Trans("messages.welcome", map[string]any{
    "name": "Sarah",
})
// => "Welcome, Sarah!"
```

#### Case Sensitivity
GoLaravel matches the capitalization of your placeholder:

| Placeholder in Dictionary | Replacement Parameter | Output |
|---|---|---|
| `:name` | `"sarah"` | `"sarah"` |
| `:Name` | `"sarah"` | `"Sarah"` |
| `:NAME` | `"sarah"` | `"SARAH"` |

---

## 3. Pluralization & Choice

Use `golaravel.TransChoice` or `c.TransChoice` when translating strings that vary based on quantity.

### Formats Supported

1. **Simple Singular / Plural**:
   ```json
   {
     "apples": "apple|apples"
   }
   ```
   ```go
   golaravel.TransChoice("messages.apples", 1) // => "apple"
   golaravel.TransChoice("messages.apples", 5) // => "apples"
   ```

2. **Explicit Counts & Ranges with `:count` Replacement**:
   ```json
   {
     "cart_items": "{0} Your cart is empty|{1} You have 1 item in your cart|[2,*] You have :count items in your cart"
   }
   ```
   ```go
   golaravel.TransChoice("messages.cart_items", 0)  // => "Your cart is empty"
   golaravel.TransChoice("messages.cart_items", 1)  // => "You have 1 item in your cart"
   golaravel.TransChoice("messages.cart_items", 12) // => "You have 12 items in your cart"
   ```

3. **Bounded Numeric Ranges**:
   ```json
   {
     "queue_status": "[0,5] Short wait|[6,20] Moderate wait|[21,*] Long wait"
   }
   ```

---

## 4. HTTP Context & Templ Views Integration

Inside any HTTP controller or middleware, use the request-scoped context methods:

```go
app.GET("/welcome", func(c *golaravel.Context) error {
    // Current request locale (e.g. "es", "en", "fr")
    currentLocale := c.GetLocale()

    // Context translations automatically use request locale
    greeting := c.Trans("messages.welcome", golaravel.H{"name": "Carlos"})
    items := c.TransChoice("messages.items", 3)

    return c.JSON(http.StatusOK, golaravel.H{
        "locale":   currentLocale,
        "greeting": greeting,
        "items":    items,
    })
})
```

### In Templ Components (SSR)
Templ views compile to pure Go code, allowing direct method calls on `*context.Context`:

```templ
package views

import "github.com/golaravel/golaravel/pkg/context"

templ GreetingView(c *context.Context, name string) {
    <div class="p-6">
        <h1 class="text-2xl font-bold">{ c.Trans("messages.welcome", map[string]any{"name": name}) }</h1>
        <p class="text-gray-600">{ c.TransChoice("messages.cart_items", 4) }</p>
    </div>
}
```

---

## 5. Localization Middleware

The `translation.Middleware` (or `golaravel.Localization`) automatically resolves the preferred locale per request in a prioritized sequence:

```go
app.Use(golaravel.Localization(translation.LocaleConfig{
    DefaultLocale:    "en",
    SupportedLocales: []string{"en", "es", "fr", "ar", "de"},
    ParamName:        "lang",           // Query: ?lang=es OR Route: /:lang/...
    CookieName:       "locale",         // Cookie: locale=es
    PersistCookie:    true,             // Automatically sets Set-Cookie response header
    SessionKey:       "locale",         // Session: session.Get("locale")
    PersistSession:   true,             // Saves in session store
}))
```

### Resolution Order:
1. **URL Query Parameter**: `?lang=es` or `?locale=es`
2. **Route Parameter**: `/users/:lang` or `/:locale/dashboard`
3. **Session Store**: `session.GetString("locale")`
4. **Cookie**: `locale=es`
5. **Accept-Language Header**: Parses RFC 7231 quality values (e.g. `es-ES,es;q=0.9,en;q=0.8`)
6. **Fallback**: Defaults to `DefaultLocale` (`"en"`)

The middleware also automatically sets the `Content-Language` header in the HTTP response.

---

## 6. Embedding Translation Files with Go Embed

In production, embed your `lang/` directory into your compiled binary so no separate filesystem assets are required:

```go
package main

import (
    "embed"
    "github.com/golaravel/golaravel"
    "github.com/golaravel/golaravel/pkg/translation"
)

//go:embed lang/*
var langFS embed.FS

func main() {
    // Load embedded translation files
    if err := translation.Default().LoadFS(langFS, "lang"); err != nil {
        panic(err)
    }

    app := golaravel.Default()
    app.Use(golaravel.Localization(translation.LocaleConfig{
        DefaultLocale:    "en",
        SupportedLocales: []string{"en", "es", "fr"},
    }))

    app.Run(":8080")
}
```

---

## 7. Artisan CLI Scaffolder

Generate complete translation dictionaries for any language with one command:

```bash
# Scaffold Spanish language files
artisan make:lang es

# Scaffold Arabic language files
artisan make:lang ar

# Scaffold French language files
artisan make:lang fr
```

This creates:
- `lang/<locale>/auth.json` (Authentication errors & throttling)
- `lang/<locale>/validation.json` (Form request & validation error messages)
- `lang/<locale>/pagination.json` (Pagination navigation labels)
- `lang/<locale>/messages.json` (Welcome greeting & pluralization examples)
- `lang/<locale>.json` (Direct root key translations)
