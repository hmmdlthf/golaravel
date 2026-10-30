package oauth

import (
	"fmt"

	"github.com/golaravel/golaravel/pkg/context"
)

// GenericOAuth2Provider supports any standard OAuth2 / OpenID Connect server.
type GenericOAuth2Provider struct {
	BaseProvider
}

// NewGenericOAuth2Provider creates a customizable OAuth2 provider.
func NewGenericOAuth2Provider(name string, cfg Config, authURL, tokenURL, userURL string) *GenericOAuth2Provider {
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	base := NewBaseProvider(name, cfg, authURL, tokenURL, userURL)
	return &GenericOAuth2Provider{BaseProvider: base}
}

// WithUserMapper sets a custom function to transform the raw provider JSON into a User.
func (p *GenericOAuth2Provider) WithUserMapper(fn UserMapperFunc) *GenericOAuth2Provider {
	p.userMapper = fn
	return p
}

// User completes the OAuth2 flow from callback and returns the authenticated User.
func (p *GenericOAuth2Provider) User(c *context.Context) (*User, error) {
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
func (p *GenericOAuth2Provider) UserFromToken(token string) (*User, error) {
	raw, err := p.FetchRawUserInfo(token)
	if err != nil {
		return nil, err
	}

	if p.userMapper != nil {
		u, err := p.userMapper(raw, &Token{AccessToken: token})
		if err != nil {
			return nil, err
		}
		u.Provider = p.name
		u.Token = token
		return u, nil
	}

	// Default OpenID Connect claims mapping
	id := ""
	if sub, ok := raw["sub"].(string); ok {
		id = sub
	} else if idNum, ok := raw["id"].(float64); ok {
		id = fmt.Sprintf("%.0f", idNum)
	} else if sid, ok := raw["id"].(string); ok {
		id = sid
	}

	nickname := ""
	if pu, ok := raw["preferred_username"].(string); ok {
		nickname = pu
	} else if un, ok := raw["username"].(string); ok {
		nickname = un
	}

	name := nickname
	if n, ok := raw["name"].(string); ok && n != "" {
		name = n
	}

	email := ""
	if em, ok := raw["email"].(string); ok {
		email = em
	}

	avatar := ""
	if pic, ok := raw["picture"].(string); ok {
		avatar = pic
	} else if av, ok := raw["avatar_url"].(string); ok {
		avatar = av
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
