package validation

import (
	"context"
	"fmt"
	"image"
	// Decoders for the dimensions rule.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"mime/multipart"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// dimensionsMatch checks image size constraints:
// width, height, min_width, max_width, min_height, max_height, ratio (3/2
// or 1.5). PNG, JPEG and GIF are decoded; other formats fail the rule.
func dimensionsMatch(f *multipart.FileHeader, params []string) bool {
	r, err := f.Open()
	if err != nil {
		return false
	}
	defer r.Close()
	cfg, _, err := image.DecodeConfig(r)
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return false
	}
	for _, p := range params {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok {
			return false
		}
		if k == "ratio" {
			want, ok := parseRatio(v)
			if !ok || math.Abs(float64(cfg.Width)/float64(cfg.Height)-want) > 1.0/float64(max(cfg.Width, cfg.Height)) {
				return false
			}
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return false
		}
		w, h := cfg.Width, cfg.Height
		switch k {
		case "width":
			ok = w == n
		case "height":
			ok = h == n
		case "min_width":
			ok = w >= n
		case "max_width":
			ok = w <= n
		case "min_height":
			ok = h >= n
		case "max_height":
			ok = h <= n
		default:
			ok = false
		}
		if !ok {
			return false
		}
	}
	return true
}

func parseRatio(s string) (float64, bool) {
	if a, b, ok := strings.Cut(s, "/"); ok {
		x, err1 := strconv.ParseFloat(a, 64)
		y, err2 := strconv.ParseFloat(b, 64)
		if err1 != nil || err2 != nil || y == 0 {
			return 0, false
		}
		return x / y, true
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil && f > 0
}

// PasswordRule checks password strength. Build it with Password():
//
//	"password": []any{"required", "confirmed", validation.Password().Min(12).MixedCase().Numbers().Symbols()}
type PasswordRule struct {
	min       int
	max       int
	letters   bool
	mixedCase bool
	numbers   bool
	symbols   bool
	notIn     map[string]bool
}

// Password returns a password rule requiring at least 8 characters.
func Password() *PasswordRule { return &PasswordRule{min: 8} }

// Min sets the minimum length in characters.
func (p *PasswordRule) Min(n int) *PasswordRule { p.min = n; return p }

// Max sets the maximum length (bcrypt only uses the first 72 bytes).
func (p *PasswordRule) Max(n int) *PasswordRule { p.max = n; return p }

// Letters requires at least one letter.
func (p *PasswordRule) Letters() *PasswordRule { p.letters = true; return p }

// MixedCase requires upper- and lower-case letters.
func (p *PasswordRule) MixedCase() *PasswordRule { p.mixedCase = true; return p }

// Numbers requires at least one digit.
func (p *PasswordRule) Numbers() *PasswordRule { p.numbers = true; return p }

// Symbols requires at least one character that is not a letter or digit.
func (p *PasswordRule) Symbols() *PasswordRule { p.symbols = true; return p }

// NotIn rejects passwords from a list (for example the most common
// passwords), compared case-insensitively.
func (p *PasswordRule) NotIn(list ...string) *PasswordRule {
	if p.notIn == nil {
		p.notIn = map[string]bool{}
	}
	for _, s := range list {
		p.notIn[strings.ToLower(s)] = true
	}
	return p
}

// Passes implements Rule.
func (p *PasswordRule) Passes(ctx context.Context, attr string, value any, data map[string]any) bool {
	return len(p.Failures(ctx, attr, value, data)) == 0
}

// Message implements Rule (Failures gives the detailed messages).
func (p *PasswordRule) Message() string {
	return "The :attribute field is not a strong enough password."
}

// Failures implements MultiRule: one message per unmet requirement.
func (p *PasswordRule) Failures(_ context.Context, _ string, value any, _ map[string]any) []string {
	s, ok := value.(string)
	if !ok {
		return []string{"The :attribute field must be a string."}
	}
	var out []string
	if n := utf8.RuneCountInString(s); n < p.min {
		out = append(out, fmt.Sprintf("The :attribute field must be at least %d characters.", p.min))
	} else if p.max > 0 && n > p.max {
		out = append(out, fmt.Sprintf("The :attribute field must not be greater than %d characters.", p.max))
	}
	var lower, upper, letter, digit, symbol bool
	for _, r := range s {
		if unicode.IsLetter(r) {
			letter = true
		}
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		case !unicode.IsLetter(r):
			symbol = true
		}
	}
	if p.letters && !letter {
		out = append(out, "The :attribute field must contain at least one letter.")
	}
	if p.mixedCase && !(lower && upper) {
		out = append(out, "The :attribute field must contain at least one uppercase and one lowercase letter.")
	}
	if p.numbers && !digit {
		out = append(out, "The :attribute field must contain at least one number.")
	}
	if p.symbols && !symbol {
		out = append(out, "The :attribute field must contain at least one symbol.")
	}
	if p.notIn[strings.ToLower(s)] {
		out = append(out, "The :attribute field is too common; choose another password.")
	}
	return out
}

var _ MultiRule = (*PasswordRule)(nil)
