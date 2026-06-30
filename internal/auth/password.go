package auth

import (
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const MinPasswordLength = 6

var ErrPasswordTooShort = errors.New("password must be at least 6 characters")

func HashPassword(password string) (string, error) {
	password = strings.TrimSpace(password)
	if len(password) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifyPassword(hash string, password string) bool {
	if strings.TrimSpace(hash) == "" || strings.TrimSpace(password) == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
