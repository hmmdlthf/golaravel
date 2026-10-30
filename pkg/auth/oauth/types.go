package oauth

import (
	"errors"
)

// Common OAuth errors
var (
	ErrInvalidState     = errors.New("oauth: invalid or expired state parameter (possible CSRF attack)")
	ErrMissingCode      = errors.New("oauth: missing authorization code in callback request")
	ErrTokenExchange    = errors.New("oauth: failed to exchange authorization code for token")
	ErrUserInfo         = errors.New("oauth: failed to retrieve user profile from provider")
	ErrProviderNotFound = errors.New("oauth: requested provider driver is not registered")
	ErrMissingVerifier  = errors.New("oauth: missing PKCE code verifier for code exchange")
)

// Config defines the OAuth2 client credentials and endpoints.
type Config struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RedirectURL  string   `json:"redirect_url"`
	Scopes       []string `json:"scopes,omitempty"`
}

// Token represents an OAuth2 access and refresh token response.
type Token struct {
	AccessToken  string         `json:"access_token"`
	TokenType    string         `json:"token_type"`
	RefreshToken string         `json:"refresh_token,omitempty"`
	ExpiresIn    int64          `json:"expires_in,omitempty"`
	Scope        string         `json:"scope,omitempty"`
	Raw          map[string]any `json:"raw,omitempty"`
}

// User represents a normalized Socialite user profile across providers.
type User struct {
	ID           string         `json:"id"`
	Nickname     string         `json:"nickname"`
	Name         string         `json:"name"`
	Email        string         `json:"email"`
	Avatar       string         `json:"avatar"`
	Token        string         `json:"token"`
	RefreshToken string         `json:"refresh_token,omitempty"`
	ExpiresIn    int64          `json:"expires_in,omitempty"`
	Raw          map[string]any `json:"raw"`
	Provider     string         `json:"provider"`
}

// GetAuthIdentifier implements auth.Authenticatable.
func (u *User) GetAuthIdentifier() string {
	return u.ID
}

// GetID returns the provider's unique user ID.
func (u *User) GetID() string {
	return u.ID
}

// GetNickname returns the user's handle or username.
func (u *User) GetNickname() string {
	return u.Nickname
}

// GetName returns the user's full name.
func (u *User) GetName() string {
	return u.Name
}

// GetEmail returns the user's primary email address.
func (u *User) GetEmail() string {
	return u.Email
}

// GetAvatar returns the user's avatar/profile image URL.
func (u *User) GetAvatar() string {
	return u.Avatar
}

// GetToken returns the current OAuth access token.
func (u *User) GetToken() string {
	return u.Token
}

// GetRefreshToken returns the OAuth refresh token if provided.
func (u *User) GetRefreshToken() string {
	return u.RefreshToken
}
