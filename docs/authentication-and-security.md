# Authentication & Security 🔒 (PRD §4.1)

GoLaravel includes pluggable multi-guard authentication (JWT Bearer tokens, HTTP-only Session cookies, and API Keys), modern cryptographic hashing (Argon2id and Bcrypt), authorization Gates & Policies, RFC 6238 TOTP two-factor authentication (2FA), turnkey auth workflows, and production security middlewares.

---

## 1. Password Hashing (`pkg/hash`)

GoLaravel supports both **Argon2id** and **Bcrypt** with automatic algorithm detection and constant-time verification:

```go
import "github.com/golaravel/golaravel/pkg/hash"

// Hash a password (defaults to Bcrypt with cost 10)
hashed, err := hash.Make("secretPassword123")

// Hash with Argon2id
argonHashed, err := hash.Argon2Make("secretPassword123")

// Verify password against either Bcrypt or Argon2id automatically:
valid := hash.Check("secretPassword123", hashed) // true
```

---

## 2. Multi-Guard Authentication (`pkg/auth`)

GoLaravel supports multiple pluggable authentication strategies that can be used standalone or chained via `MultiGuard`:

| Guard | Transport | Ideal For |
|---|---|---|
| `BearerGuard(jwt)` | `Authorization: Bearer <jwt>` | Flutter (Mobile, Desktop, Web) and Mobile APIs |
| `CookieGuard(jwt, "session_name")` | HTTP-only Cookie | Server-rendered web apps & Micro-SSRs |
| `APIKeyGuard(validator, "X-API-Key")` | Header `X-API-Key` or `Authorization: ApiKey <key>` | Machine-to-machine & Developer APIs |
| `MultiGuard(guards...)` | Evaluates guards in fallback sequence | Unified endpoints supporting Mobile, Web, and CLI |

### MultiGuard Example:
```go
authMiddleware := auth.MultiGuard(
    auth.BearerGuardFunc(jwtManager),
    auth.CookieGuardFunc(jwtManager, "app_session"),
    auth.APIKeyGuardFunc(func(key string) (auth.Authenticatable, error) {
        return client.ApiKey.Query().Where(apikey.KeyEQ(key)).Only(ctx)
    }),
)

api := app.Group("/api", authMiddleware)
```

---

## 3. RFC 6238 TOTP Two-Factor Authentication (2FA)

GoLaravel includes a full RFC 6238 compliant TOTP engine compatible with Google Authenticator, Microsoft Authenticator, 1Password, and Authy.

### Setup and Code Verification:
```go
import "github.com/golaravel/golaravel/pkg/auth"

totp := auth.NewTOTPManager(auth.TOTPOptions{
    Issuer: "MyGoLaravelApp",
    Digits: 6,
    Period: 30 * time.Second,
    SkewWindow: 1, // Allows ±30s clock drift
})

// 1. Generate Base32 Secret Key
secret, _ := totp.GenerateSecret() // e.g. "JBSWY3DPEHPK3PXP"

// 2. Generate QR Code Provisioning URI
uri := totp.ProvisioningURI("alice@example.com", secret)
// Returns: otpauth://totp/MyGoLaravelApp:alice@example.com?secret=...&issuer=MyGoLaravelApp

// 3. Verify 6-digit TOTP code
valid := totp.VerifyCode(secret, "123456")

// 4. Generate Single-Use Emergency Recovery Codes
plainCodes, hashedCodes, err := totp.GenerateRecoveryCodes(8)

// 5. Verify and consume a recovery code (single-use guarantee)
ok, remainingHashedCodes := totp.VerifyRecoveryCode("a1b2c-3d4e5", hashedCodes)
```

---

## 4. Turnkey Auth Workflows & Controller

GoLaravel provides pre-packaged authentication endpoints out of the box (Login, Registration, TOTP 2FA challenge, Password Resets, Email Verification, and 2FA management):

### Scaffolding with Artisan:
```bash
artisan make:auth
```
This scaffolds `app/handlers/auth_workflow_controller.go` connected to your database.

### Mounting Workflow Endpoints:
```go
ctrl := auth.NewWorkflowController(auth.WorkflowConfig{
    SecretKey: "your-32-byte-secret-key-goes-here",
    AppName:   "GoLaravel SaaS",
    AppURL:    "http://localhost:8080",
    Callbacks: auth.WorkflowCallbacks{
        FindByEmail: func(ctx context.Context, email string) (auth.AuthenticatableUser, error) {
            return client.User.Query().Where(user.EmailEQ(email)).Only(ctx)
        },
        FindByID: func(ctx context.Context, id string) (auth.AuthenticatableUser, error) {
            return client.User.Get(ctx, parseID(id))
        },
        CreateUser: func(ctx context.Context, email, pwdHash, name string) (auth.AuthenticatableUser, error) {
            return client.User.Create().SetEmail(email).SetPassword(pwdHash).SetName(name).Save(ctx)
        },
        UpdatePassword: func(ctx context.Context, id, newPwdHash string) error {
            return client.User.UpdateOneID(parseID(id)).SetPassword(newPwdHash).Exec(ctx)
        },
        EnableTwoFactor: func(ctx context.Context, id, secret string, recoveryCodes []string) error {
            return client.User.UpdateOneID(parseID(id)).
                SetTwoFactorSecret(secret).
                SetTwoFactorRecoveryCodes(strings.Join(recoveryCodes, ",")).
                Exec(ctx)
        },
    },
})

// Mount all pre-packaged endpoints:
ctrl.RegisterRoutes(app.Router, "/auth")
```

