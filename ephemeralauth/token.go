// Package ephemeralauth provides stateless short-lived JWT gating for public
// API endpoints. It includes guest-session middleware, token issuance with
// bot verification, and a protection middleware — all gorilla/mux-compatible
// (func(http.Handler) http.Handler) and usable standalone or auto-wired
// through litespaserver config.
package ephemeralauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// maxTokenTTL is the hard upper bound on token lifetime.
const maxTokenTTL = 180 * time.Second

// defaultTokenTTL is used when Config.TokenTTLSeconds is zero.
const defaultTokenTTL = 120 * time.Second

// DeriveKey normalises an arbitrary-length signing key to exactly 32 bytes
// using SHA-256. All public functions in this package call DeriveKey
// internally, so consumers may supply any non-empty key. Exported for
// callers that need to derive the key themselves (e.g. for external JWT
// signing in tests).
func DeriveKey(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}

// clampTTL clamps a duration to the valid token lifetime range [60s, 180s].
// Zero or negative values are replaced with the default TTL.
func clampTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultTokenTTL
	}
	if ttl < 60*time.Second {
		return 60 * time.Second
	}
	if ttl > maxTokenTTL {
		return maxTokenTTL
	}
	return ttl
}

// Claims extends jwt.RegisteredClaims with context-binding fields.
type Claims struct {
	jwt.RegisteredClaims
	IPHash string   `json:"ip_hash"`
	UAHash string   `json:"ua_hash"`
	Scopes []string `json:"scopes"`
}

// Issuer creates and verifies ephemeral JWTs.
type Issuer struct {
	signingKey []byte
	ttl        time.Duration
}

// NewIssuer creates an Issuer from the signing key and TTL.
// Any non-empty key is accepted; it is normalised to 32 bytes via
// DeriveKey (SHA-256).
// ttl is clamped to [60s, 180s].
func NewIssuer(signingKey []byte, ttl time.Duration) *Issuer {
	if len(signingKey) == 0 {
		panic("ephemeralauth: signing key must not be empty")
	}
	return &Issuer{signingKey: DeriveKey(signingKey), ttl: clampTTL(ttl)}
}

// Issue creates a signed JWT with the provided context bindings.
// Returns the token string, the expires_in seconds, and any error.
func (iss *Issuer) Issue(sessionID, ipHash, uaHash string, scopes []string) (string, int, error) {
	now := time.Now()
	exp := now.Add(iss.ttl)

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sessionID,
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "ephemeralauth",
		},
		IPHash: ipHash,
		UAHash: uaHash,
		Scopes: scopes,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(iss.signingKey)
	if err != nil {
		return "", 0, fmt.Errorf("ephemeralauth: sign token: %w", err)
	}

	return signed, int(iss.ttl.Seconds()), nil
}

// Verify parses and validates the token. Algorithm is pinned to HS256.
// Returns the claims on success or an error describing the failure.
func (iss *Issuer) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		// Algorithm pinning: only HS256 is accepted.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("ephemeralauth: unexpected signing method: %v", t.Header["alg"])
		}
		return iss.signingKey, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, fmt.Errorf("ephemeralauth: verify token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("ephemeralauth: token is not valid")
	}
	return claims, nil
}

// HashContext computes HMAC-SHA256(DeriveKey(key), value), truncated to 16 bytes,
// base64url-encoded. Used for IP and User-Agent context binding.
func HashContext(key []byte, value string) string {
	h := hmac.New(sha256.New, DeriveKey(key))
	h.Write([]byte(value))
	sum := h.Sum(nil)[:16]
	return encodeBase64URL(sum)
}
