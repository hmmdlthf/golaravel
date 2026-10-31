package translation

import (
	"fmt"
	"strings"
	"unicode"
)

// makeReplacement formats any value into a string.
func formatValue(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case error:
		return v.Error()
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v)
	case float32, float64:
		return fmt.Sprintf("%v", v)
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// applyCase matches the case pattern of placeholder in the replaced text.
// e.g. ":name" -> "john", ":Name" -> "John", ":NAME" -> "JOHN"
func applyCase(pattern, replacement string) string {
	if len(pattern) == 0 || len(replacement) == 0 {
		return replacement
	}

	// Remove leading ':'
	clean := strings.TrimPrefix(pattern, ":")
	if len(clean) == 0 {
		return replacement
	}

	runes := []rune(clean)
	allUpper := true
	for _, r := range runes {
		if !unicode.IsUpper(r) {
			allUpper = false
			break
		}
	}
	if allUpper && len(runes) > 1 {
		return strings.ToUpper(replacement)
	}

	if unicode.IsUpper(runes[0]) {
		rRep := []rune(replacement)
		rRep[0] = unicode.ToUpper(rRep[0])
		return string(rRep)
	}

	return replacement
}

// Interpolate replaces :placeholders in line with values from params.
// Supports Laravel-style case matching:
//
//	:key  -> lower
//	:Key  -> Title
//	:KEY  -> UPPER
func Interpolate(line string, params map[string]any) string {
	if len(params) == 0 || !strings.Contains(line, ":") {
		return line
	}

	for key, val := range params {
		valStr := formatValue(val)

		// Variants
		lowerKey := ":" + strings.ToLower(key)
		titleKey := ":" + strings.Title(strings.ToLower(key))
		upperKey := ":" + strings.ToUpper(key)

		// Match exact casing given
		exactKey := ":" + key
		if strings.Contains(line, exactKey) {
			line = strings.ReplaceAll(line, exactKey, applyCase(exactKey, valStr))
		}
		if strings.Contains(line, titleKey) {
			line = strings.ReplaceAll(line, titleKey, applyCase(titleKey, valStr))
		}
		if strings.Contains(line, upperKey) {
			line = strings.ReplaceAll(line, upperKey, applyCase(upperKey, valStr))
		}
		if strings.Contains(line, lowerKey) {
			line = strings.ReplaceAll(line, lowerKey, applyCase(lowerKey, valStr))
		}
	}

	return line
}
