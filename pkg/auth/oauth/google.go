package oauth

import (
	"github.com/golaravel/golaravel/pkg/context"
)

// GoogleProvider implements OAuth2 authentication for Google.
type GoogleProvider struct {
	BaseProvider
}

// NewGoogleProvider creates a new Google OAuth2 provider.
func NewGoogleProvider(cfg Config) *GoogleProvider {
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	base := NewBaseProvider(
		"google",
		cfg,
		"https://accounts.google.com/o/oauth2/v2/auth",
		"https://oauth2.googleapis.com/token",
		"https://openidconnect.googleapis.com/v1/userinfo",
	)
	return &GoogleProvider{BaseProvider: base}
}

// User completes the OAuth2 flow from callback and returns the authenticated User.
func (p *GoogleProvider) User(c *context.Context) (*User, error) {
	code, err := p.ValidateCallback(c)
	if err != nil {
		return nil, err
	}

	token, err := p.ExchangeCode(c, code)
	if err != nil {
		return nil, err
	}

	user, err := p.UserFromToken(token.AccessToken)
	if err != nil {
		return nil, err
	}

	user.RefreshToken = token.RefreshToken
	user.ExpiresIn = token.ExpiresIn
	return user, nil
}

// UserFromToken retrieves user profile using an existing access token.
func (p *GoogleProvider) UserFromToken(token string) (*User, error) {
	raw, err := p.FetchRawUserInfo(token)
	if err != nil {
		return nil, err
	}

	id := ""
	if sub, ok := raw["sub"].(string); ok {
		id = sub
	}

	name := ""
	if n, ok := raw["name"].(string); ok {
		name = n
	}

	nickname := ""
	if gn, ok := raw["given_name"].(string); ok {
		nickname = gn
	} else {
		nickname = name
	}

	email := ""
	if em, ok := raw["email"].(string); ok {
		email = em
	}

	avatar := ""
	if pic, ok := raw["picture"].(string); ok {
		avatar = pic
	}

	return &User{
		ID:       id,
		Nickname: nickname,
		Name:     name,
		Email:    email,
		Avatar:   avatar,
		Token:    token,
		Raw:      raw,
		Provider: p.name,
	}, nil
}
