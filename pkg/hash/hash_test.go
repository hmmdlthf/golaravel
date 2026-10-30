package hash

import (
	"testing"
)

func TestHash_Bcrypt(t *testing.T) {
	password := "SecretP@ssw0rd!"
	hashed, err := BcryptMake(password)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !BcryptCheck(password, hashed) {
		t.Fatal("expected bcrypt password check to pass")
	}

	if BcryptCheck("WrongPassword", hashed) {
		t.Fatal("expected wrong password to fail")
	}
}

func TestHash_Argon2id(t *testing.T) {
	password := "Argon2SecurePassword"
	hashed, err := Argon2Make(password)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !Argon2Check(password, hashed) {
		t.Fatal("expected argon2id check to pass")
	}

	if Argon2Check("BadPassword", hashed) {
		t.Fatal("expected bad password to fail")
	}

	// Verify Check universal helper
	if !Check(password, hashed) {
		t.Fatal("expected universal Check helper to pass")
	}
}
