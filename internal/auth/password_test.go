package auth

import (
	"encoding/base64"
	"fmt"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestVerifyPasswordAcceptsLegacyParameters(t *testing.T) {
	// A hash created with the previous 64 MiB / t=3 / p=2 parameters must
	// still verify after the parameter reduction, and be flagged for rehash.
	const password = "correct horse battery staple"
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	encoded := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		64*1024, 3, 2,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	ok, err := VerifyPassword(encoded, password)
	if err != nil {
		t.Fatalf("VerifyPassword(legacy) error = %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword(legacy) = false, want true")
	}
	if !NeedsRehash(encoded) {
		t.Fatal("NeedsRehash(legacy) = false, want true")
	}
}

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword() = false, want true")
	}
	ok, err = VerifyPassword(hash, "incorrect password")
	if err != nil {
		t.Fatalf("VerifyPassword(wrong) error = %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword(wrong) = true")
	}
	if NeedsRehash(hash) {
		t.Fatal("NeedsRehash() = true for current parameters")
	}
}

func TestHashPasswordRejectsShortPassword(t *testing.T) {
	if _, err := HashPassword("12345"); err == nil {
		t.Fatal("HashPassword() error = nil")
	}
	if _, err := HashPassword("短密码"); err == nil {
		t.Fatal("HashPassword() accepted fewer than 6 Unicode characters")
	}
}

func TestHashPasswordAcceptsSixCharacters(t *testing.T) {
	if _, err := HashPassword("123456"); err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
}
