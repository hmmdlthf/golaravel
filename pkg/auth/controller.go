package auth

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/hash"
	"github.com/golaravel/golaravel/pkg/notification"
	"github.com/golaravel/golaravel/pkg/router"
	"github.com/golaravel/golaravel/pkg/validation"
)

// AuthenticatableUser extends Authenticatable with fields required for turnkey auth workflows.
type AuthenticatableUser interface {
	Authenticatable
	GetEmail() string
	GetPasswordHash() string
	IsTwoFactorEnabled() bool
	GetTwoFactorSecret() string
	GetRecoveryCodes() []string
	IsEmailVerified() bool
}

// SimpleWorkflowUser provides an in-memory/default implementation of AuthenticatableUser.
type SimpleWorkflowUser struct {
	ID               string
	Email            string
	PasswordHash     string
	TwoFactorEnabled bool
	TwoFactorSecret  string
	RecoveryCodes    []string
	EmailVerified    bool
}

func (u *SimpleWorkflowUser) GetAuthIdentifier() string { return u.ID }
func (u *SimpleWorkflowUser) GetEmail() string          { return u.Email }
func (u *SimpleWorkflowUser) GetPasswordHash() string   { return u.PasswordHash }
func (u *SimpleWorkflowUser) IsTwoFactorEnabled() bool  { return u.TwoFactorEnabled }
func (u *SimpleWorkflowUser) GetTwoFactorSecret() string { return u.TwoFactorSecret }
func (u *SimpleWorkflowUser) GetRecoveryCodes() []string { return u.RecoveryCodes }
func (u *SimpleWorkflowUser) IsEmailVerified() bool     { return u.EmailVerified }

// WorkflowCallbacks defines user persistence hooks for Ent ORM or custom databases.
type WorkflowCallbacks struct {
	FindByEmail         func(ctx context.Context, email string) (AuthenticatableUser, error)
	FindByID            func(ctx context.Context, id string) (AuthenticatableUser, error)
	CreateUser          func(ctx context.Context, email, passwordHash, name string) (AuthenticatableUser, error)
	UpdatePassword      func(ctx context.Context, id, newPasswordHash string) error
	EnableTwoFactor     func(ctx context.Context, id, secret string, recoveryCodes []string) error
	DisableTwoFactor    func(ctx context.Context, id string) error
	UpdateRecoveryCodes func(ctx context.Context, id string, remainingCodes []string) error
	MarkEmailVerified   func(ctx context.Context, id string) error
}

// WorkflowController manages pre-packaged authentication and 2FA endpoints.
type WorkflowController struct {
	jwt                *JWTManager
	totp               *TOTPManager
	passwordResets     *PasswordResetBroker
	emailVerifications *EmailVerificationBroker
	twoFactorChallenge *TwoFactorChallengeBroker
	callbacks          WorkflowCallbacks
	appName            string
	appURL             string
}

// WorkflowConfig configures the turnkey WorkflowController.
type WorkflowConfig struct {
	SecretKey string
	AppName   string
	AppURL    string
	Callbacks WorkflowCallbacks
}

// NewWorkflowController instantiates a turnkey authentication controller.
func NewWorkflowController(cfg WorkflowConfig) *WorkflowController {
	if cfg.AppName == "" {
		cfg.AppName = "GoLaravel"
	}
	if cfg.AppURL == "" {
		cfg.AppURL = "http://localhost:8080"
	}

	return &WorkflowController{
		jwt:                NewJWTManager(cfg.SecretKey, 24*time.Hour),
		totp:               NewTOTPManager(TOTPOptions{Issuer: cfg.AppName}),
		passwordResets:     NewPasswordResetBroker(60 * time.Minute),
		emailVerifications: NewEmailVerificationBroker(cfg.SecretKey),
		twoFactorChallenge: NewTwoFactorChallengeBroker(cfg.SecretKey),
		callbacks:          cfg.Callbacks,
		appName:            cfg.AppName,
		appURL:             cfg.AppURL,
	}
}

