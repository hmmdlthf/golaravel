# Socialite & OAuth2 Multi-Provider Authentication (pkg/auth/oauth) (PRD §4.2) 🔐

GoLaravel provides a fluent, expressive OAuth2 multi-provider authentication engine modeled after Laravel Socialite, built directly on Go's standard library with zero external dependencies.

---

## 🌟 Key Features

- **Multi-Provider Architecture**: First-class support for **GitHub**, **Google**, and **Discord** out of the box, plus an extensible `GenericOAuth2Provider` for OpenID Connect (OIDC) providers (GitLab, Auth0, Keycloak, Okta, Microsoft Entra, etc.).
- **Normalized User Interface (`oauth.User`)**: Uniform profile data across all providers (`GetID()`, `GetNickname()`, `GetName()`, `GetEmail()`, `GetAvatar()`, `GetToken()`, `GetRefreshToken()`).
- **Seamless Framework Integration**: `oauth.User` implements `auth.Authenticatable` (`GetAuthIdentifier()`), enabling direct handoff to session cookies (`CookieGuard`) or JWT bearer tokens (`BearerGuard`).
- **CSRF & State Security**: Cryptographically secure 128-bit random state generation with automatic storage in encrypted sessions or secure HTTP-only cookies.
- **PKCE Support (RFC 7636)**: Built-in Proof Key for Code Exchange (`WithPKCE()`) with SHA-256 (`S256`) code challenge generation, safeguarding mobile, desktop, Flutter, and SPA clients against authorization code interception attacks.
- **Stateless Mode for Flutter & APIs (`Stateless()`)**: Bypass server-side state cookies for mobile applications and headless REST APIs.
- **Route Mounting Helper**: One-line routing integration with `Socialite.Mount(router, "github", callback)`.
- **Artisan Controller Generator**: Rapidly scaffold turnkey OAuth controllers with `artisan make:oauth <Provider>`.

---

## 🚀 Quickstart Example

### 1. Configure Environment Variables

Set provider credentials in your `.env` or system environment:

```env
GITHUB_CLIENT_ID=your-github-client-id
GITHUB_CLIENT_SECRET=your-github-client-secret
GITHUB_REDIRECT_URL=http://localhost:8080/auth/github/callback

GOOGLE_CLIENT_ID=your-google-client-id
GOOGLE_CLIENT_SECRET=your-google-client-secret
GOOGLE_REDIRECT_URL=http://localhost:8080/auth/google/callback

DISCORD_CLIENT_ID=your-discord-client-id
DISCORD_CLIENT_SECRET=your-discord-client-secret
DISCORD_REDIRECT_URL=http://localhost:8080/auth/discord/callback
```

### 2. Manual Route Handlers

```go
package main

import (
    "net/http"

    "github.com/golaravel/golaravel"
    "github.com/golaravel/golaravel/pkg/auth/oauth"
    "github.com/golaravel/golaravel/pkg/context"
)

func main() {
    app := golaravel.New()

    // 1. Redirect to provider authorization page
    app.Get("/auth/github/redirect", func(c *context.Context) error {
        return oauth.Driver("github").Redirect(c)
    })

    // 2. Handle provider callback
    app.Get("/auth/github/callback", func(c *context.Context) error {
        user, err := oauth.Driver("github").User(c)
        if err != nil {
            return c.JSON(http.StatusBadRequest, map[string]string{
                "error": err.Error(),
            })
        }

        // user.GetID()        -> "1234567"
        // user.GetNickname()  -> "octocat"
        // user.GetName()      -> "The Octocat"
        // user.GetEmail()     -> "octocat@github.com"
        // user.GetAvatar()    -> "https://avatars.githubusercontent.com/u/1234567"
        // user.GetToken()     -> "gho_xxxx..."

        return c.JSON(http.StatusOK, map[string]any{
            "status": "authenticated",
            "user":   user,
        })
    })

    app.Run(":8080")
}
```

### 3. One-Line Mounting Helper

Mount both `/auth/{provider}/redirect` and `/auth/{provider}/callback` automatically:

```go
golaravel.Socialite.Mount(app, "github", func(c *context.Context, user *oauth.User) error {
    // Find or create user in database, then set session or return JWT
    return c.JSON(http.StatusOK, user)
})
```

---

## 📱 Mobile, Flutter & PKCE Support

### Stateless Mode

When handling OAuth on mobile devices (e.g. Flutter using `url_launcher` or in-app browser), server-side session state cookies may not persist across redirect boundaries. Enable stateless mode to authenticate directly via authorization code:

