package mail

import (
	"errors"
	"fmt"
	"mime"
	netmail "net/mail"
	"strings"
)

// ErrInvalidHeader is returned by BuildMIME when a header value would break
// out of its header line (CR / LF) or an address cannot be parsed. Values
// often come from user input (contact forms, names), so they are never
// written as-is.
var ErrInvalidHeader = errors.New("mail: invalid header value")

func hasLineBreak(s string) bool { return strings.ContainsAny(s, "\r\n") }

// isAtext reports whether r may appear unquoted in a display name (RFC 5322
// atext, plus space between words).
func isAtext(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ':
		return true
	}
	return strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", r)
}

// encodeDisplayName renders a display name safely: plain ASCII words as-is,
// non-ASCII as an RFC 2047 encoded word, anything else as a quoted string.
func encodeDisplayName(name string) string {
	plain, ascii := true, true
	for _, r := range name {
		if r > 127 {
			ascii = false
		}
		if !isAtext(r) {
			plain = false
		}
	}
	switch {
	case plain:
		return name
	case !ascii:
		return mime.QEncoding.Encode("utf-8", name)
	default:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
	}
}

// formatAddress validates one address (bare or "Name <addr>") and renders
// it for a header.
func formatAddress(addr, name string) (string, error) {
	if hasLineBreak(addr) || hasLineBreak(name) {
		return "", fmt.Errorf("%w: line break in address %q", ErrInvalidHeader, addr)
	}
	parsed, err := netmail.ParseAddress(addr)
	if err != nil {
		return "", fmt.Errorf("%w: address %q: %v", ErrInvalidHeader, addr, err)
	}
	if name == "" {
		name = parsed.Name
	}
	if name == "" {
		return parsed.Address, nil
	}
	return fmt.Sprintf("%s <%s>", encodeDisplayName(name), parsed.Address), nil
}

func formatAddressList(addrs []string) (string, error) {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		f, err := formatAddress(a, "")
		if err != nil {
			return "", err
		}
		out = append(out, f)
	}
	return strings.Join(out, ", "), nil
}

// encodeHeaderText validates and encodes an unstructured header (Subject or
// a custom header value).
func encodeHeaderText(field, value string) (string, error) {
	if hasLineBreak(value) {
		return "", fmt.Errorf("%w: line break in %s", ErrInvalidHeader, field)
	}
	return mime.QEncoding.Encode("utf-8", value), nil
}

// validHeaderName reports whether key is an RFC 5322 field name.
func validHeaderName(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < 33 || c > 126 || c == ':' {
			return false
		}
	}
	return true
}
