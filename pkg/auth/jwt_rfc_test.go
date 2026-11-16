package auth

import (
	"errors"
	"testing"
)

// TestRFC7515ES256Interop verifies the ES256 example from RFC 7515
// appendix A.3 (key and token from the spec): the signature checks out, so
// only the long-passed exp fails. This pins the JOSE r||s signature format
// and JWKS EC parsing to the standard, not just to our own signer.
func TestRFC7515ES256Interop(t *testing.T) {
	jwks := []byte(`{"keys":[{"kty":"EC","crv":"P-256","x":"f83OJ3D2xF1Bg8vub9tLe1gHMzV76e8Tus9uPHvRVEU","y":"x_FEzRu9m36HLN_tue659LNpXW6pCyStikYjKIWI5a0"}]}`)
	v, err := NewJWTVerifierFromJWKS(jwks)
	if err != nil {
		t.Fatal(err)
	}
	tok := "eyJhbGciOiJFUzI1NiJ9.eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ.DtEhU3ljbEg8L38VWAfUAqOyKAM6-Xx-F4GawxaepmXFCgfTjDxw5djxLa8ISlSApmWQxfKTUJqPP3-Kg6NU1Q"
	_, err = v.VerifyToken(tok)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("want expired (signature valid), got %v", err)
	}
}
