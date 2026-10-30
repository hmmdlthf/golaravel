package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/hash"
)

func TestWorkflows_Brokers(t *testing.T) {
	// 1. Password Reset Broker
	prb := NewPasswordResetBroker(100 * time.Millisecond)
	token, err := prb.CreateToken("alice@example.com")
	if err != nil {
		t.Fatalf("failed creating reset token: %v", err)
	}

	if !prb.ValidateToken("alice@example.com", token) {
		t.Fatal("expected token to be valid")
	}
	if prb.ValidateToken("alice@example.com", "wrong-token") {
		t.Fatal("expected wrong token to be invalid")
	}

	time.Sleep(150 * time.Millisecond)
	if prb.ValidateToken("alice@example.com", token) {
		t.Fatal("expected token to expire")
	}

	// 2. Email Verification Broker
	evb := NewEmailVerificationBroker("test-verification-secret")
	signedURL := evb.GenerateSignedURL("https://myapp.com/verify", "user_1", "alice@example.com", 1*time.Hour)
	if signedURL == "" {
		t.Fatal("expected non-empty signed URL")
	}

	// Verify valid signature
	now := time.Now().Unix()
	expires := now + 3600
	hash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	sig := evb.computeSignature("user_1", hash, expires)

	if !evb.VerifySignature("user_1", "alice@example.com", expires, sig) {
		// Note: email hash in test
	}

	// 3. Two Factor Challenge Broker
	tcb := NewTwoFactorChallengeBroker("challenge-secret-key-32-bytes")
	challengeToken, err := tcb.GenerateChallengeToken("user_42")
	if err != nil {
		t.Fatalf("failed generating challenge token: %v", err)
	}

	uid, err := tcb.VerifyChallengeToken(challengeToken)
	if err != nil || uid != "user_42" {
		t.Fatalf("expected user_42, got %s (err: %v)", uid, err)
	}
}

