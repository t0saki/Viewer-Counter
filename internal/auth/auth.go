// Package auth implements static bearer-token authentication for the admin
// API. Tokens are configured out-of-band (config file / env) and compared in
// constant time.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

type Authenticator struct {
	tokenHashes [][32]byte
}

func New(tokens []string) *Authenticator {
	a := &Authenticator{}
	for _, t := range tokens {
		if t = strings.TrimSpace(t); t != "" {
			a.tokenHashes = append(a.tokenHashes, sha256.Sum256([]byte(t)))
		}
	}
	return a
}

// Enabled reports whether any admin token is configured.
func (a *Authenticator) Enabled() bool { return len(a.tokenHashes) > 0 }

// Valid reports whether token matches a configured admin token.
func (a *Authenticator) Valid(token string) bool {
	if token == "" {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	ok := false
	for _, h := range a.tokenHashes {
		if subtle.ConstantTimeCompare(sum[:], h[:]) == 1 {
			ok = true
		}
	}
	return ok
}

// ExtractToken pulls the bearer token from the Authorization header, falling
// back to the X-API-Key header.
func ExtractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}