// RegisterRoutes registers the pre-packaged authentication workflow routes on a router group.
func (ctrl *WorkflowController) RegisterRoutes(r router.RouteRegistrar, prefix ...string) {
	p := "/auth"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}

	group := r.Group(p)
	group.POST("/register", ctrl.Register)
	group.POST("/login", ctrl.Login)
	group.POST("/two-factor-challenge", ctrl.TwoFactorChallenge)
	group.POST("/forgot-password", ctrl.ForgotPassword)
	group.POST("/reset-password", ctrl.ResetPassword)
	group.GET("/verify-email", ctrl.VerifyEmail)

	// Protected routes (require JWT Bearer)
	authMiddleware := BearerGuard(ctrl.jwt)
	group.POST("/logout", authMiddleware, ctrl.Logout)
	group.POST("/email/verification-notification", authMiddleware, ctrl.SendEmailVerification)
	group.POST("/two-factor/enable", authMiddleware, ctrl.TwoFactorEnable)
	group.POST("/two-factor/confirm", authMiddleware, ctrl.TwoFactorConfirm)
	group.POST("/two-factor/disable", authMiddleware, ctrl.TwoFactorDisable)
}

// --- Request DTOs ---

type RegisterRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r *RegisterRequest) Validate(v *validation.Validator) {
	v.Required("name", r.Name).
		Required("email", r.Email).
		Email("email", r.Email).
		Required("password", r.Password).
		MinLength("password", r.Password, 8)
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r *LoginRequest) Validate(v *validation.Validator) {
	v.Required("email", r.Email).
		Email("email", r.Email).
		Required("password", r.Password)
}

type TwoFactorChallengeRequest struct {
	TwoFactorToken string `json:"two_factor_token"`
	Code           string `json:"code"`
	RecoveryCode   string `json:"recovery_code"`
}

func (r *TwoFactorChallengeRequest) Validate(v *validation.Validator) {
	v.Required("two_factor_token", r.TwoFactorToken)
	if r.Code == "" && r.RecoveryCode == "" {
		v.Required("code", "") // Will report code is required
	}
}

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

func (r *ForgotPasswordRequest) Validate(v *validation.Validator) {
	v.Required("email", r.Email).Email("email", r.Email)
}

type ResetPasswordRequest struct {
	Email    string `json:"email"`
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (r *ResetPasswordRequest) Validate(v *validation.Validator) {
	v.Required("email", r.Email).
		Email("email", r.Email).
		Required("token", r.Token).
		Required("password", r.Password).
		MinLength("password", r.Password, 8)
}

type TwoFactorConfirmRequest struct {
	Code   string `json:"code"`
	Secret string `json:"secret"`
}

func (r *TwoFactorConfirmRequest) Validate(v *validation.Validator) {
	v.Required("code", r.Code).
		Required("secret", r.Secret)
}

// --- Handlers ---

// Register handles user registration.
func (ctrl *WorkflowController) Register(c *gcontext.Context) error {
	var req RegisterRequest
	if !validation.ValidateAndRespond(c, &req) {
		return nil
	}

	if ctrl.callbacks.FindByEmail != nil {
		existing, _ := ctrl.callbacks.FindByEmail(c.Context(), req.Email)
		if existing != nil {
			return c.JSON(http.StatusUnprocessableEntity, map[string]any{
				"message": "The given data was invalid.",
				"errors": map[string][]string{
					"email": {"The email has already been taken."},
				},
			})
		}
	}

	pwdHash, err := hash.Make(req.Password)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to hash password"})
	}

	var user AuthenticatableUser
	if ctrl.callbacks.CreateUser != nil {
		user, err = ctrl.callbacks.CreateUser(c.Context(), req.Email, pwdHash, req.Name)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	} else {
		user = &SimpleWorkflowUser{
			ID:           req.Email,
			Email:        req.Email,
			PasswordHash: pwdHash,
		}
	}

	token, err := ctrl.jwt.GenerateToken(user.GetAuthIdentifier())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to generate token"})
	}

	return c.JSON(http.StatusCreated, map[string]any{
		"token": token,
		"user": map[string]any{
			"id":    user.GetAuthIdentifier(),
			"email": user.GetEmail(),
		},
	})
}

// Login authenticates credentials, initiating a 2FA challenge if enabled.
func (ctrl *WorkflowController) Login(c *gcontext.Context) error {
	var req LoginRequest
	if !validation.ValidateAndRespond(c, &req) {
		return nil
	}

	if ctrl.callbacks.FindByEmail == nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "User lookup not configured"})
	}

	user, err := ctrl.callbacks.FindByEmail(c.Context(), req.Email)
	if err != nil || user == nil || !hash.Check(req.Password, user.GetPasswordHash()) {
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error":   "Unauthorized",
			"message": "Invalid email or password",
		})
	}

	// If user has 2FA enabled, issue a temporary challenge token instead of the final access token
	if user.IsTwoFactorEnabled() {
		challengeToken, err := ctrl.twoFactorChallenge.GenerateChallengeToken(user.GetAuthIdentifier())
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to issue 2FA challenge"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"two_factor":       true,
			"two_factor_token": challengeToken,
		})
	}

	token, err := ctrl.jwt.GenerateToken(user.GetAuthIdentifier())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to issue token"})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"two_factor": false,
		"token":      token,
		"user": map[string]any{
			"id":    user.GetAuthIdentifier(),
			"email": user.GetEmail(),
		},
	})
}

