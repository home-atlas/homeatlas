package domain

import (
	"crypto/sha256"
	"encoding/base64"
	"time"
)

type AccountProfile struct {
	User        User `json:"user"`
	HasPassword bool `json:"hasPassword"`
}

type AccountSession struct {
	ID        string    `json:"id"`
	Current   bool      `json:"current"`
	Method    string    `json:"method"`
	LastSeen  time.Time `json:"lastSeenAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	TokenHash []byte    `json:"-"`
}

// Management handles cannot be used as cookies or reveal stored token hashes.
func SessionHandle(hash []byte) string {
	digest := sha256.Sum256(append([]byte("homeatlas/session-management/v1:"), hash...))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
