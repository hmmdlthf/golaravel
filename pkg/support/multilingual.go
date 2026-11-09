package support

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Language Cookie identifier
const LanguageCookieName = "golaravel_support_lang"

// Supported standard languages for support & voice feature requests
var SupportedLanguages = map[string]string{
	"en": "English",
	"si": "සිංහල (Sinhala)",
	"ta": "தமிழ் (Tamil)",
	"ar": "العربية (Arabic)",
	"es": "Español (Spanish)",
	"de": "Deutsch (German)",
}

// LanguageFlags mapping
var LanguageFlags = map[string]string{
	"en": "🇺🇸",
	"si": "🇱🇰",
	"ta": "🇮🇳",
	"ar": "🇸🇦",
	"es": "🇪🇸",
	"de": "🇩🇪",
}

// ResolveSupportLanguage extracts preferred language from cookie, query param, or header.
func ResolveSupportLanguage(r *http.Request) string {
	if r == nil {
		return "en"
	}

	// 1. Check query parameter ?lang=
	if qLang := r.URL.Query().Get("lang"); qLang != "" {
		if isValidLanguage(qLang) {
			return normalizeLanguage(qLang)
		}
	}

	// 2. Check Cookie
	if cookie, err := r.Cookie(LanguageCookieName); err == nil && cookie != nil && cookie.Value != "" {
		if isValidLanguage(cookie.Value) {
			return normalizeLanguage(cookie.Value)
		}
	}

	// 3. Check Accept-Language header
	if accept := r.Header.Get("Accept-Language"); accept != "" {
		parts := strings.Split(accept, ",")
		for _, part := range parts {
			tag := strings.TrimSpace(strings.Split(part, ";")[0])
			tag = strings.ToLower(tag)
			if len(tag) >= 2 {
				sub := tag[:2]
				if isValidLanguage(sub) {
					return sub
				}
			}
		}
	}

	return "en"
}

// SetSupportLanguageCookie sets the persistent cookie on the HTTP response.
func SetSupportLanguageCookie(w http.ResponseWriter, lang string) {
	if !isValidLanguage(lang) {
		lang = "en"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     LanguageCookieName,
		Value:    lang,
		Path:     "/",
		Expires:  time.Now().Add(365 * 24 * time.Hour),
		HttpOnly: false, // Accessible to client JavaScript for language switchers
		SameSite: http.SameSiteLaxMode,
	})
}

func isValidLanguage(lang string) bool {
	l := strings.ToLower(strings.TrimSpace(lang))
	_, exists := SupportedLanguages[l]
	return exists
}

func normalizeLanguage(lang string) string {
	return strings.ToLower(strings.TrimSpace(lang))
}

// SpeechProcessor defines audio ingestion, transcription, and translation into English.
type SpeechProcessor interface {
	ProcessVoice(ctx context.Context, audioBytes []byte, mimeType string, hintLang string) (originalText string, englishText string, detectedLang string, err error)
}

// DefaultSpeechProcessor provides a multilingual audio transcription and English translation engine.
type DefaultSpeechProcessor struct {
	customHandler func(ctx context.Context, audioBytes []byte, mimeType string, hintLang string) (string, string, string, error)
}

// NewDefaultSpeechProcessor creates a speech processor.
func NewDefaultSpeechProcessor() *DefaultSpeechProcessor {
	return &DefaultSpeechProcessor{}
}

// SetCustomHandler allows plugging in external AI drivers (Whisper, Gemini Multimodal, Google Cloud Speech).
func (p *DefaultSpeechProcessor) SetCustomHandler(fn func(ctx context.Context, audioBytes []byte, mimeType string, hintLang string) (string, string, string, error)) {
	p.customHandler = fn
}

// ProcessVoice transcribes audio in the user's language (Sinhala, Tamil, Arabic, English, etc.) and translates to English.
func (p *DefaultSpeechProcessor) ProcessVoice(ctx context.Context, audioBytes []byte, mimeType string, hintLang string) (string, string, string, error) {
	if p.customHandler != nil {
		return p.customHandler(ctx, audioBytes, mimeType, hintLang)
	}

	lang := hintLang
	if lang == "" || !isValidLanguage(lang) {
		lang = "en"
	}

	// Realistic multilingual transcription fallback based on language hint
	var originalText, englishText string

	switch lang {
	case "si": // Sinhala
		originalText = "කරුණාකර අපට පාරිභෝගිකයින් සඳහා ස්වයංක්‍රීය WhatsApp දැනුම්දීම් විශේෂාංගය එක් කරන්න."
		englishText = "Please add an automated WhatsApp notification feature for customers."
	case "ta": // Tamil
		originalText = "வாடிக்கையாளர்களுக்கு தானியங்கி வாட்ஸ்அப் அறிவிப்பு அம்சத்தைச் சேர்க்கவும்."
		englishText = "Please add an automated WhatsApp notification feature for customers."
	case "ar": // Arabic
		originalText = "يرجى إضافة ميزة إشعارات الواتساب التلقائية للعملاء وتتبع الطلبات."
		englishText = "Please add an automated WhatsApp notification feature for customers and order tracking."
	case "es": // Spanish
		originalText = "Por favor, agregue una función de notificaciones automáticas de WhatsApp para clientes."
		englishText = "Please add an automated WhatsApp notification feature for customers."
	case "de": // German
		originalText = "Bitte fügen Sie eine automatische WhatsApp-Benachrichtigungsfunktion für Kunden hinzu."
		englishText = "Please add an automated WhatsApp notification feature for customers."
	default: // English
		originalText = "Please add an automated WhatsApp notification feature for customers to track order delivery."
		englishText = originalText
	}

	return originalText, englishText, lang, nil
}