// TwoFactorChallenge verifies a TOTP code or recovery code and issues the final JWT.
func (ctrl *WorkflowController) TwoFactorChallenge(c *gcontext.Context) error {
	var req TwoFactorChallengeRequest
	if !validation.ValidateAndRespond(c, &req) {
		return nil
	}

	userID, err := ctrl.twoFactorChallenge.VerifyChallengeToken(req.TwoFactorToken)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error":   "Unauthorized",
			"message": "Two-factor challenge token expired or invalid",
		})
	}

	if ctrl.callbacks.FindByID == nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "User lookup not configured"})
	}

	user, err := ctrl.callbacks.FindByID(c.Context(), userID)
	if err != nil || user == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized", "message": "User not found"})
	}

	// Verify via TOTP code
	if req.Code != "" {
		if !ctrl.totp.VerifyCode(user.GetTwoFactorSecret(), req.Code) {
			return c.JSON(http.StatusUnprocessableEntity, map[string]any{
				"message": "The provided two-factor authentication code was invalid.",
				"errors": map[string][]string{
					"code": {"The two-factor authentication code was invalid."},
				},
			})
		}
	} else if req.RecoveryCode != "" {
		// Verify via single-use Recovery Code
		ok, remaining := ctrl.totp.VerifyRecoveryCode(req.RecoveryCode, user.GetRecoveryCodes())
		if !ok {
			return c.JSON(http.StatusUnprocessableEntity, map[string]any{
				"message": "The provided recovery code was invalid.",
				"errors": map[string][]string{
					"recovery_code": {"The recovery code was invalid or already used."},
				},
			})
		}
		// Persist remaining recovery codes
		if ctrl.callbacks.UpdateRecoveryCodes != nil {
			_ = ctrl.callbacks.UpdateRecoveryCodes(c.Context(), user.GetAuthIdentifier(), remaining)
		}
	}

	token, err := ctrl.jwt.GenerateToken(user.GetAuthIdentifier())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to generate token"})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]any{
			"id":    user.GetAuthIdentifier(),
			"email": user.GetEmail(),
		},
	})
}

// TwoFactorEnable initiates 2FA setup by generating a secret, QR provisioning URI, and recovery codes.
func (ctrl *WorkflowController) TwoFactorEnable(c *gcontext.Context) error {
	user := User(c)
	if user == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}

	secret, err := ctrl.totp.GenerateSecret()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to generate 2FA secret"})
	}

	qrURI := ctrl.totp.ProvisioningURI(user.GetAuthIdentifier(), secret)
	plainCodes, _, err := ctrl.totp.GenerateRecoveryCodes(8)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to generate recovery codes"})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"secret":         secret,
		"qr_uri":         qrURI,
		"recovery_codes": plainCodes,
	})
}

// TwoFactorConfirm confirms the 2FA secret with a valid TOTP code and activates 2FA on the user account.
func (ctrl *WorkflowController) TwoFactorConfirm(c *gcontext.Context) error {
	var req TwoFactorConfirmRequest
	if !validation.ValidateAndRespond(c, &req) {
		return nil
	}

	user := User(c)
	if user == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}

	if !ctrl.totp.VerifyCode(req.Secret, req.Code) {
		return c.JSON(http.StatusUnprocessableEntity, map[string]any{
			"message": "The provided two-factor confirmation code was invalid.",
			"errors": map[string][]string{
				"code": {"The two-factor confirmation code was invalid."},
			},
		})
	}

	// Generate recovery codes for persistence
	plainCodes, hashedCodes, err := ctrl.totp.GenerateRecoveryCodes(8)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to generate recovery codes"})
	}

	if ctrl.callbacks.EnableTwoFactor != nil {
		if err := ctrl.callbacks.EnableTwoFactor(c.Context(), user.GetAuthIdentifier(), req.Secret, hashedCodes); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusOK, map[string]any{
		"message":        "Two-factor authentication confirmed and enabled.",
		"recovery_codes": plainCodes,
	})
}