### Endpoints Generated:
| Endpoint | Method | Description |
|---|---|---|
| `/auth/register` | `POST` | Registers a new user and returns JWT token |
| `/auth/login` | `POST` | Validates credentials; returns `two_factor: true` & challenge token if 2FA enabled |
| `/auth/two-factor-challenge`| `POST` | Verifies TOTP code or Recovery Code; issues final JWT token |
| `/auth/two-factor/enable` | `POST` | Generates 2FA secret, QR code URI, and recovery codes |
| `/auth/two-factor/confirm`| `POST` | Confirms code and activates 2FA on the account |
| `/auth/two-factor/disable`| `POST` | Disables 2FA on the account |
| `/auth/forgot-password` | `POST` | Generates reset token and dispatches reset notification |
| `/auth/reset-password` | `POST` | Validates reset token and updates password |
| `/auth/verify-email` | `GET` | Validates HMAC signature and marks email as verified |

---

## 5. Authorization Gates & Policies

Define fine-grained permission checks using Gates:

```go
// Define a gate
auth.Define("edit-invoice", func(user auth.Authenticatable, args ...any) bool {
	if len(args) == 0 {
		return false
	}
	inv := args[0].(*app.Invoice)
	return inv.OwnerID == user.GetAuthIdentifier()
})

// Check permission inside a handler:
func UpdateInvoice(c *golaravel.Context) error {
	user := auth.User(c)
	if auth.Denies("edit-invoice", user, currentInvoice) {
		return c.String(403, "Forbidden")
	}
	// ...
}
```

Generate a policy using Artisan:
```bash
artisan make:policy InvoicePolicy
```

---

## 6. Built-in Security Middlewares

```go
import "github.com/golaravel/golaravel/pkg/middleware"

// Global recovery from panics (prevents crashes, logs stack trace, returns 500)
app.Use(middleware.Recovery())

// Secure HTTP Headers (HSTS, X-Content-Type-Options: nosniff, X-Frame-Options)
app.Use(middleware.SecureHeaders())

// Cross-Origin Resource Sharing (CORS)
app.Use(middleware.CORS(middleware.DefaultCORS()))

// Double Submit Cookie CSRF verification
app.Use(middleware.CSRF())

// In-Memory Token Bucket Rate Limiting (e.g. 60 requests/minute per IP)
app.Use(middleware.RateLimiter(60))
```

## Password reset tokens across restarts and instances

By default, password reset tokens are kept in memory. A restart, or a second instance, breaks
any reset that's still pending. Store them in a shared cache instead:

```go
store := cache.NewRedisStore(rc) // or cache.NewSQLStore(...)
ctrl := auth.NewWorkflowController(auth.WorkflowConfig{
    SecretKey:   os.Getenv("APP_KEY"),
    ResetTokens: auth.CacheResetTokenStore(store),
})
// Or directly:
broker := auth.NewPasswordResetBrokerWithStore(auth.CacheResetTokenStore(store), time.Hour)
```

Only a hash of each token is stored, and cache keys use a hash of the email address. To use
your own storage, implement `auth.ResetTokenStore`.

## JWT with public/private keys (RS256, ES256, EdDSA)

`auth.NewJWTManager(secret, ttl)` signs with a shared secret (HS256), so every service that checks
tokens can also mint them. Sign with a private key instead, so other services only need the public
key:

```go
priv, _ := auth.ParsePrivateKeyPEM(pemBytes)               // RSA (2048+), P-256 ECDSA or Ed25519
jwt, err := auth.NewJWTManagerWithKey(priv, "2026-06", 24*time.Hour) // "2026-06" = key id (kid)
jwt.Issuer("auth.example.com").Audience("api")

// Publish the public keys for other services:
app.GET("/.well-known/jwks.json", func(c *context.Context) error {
    keys, _ := jwt.JWKS()
    c.SetHeader("Content-Type", "application/json")
    return c.String(200, "%s", keys)
})
```

On a service that only verifies tokens:

```go
verifier, err := auth.NewJWTVerifierFromJWKS(jwksBytes) // or auth.NewJWTVerifier(publicKey, "2026-06")
verifier.Issuer("auth.example.com").Audience("api")
claims, err := verifier.VerifyToken(token) // GenerateToken returns ErrNoSigningKey here
```

Things to know:

- **Choosing the algorithm:** it follows the key type: RSA → RS256, P-256 → ES256, Ed25519 → EdDSA.
  RSA keys smaller than 2048 bits and other elliptic curves are refused.
- **The token never chooses the algorithm.** A key-based manager rejects HS256 tokens (which blocks
  the classic attack of using the public key as an HMAC secret), and a secret manager rejects
  key-signed tokens.
- **Rotation:** sign with the new key and keep accepting the old one with
  `jwt.AcceptKey(oldPublicKey, "2026-01")` until its tokens expire. Tokens carry their key id
  (`kid`), and a token is only checked against the key it names. `JWKS()` lists every accepted key.
- The ES256 signature format is checked against the example in RFC 7515, so tokens work with other
  JWT libraries.
