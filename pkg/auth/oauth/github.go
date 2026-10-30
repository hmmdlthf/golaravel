package oauth

import (
	"fmt"
	"io"
	"net/http"

	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/context"
)

// GitHubProvider implements OAuth2 authentication for GitHub.
type GitHubProvider struct {
	BaseProvider
	emailsURL string
}

// NewGitHubProvider creates a new GitHub OAuth2 provider.
func NewGitHubProvider(cfg Config) *GitHubProvider {
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"read:user", "user:email"}
	}
	base := NewBaseProvider(
		"github",
		cfg,
		"https://github.com/login/oauth/authorize",
		"https://github.com/login/oauth/access_token",
		"https://api.github.com/user",
	)
	return &GitHubProvider{
		BaseProvider: base,
		emailsURL:    "https://api.github.com/user/emails",
	}
}

// User completes the OAuth2 flow from callback and returns the authenticated User.
func (p *GitHubProvider) User(c *context.Context) (*User, error) {
	code, err := p.ValidateCallback(c)
	if err != nil {
		return nil, err
	}

	token, err := p.ExchangeCode(c, code)
	if err != nil {
		return nil, err
	}

	return p.UserFromToken(token.AccessToken)
}

// UserFromToken retrieves user profile using an existing access token.
func (p *GitHubProvider) UserFromToken(token string) (*User, error) {
	raw, err := p.FetchRawUserInfo(token)
	if err != nil {
		return nil, err
	}

	user := p.mapUser(raw, token)

	// GitHub user profile email can be private; fetch from emails endpoint if missing
	if user.Email == "" {
		if email, err := p.fetchPrimaryEmail(token); err == nil && email != "" {
			user.Email = email
		}
	}

	return user, nil
}

func (p *GitHubProvider) mapUser(raw map[string]any, token string) *User {
	idStr := ""
	if idNum, ok := raw["id"].(float64); ok {
		idStr = fmt.Sprintf("%.0f", idNum)
	} else if id, ok := raw["id"].(string); ok {
		idStr = id
	}

	nickname := ""
	if login, ok := raw["login"].(string); ok {
		nickname = login
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
	if av, ok := raw["avatar_url"].(string); ok {
		avatar = av
	}

	return &User{
		ID:       idStr,
		Nickname: nickname,
		Name:     name,
		Email:    email,
		Avatar:   avatar,
		Token:    token,
		Raw:      raw,
		Provider: p.name,
	}
}

func (p *GitHubProvider) fetchPrimaryEmail(token string) (string, error) {
	req, err := http.NewRequest("GET", p.emailsURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GoLaravel-Socialite/1.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("failed to fetch emails: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	type githubEmail struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}

	var emails []githubEmail
	if err := json.Unmarshal(body, &emails); err != nil {
		return "", err
	}

	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}

	if len(emails) > 0 {
		return emails[0].Email, nil
	}

	return "", nil
}
