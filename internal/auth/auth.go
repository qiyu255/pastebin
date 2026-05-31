// Package auth provides admin authentication using HTTP Basic Auth and bcrypt.
package auth

import (
	"encoding/base64"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	// DefaultCost is the bcrypt cost used for GenerateHash.
	DefaultCost = 12
)

// GenerateHash creates a bcrypt hash of the password.
func GenerateHash(password []byte) (string, error) {
	hash, err := bcrypt.GenerateFromPassword(password, DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// IsAdmin checks whether the request contains valid admin credentials.
// It extracts the Authorization header (Basic scheme), decodes it,
// and compares the password against the stored bcrypt hash.
func IsAdmin(r *http.Request, hash string) bool {
	if hash == "" {
		return false
	}

	auth := r.Header.Get("Authorization")
	if auth == "" {
		return false
	}

	username, password, ok := parseBasicAuth(auth)
	if !ok {
		return false
	}

	// Only check password; any non-empty username is accepted as admin
	_ = username

	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// parseBasicAuth parses an HTTP Basic Authentication header.
func parseBasicAuth(auth string) (username, password string, ok bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(auth, prefix) {
		return "", "", false
	}

	decoded, err := base64.StdEncoding.DecodeString(auth[len(prefix):])
	if err != nil {
		return "", "", false
	}

	s := string(decoded)
	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return "", "", false
	}

	return s[:colon], s[colon+1:], true
}
