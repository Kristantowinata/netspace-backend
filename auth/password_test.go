package auth

import "testing"

func TestCheckPasswordHashed(t *testing.T) {
	hash, err := HashPassword("admin123")
	if err != nil {
		t.Fatal(err)
	}
	if !IsPasswordHashed(hash) {
		t.Fatalf("expected %q to be recognised as a bcrypt hash", hash)
	}
	if !CheckPassword(hash, "admin123") {
		t.Error("correct password rejected")
	}
	if CheckPassword(hash, "admin124") {
		t.Error("wrong password accepted")
	}
}

func TestCheckPasswordLegacyPlaintext(t *testing.T) {
	if IsPasswordHashed("admin123") {
		t.Fatal("plaintext recognised as a hash")
	}
	if !CheckPassword("admin123", "admin123") {
		t.Error("correct legacy password rejected")
	}
	if CheckPassword("admin123", "admin12") {
		t.Error("wrong legacy password accepted")
	}
}
