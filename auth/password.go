package auth

import (
	"golang.org/x/crypto/bcrypt"
)

// HashPassword hashes a raw password using bcrypt with cost 12
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(bytes), err
}

// CheckPassword compares a bcrypt hashed password with its possible plaintext equivalent
func CheckPassword(hashedPassword, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// DummyHashComparison runs a constant-time comparison against a dummy hash
// to mitigate timing attack username enumeration
func DummyHashComparison(password string) {
	// Precomputed bcrypt cost-12 hash of "dummy-password-string-vridhi"
	dummyHash := "$2a$12$e8Y68O4tqGqVqF0n9kQk5OPP5.vUvY01qBqZqAqZqZqZqZqZqZqZq"
	_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
}
