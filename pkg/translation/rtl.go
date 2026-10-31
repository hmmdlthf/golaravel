package translation

import (
	"strings"
	"sync"
)

var (
	rtlMu      sync.RWMutex
	rtlLocales = map[string]bool{
		"ar":  true, // Arabic
		"fa":  true, // Persian / Farsi
		"ur":  true, // Urdu
		"he":  true, // Hebrew
		"yi":  true, // Yiddish
		"ps":  true, // Pashto
		"sd":  true, // Sindhi
		"ug":  true, // Uyghur
		"ckb": true, // Central Kurdish (Sorani)
		"ku":  true, // Kurdish (Arabic script)
		"dv":  true, // Divehi / Maldivian
	}
)

// RegisterRTL registers one or more locale codes as Right-to-Left (RTL).
func RegisterRTL(locales ...string) {
	rtlMu.Lock()
	defer rtlMu.Unlock()
	for _, loc := range locales {
		clean := strings.ToLower(strings.TrimSpace(loc))
		if clean != "" {
			rtlLocales[clean] = true
		}
	}
}

// IsRTL reports whether the given locale (or current active locale if omitted) is Right-to-Left.
// Accurately recognizes regional variants such as "ar-SA", "ar-EG", "fa-IR", "ur_PK", "he-IL".
func IsRTL(locale ...string) bool {
	loc := ""
	if len(locale) > 0 && locale[0] != "" {
		loc = locale[0]
	} else {
		loc = GetLocale()
	}

	loc = strings.ToLower(strings.TrimSpace(loc))
	if loc == "" {
		return false
	}

	rtlMu.RLock()
	defer rtlMu.RUnlock()

	// 1. Direct match (e.g. "ar", "he")
	if rtlLocales[loc] {
		return true
	}

	// 2. Base language prefix match (e.g. "ar-SA" -> "ar", "fa_IR" -> "fa")
	if idx := strings.IndexAny(loc, "-_"); idx > 0 {
		base := loc[:idx]
		if rtlLocales[base] {
			return true
		}
	}

	return false
}

// IsLTR reports whether the given locale (or current active locale if omitted) is Left-to-Right.
func IsLTR(locale ...string) bool {
	return !IsRTL(locale...)
}

// Direction returns "rtl" or "ltr" for the given locale (or current active locale if omitted).
func Direction(locale ...string) string {
	if IsRTL(locale...) {
		return "rtl"
	}
	return "ltr"
}

// IsRTL reports whether the Translator's active locale (or specified locale) is RTL.
func (t *Translator) IsRTL(locale ...string) bool {
	loc := t.defaultLocale
	if len(locale) > 0 && locale[0] != "" {
		loc = locale[0]
	}
	return IsRTL(loc)
}

// Direction returns "rtl" or "ltr" for the Translator's active locale (or specified locale).
func (t *Translator) Direction(locale ...string) string {
	if t.IsRTL(locale...) {
		return "rtl"
	}
	return "ltr"
}
