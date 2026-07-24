package auth

import "testing"

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
