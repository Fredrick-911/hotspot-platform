// Package auth handles staff logins (admins and box owners): password hashing,
// signed cookies, the login rate limiter, and the users table.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	hashScheme     = "pbkdf2-sha256"
	hashIterations = 600_000 // OWASP 2023 recommendation for PBKDF2-HMAC-SHA256
	hashKeyLen     = 32
	saltLen        = 16

	// MinPasswordLen is the shortest password we accept for staff accounts.
	MinPasswordLen = 10
)

var (
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	ErrBadHash          = errors.New("unrecognised password hash")
)

// HashPassword returns "pbkdf2-sha256$<iterations>$<salt>$<hash>". It uses only
// the Go standard library (crypto/pbkdf2, Go 1.24+), so there is no extra dependency.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLen {
		return "", ErrPasswordTooShort
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}
	return encodeHash(password, salt, hashIterations)
}

func encodeHash(password string, salt []byte, iterations int) (string, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, hashKeyLen)
	if err != nil {
		return "", fmt.Errorf("pbkdf2: %w", err)
	}
	enc := base64.RawStdEncoding
	return strings.Join([]string{hashScheme, strconv.Itoa(iterations), enc.EncodeToString(salt), enc.EncodeToString(key)}, "$"), nil
}

// CheckPassword reports whether password matches a hash made by HashPassword.
// The comparison takes the same time whether or not the password is right.
func CheckPassword(hash, password string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return false, ErrBadHash
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return false, ErrBadHash
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[2])
	if err != nil {
		return false, ErrBadHash
	}
	want, err := enc.DecodeString(parts[3])
	if err != nil {
		return false, ErrBadHash
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is checked when an email does not exist, so a login attempt takes
// about as long for unknown emails as for known ones.
var dummyHash = func() string {
	h, _ := encodeHash("not-a-real-password", make([]byte, saltLen), hashIterations)
	return h
}()

// GeneratePassword returns a random password for a new account, e.g.
// "k3f9-x2m7-qa84-p1zd" (16 characters from an unambiguous alphabet).
func GeneratePassword() (string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o/1/l/i
	out := make([]byte, 0, 19)
	buf := make([]byte, 1)
	for len(out) < 19 {
		if (len(out)+1)%5 == 0 {
			out = append(out, '-')
			continue
		}
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if int(buf[0]) >= 256-(256%len(alphabet)) {
			continue
		}
		out = append(out, alphabet[int(buf[0])%len(alphabet)])
	}
	return string(out[:19]), nil
}