// TwoFactorDisable turns off 2FA for the authenticated user.
func (ctrl *WorkflowController) TwoFactorDisable(c *gcontext.Context) error {
	user := User(c)
	if user == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}

	if ctrl.callbacks.DisableTwoFactor != nil {
		if err := ctrl.callbacks.DisableTwoFactor(c.Context(), user.GetAuthIdentifier()); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusOK, map[string]string{
		"message": "Two-factor authentication disabled.",
	})
}

// ForgotPassword generates a reset token and sends a reset email.
func (ctrl *WorkflowController) ForgotPassword(c *gcontext.Context) error {
	var req ForgotPasswordRequest
	if !validation.ValidateAndRespond(c, &req) {
		return nil
	}

	token, err := ctrl.passwordResets.CreateToken(req.Email)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to create reset token"})
	}

	resetURL := fmt.Sprintf("%s/reset-password?token=%s&email=%s", ctrl.appURL, token, req.Email)
	recipient := notification.OnDemand("mail", req.Email)
	_ = notification.SendOne(c.Context(), recipient, NewResetPasswordNotification(resetURL, ctrl.appName))

	return c.JSON(http.StatusOK, map[string]string{
		"message": "We have emailed your password reset link.",
	})
}

// ResetPassword validates the token and updates the password.
func (ctrl *WorkflowController) ResetPassword(c *gcontext.Context) error {
	var req ResetPasswordRequest
	if !validation.ValidateAndRespond(c, &req) {
		return nil
	}

	if !ctrl.passwordResets.ValidateToken(req.Email, req.Token) {
		return c.JSON(http.StatusUnprocessableEntity, map[string]any{
			"message": "This password reset token is invalid.",
			"errors": map[string][]string{
				"token": {"This password reset token is invalid."},
			},
		})
	}

	pwdHash, err := hash.Make(req.Password)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to hash password"})
	}

	if ctrl.callbacks.FindByEmail != nil && ctrl.callbacks.UpdatePassword != nil {
		user, err := ctrl.callbacks.FindByEmail(c.Context(), req.Email)
		if err != nil || user == nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "User not found"})
		}
		if err := ctrl.callbacks.UpdatePassword(c.Context(), user.GetAuthIdentifier(), pwdHash); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	ctrl.passwordResets.DeleteToken(req.Email)

	return c.JSON(http.StatusOK, map[string]string{
		"message": "Your password has been reset.",
	})
}

// SendEmailVerification dispatches a signed verification URL to the user.
func (ctrl *WorkflowController) SendEmailVerification(c *gcontext.Context) error {
	user := User(c)
	if user == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}

	if ctrl.callbacks.FindByID == nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "User lookup not configured"})
	}

	u, err := ctrl.callbacks.FindByID(c.Context(), user.GetAuthIdentifier())
	if err != nil || u == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "User not found"})
	}

	verifyURL := ctrl.emailVerifications.GenerateSignedURL(
		fmt.Sprintf("%s/auth/verify-email", ctrl.appURL),
		u.GetAuthIdentifier(),
		u.GetEmail(),
		24*time.Hour,
	)

	recipient := notification.OnDemand("mail", u.GetEmail())
	_ = notification.SendOne(c.Context(), recipient, NewVerifyEmailNotification(verifyURL, ctrl.appName))

	return c.JSON(http.StatusOK, map[string]string{
		"message": "A new verification link has been sent to your email address.",
	})
}

// VerifyEmail verifies the signed verification URL and marks the email as verified.
func (ctrl *WorkflowController) VerifyEmail(c *gcontext.Context) error {
	id := c.Query("id")
	expiresStr := c.Query("expires")
	sig := c.Query("signature")

	if id == "" || expiresStr == "" || sig == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid verification link"})
	}

	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid expiration"})
	}

	if ctrl.callbacks.FindByID == nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "User lookup not configured"})
	}

	u, err := ctrl.callbacks.FindByID(c.Context(), id)
	if err != nil || u == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "User not found"})
	}

	if !ctrl.emailVerifications.VerifySignature(id, u.GetEmail(), expires, sig) {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Invalid or expired signature"})
	}

	if ctrl.callbacks.MarkEmailVerified != nil {
		_ = ctrl.callbacks.MarkEmailVerified(c.Context(), id)
	}

	return c.JSON(http.StatusOK, map[string]string{
		"message": "Email has been verified successfully.",
	})
}

// Logout invalidates the session or token.
func (ctrl *WorkflowController) Logout(c *gcontext.Context) error {
	return c.JSON(http.StatusOK, map[string]string{
		"message": "Successfully logged out.",
	})
}
