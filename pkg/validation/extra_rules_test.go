package validation

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestPasswordRule(t *testing.T) {
	rule := Password().Min(10).MixedCase().Numbers().Symbols().NotIn("Password123!")
	e := Make(map[string]any{"password": "short"}, Rules{"password": []any{"required", rule}}).Errors()["password"]
	for _, want := range []string{"at least 10 characters", "uppercase and one lowercase", "one number", "one symbol"} {
		found := false
		for _, m := range e {
			if strings.Contains(m, want) && strings.HasPrefix(m, "The password field") {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %q in %v", want, e)
		}
	}
	if e := Make(map[string]any{"password": "Tr0ub4dor&3x"}, Rules{"password": []any{rule}}).Errors(); len(e) > 0 {
		t.Errorf("strong password rejected: %v", e)
	}
	if e := Make(map[string]any{"password": "password123!"}, Rules{"password": []any{Password().NotIn("Password123!")}}).Errors(); len(e) == 0 {
		t.Error("common password accepted")
	}
	if e := Make(map[string]any{"password": "12345678"}, Rules{"password": []any{Password().Letters()}}).Errors(); len(e) == 0 {
		t.Error("password without letters accepted")
	}
}

func TestExcludeRules(t *testing.T) {
	data := map[string]any{"type": "person", "company": "", "vat": "123", "notes": "x"}
	v := Make(data, Rules{
		"company": "exclude_if:type,person|required",
		"vat":     "exclude_unless:type,business|required|digits:9",
		"notes":   "exclude|string",
		"name":    "exclude_without:email|required",
	})
	if v.Fails() {
		t.Fatalf("excluded fields must not be validated: %v", v.Errors())
	}
	for _, k := range []string{"company", "vat", "notes", "name"} {
		if _, ok := v.Validated()[k]; ok {
			t.Errorf("excluded %s leaked into Validated(): %v", k, v.Validated())
		}
	}
	v = Make(map[string]any{"type": "business"}, Rules{"company": "exclude_if:type,person|required"})
	if !v.Fails() {
		t.Error("exclude_if must not exclude when the condition is false")
	}
	v = Make(map[string]any{"email": "a@b.co"}, Rules{"name": "exclude_with:email|required"})
	if v.Fails() {
		t.Errorf("exclude_with: %v", v.Errors())
	}
}

func TestRequiredArrayKeys(t *testing.T) {
	r := Rules{"address": "array|required_array_keys:street,city"}
	if e := Make(map[string]any{"address": map[string]any{"street": "1 Main"}}, r).Errors(); len(e["address"]) == 0 || !strings.Contains(e["address"][0], "street, city") {
		t.Errorf("missing key: %v", e)
	}
	if e := Make(map[string]any{"address": map[string]any{"street": "1", "city": "Colombo"}}, r).Errors(); len(e) > 0 {
		t.Errorf("all keys present: %v", e)
	}
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.Black)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDimensionsRule(t *testing.T) {
	f := multipartFile(t, "avatar.png", pngOf(t, 300, 200))
	expectValid(t, map[string]any{"f": f}, Rules{"f": "image|dimensions:min_width=100,max_width=400,ratio=3/2"})
	expectInvalid(t, "f", map[string]any{"f": f}, Rules{"f": "dimensions:min_width=500"})
	expectInvalid(t, "f", map[string]any{"f": f}, Rules{"f": "dimensions:ratio=1"})
	expectValid(t, map[string]any{"f": f}, Rules{"f": "dimensions:width=300,height=200"})
	notImage := multipartFile(t, "x.png", []byte("not an image"))
	expectInvalid(t, "f", map[string]any{"f": notImage}, Rules{"f": "dimensions:min_width=1"})
}