```go
app.Get("/api/auth/google/callback", func(c *context.Context) error {
    user, err := oauth.Driver("google").Stateless().User(c)
    if err != nil {
        return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
    }

    // Issue JWT bearer token for Flutter client
    token, _ := jwtManager.GenerateToken(user.GetID(), map[string]any{
        "email": user.GetEmail(),
        "name":  user.GetName(),
    })

    return c.JSON(http.StatusOK, map[string]string{
        "token": token,
    })
})
```

### PKCE (RFC 7636)

To protect public clients against code interception attacks, activate PKCE:

```go
app.Get("/auth/google/redirect", func(c *context.Context) error {
    return oauth.Driver("google").WithPKCE().Redirect(c)
})
```

---

## ⚙️ Custom Scopes & Additional Parameters

### Overriding Scopes

```go
app.Get("/auth/google/redirect", func(c *context.Context) error {
    return oauth.Driver("google").
        Scopes("openid", "profile", "email", "https://www.googleapis.com/auth/calendar.readonly").
        Redirect(c)
})
```

### Adding Custom Query Parameters (`With`)

Pass extra provider-specific parameters such as `prompt=consent` or `access_type=offline`:

```go
app.Get("/auth/google/redirect", func(c *context.Context) error {
    return oauth.Driver("google").
        With(map[string]string{
            "prompt":      "consent",
            "access_type": "offline",
        }).
        Redirect(c)
})
```

---

## 🌐 Custom & Generic OAuth2 / OpenID Connect Providers

Connect to GitLab, Keycloak, Auth0, Okta, or internal corporate identity providers using `GenericOAuth2Provider`:

```go
import "github.com/golaravel/golaravel/pkg/auth/oauth"

func init() {
    oauth.Register("gitlab", func(cfg oauth.Config) oauth.Provider {
        return oauth.NewGenericOAuth2Provider(
            "gitlab",
            cfg,
            "https://gitlab.com/oauth/authorize",
            "https://gitlab.com/oauth/token",
            "https://gitlab.com/api/v4/user",
        )
    })
}
```

### Custom User Data Mapping

```go
gitlabProvider := oauth.NewGenericOAuth2Provider(
    "gitlab",
    cfg,
    "https://gitlab.com/oauth/authorize",
    "https://gitlab.com/oauth/token",
    "https://gitlab.com/api/v4/user",
).WithUserMapper(func(raw map[string]any, token *oauth.Token) (*oauth.User, error) {
    return &oauth.User{
        ID:       fmt.Sprint(raw["id"]),
        Nickname: fmt.Sprint(raw["username"]),
        Name:     fmt.Sprint(raw["name"]),
        Email:    fmt.Sprint(raw["email"]),
        Avatar:   fmt.Sprint(raw["avatar_url"]),
    }, nil
})
```

---

## 🛠️ Artisan Scaffolding

Scaffold a full-fledged OAuth controller directly using the Artisan CLI:

```bash
artisan make:oauth GitHub
# or
artisan make:oauth Google
```

This generates `app/handlers/github_oauth_controller.go`:

```go
package handlers

import (
    "fmt"
    "net/http"

    "github.com/golaravel/golaravel/pkg/auth/oauth"
    "github.com/golaravel/golaravel/pkg/context"
    "github.com/golaravel/golaravel/pkg/router"
)

type GitHubOAuthController struct{}

func NewGitHubOAuthController() *GitHubOAuthController {
    return &GitHubOAuthController{}
}

func (ctrl *GitHubOAuthController) RegisterRoutes(r router.RouteRegistrar) {
    r.Get("/auth/github/redirect", ctrl.Redirect)
    r.Get("/auth/github/callback", ctrl.Callback)
}

func (ctrl *GitHubOAuthController) Redirect(c *context.Context) error {
    return oauth.Driver("github").Redirect(c)
}

func (ctrl *GitHubOAuthController) Callback(c *context.Context) error {
    socialUser, err := oauth.Driver("github").User(c)
    if err != nil {
        return c.JSON(http.StatusBadRequest, map[string]string{
            "error":   "oauth_error",
            "message": err.Error(),
        })
    }

    // Synchronize or create user in Ent database
    return c.JSON(http.StatusOK, map[string]any{
        "message": "Authenticated via GitHub successfully",
        "user":    socialUser,
    })
}
```
Mount it in `main.go`:
```go
handlers.NewGitHubOAuthController().RegisterRoutes(app)
```
