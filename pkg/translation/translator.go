package translation

import (
	"io/fs"
	"sync"

	"github.com/golaravel/golaravel/pkg/context"
)

// Translator manages localized message dictionaries and parameter interpolations.
type Translator struct {
	mu             sync.RWMutex
	defaultLocale  string
	fallbackLocale string
	messages       map[string]map[string]string // locale -> key -> line
}

// Option defines a functional option for configuring a Translator.
type Option func(*Translator)

// WithLocale sets the initial default locale.
func WithLocale(locale string) Option {
	return func(t *Translator) {
		t.defaultLocale = locale
	}
}

// WithFallback sets the fallback locale.
func WithFallback(fallback string) Option {
	return func(t *Translator) {
		t.fallbackLocale = fallback
	}
}

// New creates a new thread-safe Translator instance.
func New(opts ...Option) *Translator {
	t := &Translator{
		defaultLocale:  "en",
		fallbackLocale: "en",
		messages:       make(map[string]map[string]string),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

var (
	defaultMu         sync.RWMutex
	defaultTranslator = New()
)

// Default returns the global default Translator instance.
func Default() *Translator {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultTranslator
}

// SetDefault sets the global default Translator instance.
func SetDefault(t *Translator) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultTranslator = t
	context.SetTranslator(t)
}

// SetLocale sets the active locale.
func (t *Translator) SetLocale(locale string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.defaultLocale = locale
}

// GetLocale returns the active locale.
func (t *Translator) GetLocale() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.defaultLocale
}

// SetFallback sets the fallback locale.
func (t *Translator) SetFallback(fallback string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fallbackLocale = fallback
}

// GetFallback returns the fallback locale.
func (t *Translator) GetFallback() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.fallbackLocale
}

// IsLocale checks if the current locale matches the given locale.
func (t *Translator) IsLocale(locale string) bool {
	return t.GetLocale() == locale
}

// Has checks if a translation key exists for the given locale (or current locale).
func (t *Translator) Has(key string, locale ...string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	loc := t.defaultLocale
	if len(locale) > 0 && locale[0] != "" {
		loc = locale[0]
	}

	if locMap, ok := t.messages[loc]; ok {
		if _, exists := locMap[key]; exists {
			return true
		}
	}
	return false
}

// Get translates the given key for the current active locale.
func (t *Translator) Get(key string, replace ...map[string]any) string {
	return t.GetForLocale(key, t.GetLocale(), replace...)
}

// GetForLocale translates the given key for the specified locale, falling back if not found.
func (t *Translator) GetForLocale(key string, locale string, replace ...map[string]any) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if locale == "" {
		locale = t.defaultLocale
	}

	// 1. Try specified locale
	line, found := t.lookup(locale, key)

	// 2. Try fallback locale if not found
	if !found && t.fallbackLocale != "" && t.fallbackLocale != locale {
		line, found = t.lookup(t.fallbackLocale, key)
	}

	// 3. If still not found, return key
	if !found {
		line = key
	}

	// 4. Interpolate parameters if provided
	if len(replace) > 0 && replace[0] != nil {
		line = Interpolate(line, replace[0])
	}

	return line
}

// Choice selects a pluralized translation based on number for the current locale.
func (t *Translator) Choice(key string, number int, replace ...map[string]any) string {
	return t.ChoiceForLocale(key, t.GetLocale(), number, replace...)
}

// ChoiceForLocale selects a pluralized translation based on number for the specified locale.
func (t *Translator) ChoiceForLocale(key string, locale string, number int, replace ...map[string]any) string {
	line := t.GetForLocale(key, locale)
	selected := SelectPlural(line, number)

	params := make(map[string]any)
	if len(replace) > 0 && replace[0] != nil {
		for k, v := range replace[0] {
			params[k] = v
		}
	}
	// Always supply :count if not explicitly overwritten
	if _, ok := params["count"]; !ok {
		params["count"] = number
	}

	return Interpolate(selected, params)
}

func (t *Translator) lookup(locale, key string) (string, bool) {
	if locMap, ok := t.messages[locale]; ok {
		if line, exists := locMap[key]; exists {
			return line, true
		}
	}
	return "", false
}

// AddLines registers a map of translation lines for a locale.
// An optional group can be provided to prefix all keys (e.g. "auth" -> "auth.failed").
func (t *Translator) AddLines(lines map[string]string, locale string, group ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.messages[locale]; !ok {
		t.messages[locale] = make(map[string]string)
	}

	prefix := ""
	if len(group) > 0 && group[0] != "" {
		prefix = group[0] + "."
	}

	for k, v := range lines {
		t.messages[locale][prefix+k] = v
	}
}

// AddJSON parses and registers a raw JSON dictionary for a locale.
func (t *Translator) AddJSON(data []byte, locale string, group ...string) error {
	prefix := ""
	if len(group) > 0 {
		prefix = group[0]
	}

	temp := make(map[string]string)
	if err := parseJSONTranslations(data, prefix, temp); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.messages[locale]; !ok {
		t.messages[locale] = make(map[string]string)
	}
	for k, v := range temp {
		t.messages[locale][k] = v
	}
	return nil
}

// LoadFS loads all translation files from an fs.FS (e.g. embedded or DirFS).
func (t *Translator) LoadFS(fsys fs.FS, rootDir string) error {
	data, err := LoadFromFS(fsys, rootDir)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for loc, items := range data {
		if _, ok := t.messages[loc]; !ok {
			t.messages[loc] = make(map[string]string)
		}
		for k, v := range items {
			t.messages[loc][k] = v
		}
	}
	return nil
}

// LoadDir loads translation files from a local directory path.
func (t *Translator) LoadDir(dirPath string) error {
	data, err := LoadFromDir(dirPath)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for loc, items := range data {
		if _, ok := t.messages[loc]; !ok {
			t.messages[loc] = make(map[string]string)
		}
		for k, v := range items {
			t.messages[loc][k] = v
		}
	}
	return nil
}

// Package-level shortcuts operating on the Default() translator

// Trans translates the given key for the active locale.
func Trans(key string, replace ...map[string]any) string {
	return Default().Get(key, replace...)
}

// TransChoice translates with pluralization for the active locale.
func TransChoice(key string, number int, replace ...map[string]any) string {
	return Default().Choice(key, number, replace...)
}

// SetLocale sets the global active locale.
func SetLocale(locale string) {
	Default().SetLocale(locale)
}

// GetLocale returns the global active locale.
func GetLocale() string {
	return Default().GetLocale()
}

// SetFallback sets the global fallback locale.
func SetFallback(fallback string) {
	Default().SetFallback(fallback)
}

// Has checks if a key exists in the global translator.
func Has(key string, locale ...string) bool {
	return Default().Has(key, locale...)
}
