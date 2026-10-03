package auth

import (
	"crypto/subtle"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword returns a bcrypt hash suitable for storing in Admins.password.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// IsPasswordHashed reports whether a stored password is already a bcrypt hash.
func IsPasswordHashed(stored string) bool {
	return strings.HasPrefix(stored, "$2a$") ||
		strings.HasPrefix(stored, "$2b$") ||
		strings.HasPrefix(stored, "$2y$")
}

// CheckPassword compares a login attempt against the stored password.
//
// Rows seeded before hashing was introduced still hold plaintext; those are
// compared in constant time so the caller can upgrade them to a hash on the
// first successful login.
func CheckPassword(stored string, password string) bool {
	if IsPasswordHashed(stored) {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(password)) == 1
}
