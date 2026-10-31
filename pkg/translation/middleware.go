package translation

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/session"
)

// LocaleConfig defines options for the Localization middleware.
type LocaleConfig struct {
	DefaultLocale    string
	SupportedLocales []string
	ParamName        string
	CookieName       string
	CookieMaxAge     int
	CookiePath       string
	PersistCookie    bool
	SessionKey       string
	PersistSession   bool
}

// DefaultLocaleConfig provides sensible defaults for locale resolution.
var DefaultLocaleConfig = LocaleConfig{
	DefaultLocale:    "en",
	SupportedLocales: []string{"en"},
	ParamName:        "lang",
	CookieName:       "locale",
	CookieMaxAge:     86400 * 365, // 1 year
	CookiePath:       "/",
	PersistCookie:    true,
	SessionKey:       "locale",
	PersistSession:   true,
}

type langQuality struct {
	lang string
	q    float64
}

// parseAcceptLanguage parses an RFC 7231 Accept-Language header into a prioritized slice of language tags.
func parseAcceptLanguage(header string) []string {
	if header == "" {
		return nil
	}

	parts := strings.Split(header, ",")
	qualities := make([]langQuality, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}

		sub := strings.Split(trimmed, ";")
		tag := strings.TrimSpace(sub[0])
		q := 1.0

		if len(sub) > 1 {
			for _, param := range sub[1:] {
				param = strings.TrimSpace(param)
				if strings.HasPrefix(param, "q=") {
					if val, err := strconv.ParseFloat(strings.TrimPrefix(param, "q="), 64); err == nil {
						q = val
					}
				}
			}
		}

		if tag != "" && tag != "*" {
			qualities = append(qualities, langQuality{lang: tag, q: q})
		}
	}

	// Sort highest quality first
	sort.SliceStable(qualities, func(i, j int) bool {
		return qualities[i].q > qualities[j].q
	})

	results := make([]string, 0, len(qualities)*2)
	for _, lq := range qualities {
		results = append(results, lq.lang)
		// If tag is "en-US", also try base language "en"
		if idx := strings.IndexByte(lq.lang, '-'); idx > 0 {
			base := lq.lang[:idx]
			results = append(results, base)
		}
	}

	return results
}

func isSupported(locale string, supported []string) (string, bool) {
	for _, s := range supported {
		if strings.EqualFold(locale, s) {
			return s, true
		}
	}
	return "", false
}

// Middleware creates a GoLaravel HTTP middleware for automated locale detection and request scoping.
func Middleware(cfg ...LocaleConfig) context.HandlerFunc {
	config := DefaultLocaleConfig
	if len(cfg) > 0 {
		c := cfg[0]
		if c.DefaultLocale != "" {
			config.DefaultLocale = c.DefaultLocale
		}
		if len(c.SupportedLocales) > 0 {
			config.SupportedLocales = c.SupportedLocales
		}
		if c.ParamName != "" {
			config.ParamName = c.ParamName
		}
		if c.CookieName != "" {
			config.CookieName = c.CookieName
		}
		if c.CookieMaxAge != 0 {
			config.CookieMaxAge = c.CookieMaxAge
		}
		if c.CookiePath != "" {
			config.CookiePath = c.CookiePath
		}
		config.PersistCookie = c.PersistCookie
		if c.SessionKey != "" {
			config.SessionKey = c.SessionKey
		}
		config.PersistSession = c.PersistSession
	}

	return func(c *context.Context) error {
		var detected string

		// 1. Check URL query parameter (e.g. ?lang=es or ?locale=es)
		if queryVal := c.Query(config.ParamName); queryVal != "" {
			if match, ok := isSupported(queryVal, config.SupportedLocales); ok {
				detected = match
			}
		}
		if detected == "" && config.ParamName != "locale" {
			if queryVal := c.Query("locale"); queryVal != "" {
				if match, ok := isSupported(queryVal, config.SupportedLocales); ok {
					detected = match
				}
			}
		}

		// 2. Check Route parameter (:lang or :locale)
		if detected == "" {
			if paramVal := c.Param(config.ParamName); paramVal != "" {
				if match, ok := isSupported(paramVal, config.SupportedLocales); ok {
					detected = match
				}
			}
		}
		if detected == "" {
			if paramVal := c.Param("locale"); paramVal != "" {
				if match, ok := isSupported(paramVal, config.SupportedLocales); ok {
					detected = match
				}
			}
		}

		// 3. Check Session if available
		if detected == "" && config.SessionKey != "" {
			if s := session.FromContext(c); s != nil {
				if sessVal := s.GetString(config.SessionKey); sessVal != "" {
					if match, ok := isSupported(sessVal, config.SupportedLocales); ok {
						detected = match
					}
				}
			}
		}

		// 4. Check Cookie
		if detected == "" && config.CookieName != "" {
			if cookie, err := c.Request.Cookie(config.CookieName); err == nil && cookie.Value != "" {
				if match, ok := isSupported(cookie.Value, config.SupportedLocales); ok {
					detected = match
				}
			}
		}

		// 5. Check Accept-Language Header
		if detected == "" {
			tags := parseAcceptLanguage(c.Header("Accept-Language"))
			for _, tag := range tags {
				if match, ok := isSupported(tag, config.SupportedLocales); ok {
					detected = match
					break
				}
			}
		}

		// 6. Fallback to DefaultLocale
		if detected == "" {
			detected = config.DefaultLocale
		}

		// Set locale in context store
		c.SetLocale(detected)
		c.SetHeader("Content-Language", detected)

		// Persist cookie if enabled
		if config.PersistCookie && config.CookieName != "" {
			http.SetCookie(&c.Response, &http.Cookie{
				Name:     config.CookieName,
				Value:    detected,
				Path:     config.CookiePath,
				MaxAge:   config.CookieMaxAge,
				HttpOnly: false,
				SameSite: http.SameSiteLaxMode,
			})
		}

		// Persist to session if enabled
		if config.PersistSession && config.SessionKey != "" {
			if s := session.FromContext(c); s != nil {
				s.Put(config.SessionKey, detected)
			}
		}

		return c.Next()
	}
}
