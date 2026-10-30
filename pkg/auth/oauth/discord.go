package oauth

import (
	"fmt"

	"github.com/golaravel/golaravel/pkg/context"
)

// DiscordProvider implements OAuth2 authentication for Discord.
type DiscordProvider struct {
	BaseProvider
}

// NewDiscordProvider creates a new Discord OAuth2 provider.
func NewDiscordProvider(cfg Config) *DiscordProvider {
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"identify", "email"}
	}
	base := NewBaseProvider(
		"discord",
		cfg,
		"https://discord.com/api/oauth2/authorize",
		"https://discord.com/api/oauth2/token",
		"https://discord.com/api/users/@me",
	)
	return &DiscordProvider{BaseProvider: base}
}

// User completes the OAuth2 flow from callback and returns the authenticated User.
func (p *DiscordProvider) User(c *context.Context) (*User, error) {
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
func (p *DiscordProvider) UserFromToken(token string) (*User, error) {
	raw, err := p.FetchRawUserInfo(token)
	if err != nil {
		return nil, err
	}

	id := ""
	if sid, ok := raw["id"].(string); ok {
		id = sid
	}

	username := ""
	if un, ok := raw["username"].(string); ok {
		username = un
	}

	name := username
	if gn, ok := raw["global_name"].(string); ok && gn != "" {
		name = gn
	}

	email := ""
	if em, ok := raw["email"].(string); ok {
		email = em
	}

	avatar := ""
	if avHash, ok := raw["avatar"].(string); ok && avHash != "" && id != "" {
		avatar = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png", id, avHash)
	}

	return &User{
		ID:       id,
		Nickname: username,
		Name:     name,
		Email:    email,
		Avatar:   avatar,
		Token:    token,
		Raw:      raw,
		Provider: p.name,
	}, nil
}
