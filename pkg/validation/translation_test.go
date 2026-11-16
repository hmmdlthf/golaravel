package validation

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/translation"
)

func TestMessagesComeFromLangFiles(t *testing.T) {
	tr := translation.New(translation.WithLocale("es"))
	if err := tr.AddJSON([]byte(`{
		"required": "El campo :attribute es obligatorio.",
		"min": {"string": "El campo :attribute debe tener al menos :min caracteres."}
	}`), "es", "validation"); err != nil {
		t.Fatal(err)
	}
	prev := translation.Default()
	translation.SetDefault(tr)
	t.Cleanup(func() { translation.SetDefault(prev) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"ab"}`))
	req.Header.Set("Content-Type", "application/json")
	c := &gcontext.Context{}
	c.Reset(rec, req)
	ValidateRequest(c, Rules{"name": "string|min:3", "email": "required", "age": "integer"})

	var body ValidationError
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if got := body.Errors["name"]; len(got) != 1 || got[0] != "El campo name debe tener al menos 3 caracteres." {
		t.Errorf("nested size translation: %v", got)
	}
	if got := body.Errors["email"]; len(got) != 1 || got[0] != "El campo email es obligatorio." {
		t.Errorf("translation: %v", got)
	}
}
