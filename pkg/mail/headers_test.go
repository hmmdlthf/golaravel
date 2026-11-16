package mail

import (
	"errors"
	"strings"
	"testing"
)

func build(t *testing.T, m *Message) (string, error) {
	t.Helper()
	b, err := m.BuildMIME()
	return string(b), err
}

func TestHeaderInjectionIsRefused(t *testing.T) {
	cases := map[string]*Message{
		"subject":   NewMessage().From("a@example.com").To("b@example.com").Subject("Hi\r\nBcc: victim@example.com"),
		"to":        NewMessage().From("a@example.com").To("b@example.com\r\nBcc: victim@example.com").Subject("x"),
		"from name": NewMessage().From("a@example.com", "Eve\nBcc: victim@example.com").To("b@example.com").Subject("x"),
		"reply-to":  NewMessage().From("a@example.com").To("b@example.com").ReplyTo("c@example.com\r\nX: y").Subject("x"),
		"header":    NewMessage().From("a@example.com").To("b@example.com").Subject("x").Header("X-Tag", "a\r\nBcc: v@example.com"),
		"hdr name":  NewMessage().From("a@example.com").To("b@example.com").Subject("x").Header("Bad Name:", "v"),
		"bad addr":  NewMessage().From("a@example.com").To("not an address").Subject("x"),
		"filename":  NewMessage().From("a@example.com").To("b@example.com").Subject("x").Attach("a.txt\r\nX-Evil: 1", []byte("x")),
	}
	for name, m := range cases {
		out, err := build(t, m)
		if !errors.Is(err, ErrInvalidHeader) {
			t.Errorf("%s: expected ErrInvalidHeader, got %v", name, err)
		}
		if strings.Contains(out, "victim@example.com") {
			t.Errorf("%s: injected header written", name)
		}
	}
}

func TestHeadersAreEncoded(t *testing.T) {
	m := NewMessage().From("billing@example.com", "Café Billing").To("Zoë <zoe@example.com>").
		Subject("Ihre Rechnung über 20 €").Attach("rechnung März.pdf", []byte("%PDF"))
	out, err := build(t, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"From: =?utf-8?q?Caf=C3=A9_Billing?= <billing@example.com>",
		"To: =?utf-8?q?Zo=C3=AB?= <zoe@example.com>",
		"Subject: =?utf-8?q?",
		"filename*=utf-8''rechnung%20M%C3%A4rz.pdf",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Plain ASCII stays readable.
	out, _ = build(t, NewMessage().From("b@example.com", "GoLaravel Billing").To("c@example.com").Subject("Invoice #1024"))
	if !strings.Contains(out, "From: GoLaravel Billing <b@example.com>") || !strings.Contains(out, "Subject: Invoice #1024") {
		t.Errorf("ASCII headers changed:\n%s", out)
	}
}