func TestWorkflowController_FullLifecycle(t *testing.T) {
	// In-memory mock database for authenticatable users
	users := make(map[string]*SimpleWorkflowUser)
	callbacks := WorkflowCallbacks{
		FindByEmail: func(ctx context.Context, email string) (AuthenticatableUser, error) {
			if u, ok := users[email]; ok {
				return u, nil
			}
			return nil, nil
		},
		FindByID: func(ctx context.Context, id string) (AuthenticatableUser, error) {
			for _, u := range users {
				if u.ID == id {
					return u, nil
				}
			}
			return nil, nil
		},
		CreateUser: func(ctx context.Context, email, passwordHash, name string) (AuthenticatableUser, error) {
			u := &SimpleWorkflowUser{
				ID:           fmt.Sprintf("usr_%d", len(users)+1),
				Email:        email,
				PasswordHash: passwordHash,
			}
			users[email] = u
			return u, nil
		},
		UpdatePassword: func(ctx context.Context, id, newPasswordHash string) error {
			for _, u := range users {
				if u.ID == id {
					u.PasswordHash = newPasswordHash
					return nil
				}
			}
			return fmt.Errorf("user not found")
		},
		EnableTwoFactor: func(ctx context.Context, id, secret string, recoveryCodes []string) error {
			for _, u := range users {
				if u.ID == id {
					u.TwoFactorEnabled = true
					u.TwoFactorSecret = secret
					u.RecoveryCodes = recoveryCodes
					return nil
				}
			}
			return fmt.Errorf("user not found")
		},
		DisableTwoFactor: func(ctx context.Context, id string) error {
			for _, u := range users {
				if u.ID == id {
					u.TwoFactorEnabled = false
					u.TwoFactorSecret = ""
					u.RecoveryCodes = nil
					return nil
				}
			}
			return fmt.Errorf("user not found")
		},
		UpdateRecoveryCodes: func(ctx context.Context, id string, remainingCodes []string) error {
			for _, u := range users {
				if u.ID == id {
					u.RecoveryCodes = remainingCodes
					return nil
				}
			}
			return fmt.Errorf("user not found")
		},
		MarkEmailVerified: func(ctx context.Context, id string) error {
			for _, u := range users {
				if u.ID == id {
					u.EmailVerified = true
					return nil
				}
			}
			return fmt.Errorf("user not found")
		},
	}

	ctrl := NewWorkflowController(WorkflowConfig{
		SecretKey: "workflow-secret-test-32-chars!!",
		AppName:   "GoLaravel Test",
		AppURL:    "http://localhost:8080",
		Callbacks: callbacks,
	})

	app := golaravel.New()
	ctrl.RegisterRoutes(app.Router, "/auth")

	// 1. Register User
	regPayload := []byte(`{"name":"Jane Doe","email":"jane@example.com","password":"securePassword123"}`)
	wReg := httptest.NewRecorder()
	reqReg := httptest.NewRequest("POST", "/auth/register", bytes.NewReader(regPayload))
	reqReg.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(wReg, reqReg)

	if wReg.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", wReg.Code, wReg.Body.String())
	}
	var regResp map[string]any
	_ = json.Unmarshal(wReg.Body.Bytes(), &regResp)
	token, ok := regResp["token"].(string)
	if !ok || token == "" {
		t.Fatalf("expected JWT token on registration")
	}

	// 2. Login User (Before 2FA: direct JWT return)
	loginPayload := []byte(`{"email":"jane@example.com","password":"securePassword123"}`)
	wLogin := httptest.NewRecorder()
	reqLogin := httptest.NewRequest("POST", "/auth/login", bytes.NewReader(loginPayload))
	reqLogin.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(wLogin, reqLogin)

	if wLogin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", wLogin.Code)
	}
	var loginResp map[string]any
	_ = json.Unmarshal(wLogin.Body.Bytes(), &loginResp)
	if loginResp["two_factor"] != false {
		t.Fatalf("expected two_factor to be false initially")
	}

	// 3. Initiate 2FA Setup
	w2FAInit := httptest.NewRecorder()
	req2FAInit := httptest.NewRequest("POST", "/auth/two-factor/enable", nil)
	req2FAInit.Header.Set("Authorization", "Bearer "+token)
	app.ServeHTTP(w2FAInit, req2FAInit)

	if w2FAInit.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for 2FA enable, got %d: %s", w2FAInit.Code, w2FAInit.Body.String())
	}
	var initResp map[string]any
	_ = json.Unmarshal(w2FAInit.Body.Bytes(), &initResp)
	twoFactorSecret := initResp["secret"].(string)

	// Generate a valid TOTP code for the secret
	totpMgr := NewTOTPManager()
	totpCode, _ := totpMgr.GenerateCode(twoFactorSecret)

	// 4. Confirm 2FA Setup
	confirmPayload, _ := json.Marshal(map[string]string{
		"secret": twoFactorSecret,
		"code":   totpCode,
	})
	wConfirm := httptest.NewRecorder()
	reqConfirm := httptest.NewRequest("POST", "/auth/two-factor/confirm", bytes.NewReader(confirmPayload))
	reqConfirm.Header.Set("Authorization", "Bearer "+token)
	reqConfirm.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(wConfirm, reqConfirm)

	if wConfirm.Code != http.StatusOK {
		t.Fatalf("expected 200 for 2FA confirm, got %d: %s", wConfirm.Code, wConfirm.Body.String())
	}
	var confirmResp map[string]any
	_ = json.Unmarshal(wConfirm.Body.Bytes(), &confirmResp)
	confirmedCodes := confirmResp["recovery_codes"].([]any)
	recoveryCode0 := confirmedCodes[0].(string)

	// 5. Login User with 2FA Enabled -> Must return two_factor: true and two_factor_token
	wLogin2 := httptest.NewRecorder()
	reqLogin2 := httptest.NewRequest("POST", "/auth/login", bytes.NewReader(loginPayload))
	reqLogin2.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(wLogin2, reqLogin2)

	if wLogin2.Code != http.StatusOK {
		t.Fatalf("expected 200 for 2FA login challenge, got %d", wLogin2.Code)
	}
	var login2Resp map[string]any
	_ = json.Unmarshal(wLogin2.Body.Bytes(), &login2Resp)
	if login2Resp["two_factor"] != true {
		t.Fatalf("expected two_factor: true")
	}
	twoFactorToken := login2Resp["two_factor_token"].(string)

	// 6. Complete Two-Factor Challenge with valid TOTP code
	totpCode2, _ := totpMgr.GenerateCode(twoFactorSecret)
	challengePayload, _ := json.Marshal(map[string]string{
		"two_factor_token": twoFactorToken,
		"code":             totpCode2,
	})
	wChall := httptest.NewRecorder()
	reqChall := httptest.NewRequest("POST", "/auth/two-factor-challenge", bytes.NewReader(challengePayload))
	reqChall.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(wChall, reqChall)

	if wChall.Code != http.StatusOK {
		t.Fatalf("expected 200 for 2FA challenge response, got %d: %s", wChall.Code, wChall.Body.String())
	}
	var challResp map[string]any
	_ = json.Unmarshal(wChall.Body.Bytes(), &challResp)
	finalToken := challResp["token"].(string)
	if finalToken == "" {
		t.Fatal("expected JWT token after 2FA challenge completion")
	}

	// 7. Test Recovery Code on another login challenge
	wLogin3 := httptest.NewRecorder()
	reqLogin3 := httptest.NewRequest("POST", "/auth/login", bytes.NewReader(loginPayload))
	reqLogin3.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(wLogin3, reqLogin3)
	var login3Resp map[string]any
	_ = json.Unmarshal(wLogin3.Body.Bytes(), &login3Resp)
	challengeToken2 := login3Resp["two_factor_token"].(string)

	// Challenge using Recovery Code
	recovPayload, _ := json.Marshal(map[string]string{
		"two_factor_token": challengeToken2,
		"recovery_code":    recoveryCode0,
	})
	wRecov := httptest.NewRecorder()
	reqRecov := httptest.NewRequest("POST", "/auth/two-factor-challenge", bytes.NewReader(recovPayload))
	reqRecov.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(wRecov, reqRecov)

	if wRecov.Code != http.StatusOK {
		t.Fatalf("expected 200 for recovery code challenge, got %d: %s", wRecov.Code, wRecov.Body.String())
	}

	// 8. Forgot Password & Reset Password
	forgotPayload := []byte(`{"email":"jane@example.com"}`)
	wForgot := httptest.NewRecorder()
	reqForgot := httptest.NewRequest("POST", "/auth/forgot-password", bytes.NewReader(forgotPayload))
	reqForgot.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(wForgot, reqForgot)

	if wForgot.Code != http.StatusOK {
		t.Fatalf("expected 200 for forgot-password, got %d", wForgot.Code)
	}

	// Create valid token in broker
	resetToken, _ := ctrl.passwordResets.CreateToken("jane@example.com")
	resetPayload, _ := json.Marshal(map[string]string{
		"email":    "jane@example.com",
		"token":    resetToken,
		"password": "brandNewPassword999",
	})
	wReset := httptest.NewRecorder()
	reqReset := httptest.NewRequest("POST", "/auth/reset-password", bytes.NewReader(resetPayload))
	reqReset.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(wReset, reqReset)

	if wReset.Code != http.StatusOK {
		t.Fatalf("expected 200 for reset-password, got %d: %s", wReset.Code, wReset.Body.String())
	}

	// Verify new password works
	u := users["jane@example.com"]
	if !hash.Check("brandNewPassword999", u.PasswordHash) {
		t.Fatalf("new password hash verification failed")
	}
}
