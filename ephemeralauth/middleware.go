package ephemeralauth

import (
	"crypto/hmac"
	"log/slog"
	"net/http"
	"strings"
)

// Protect returns middleware that protects API routes with ephemeral token
// verification. It extracts the Bearer token, verifies its signature and
// expiry, checks context binding (session, IP, UA), and validates scopes.
//
// Missing/malformed header → 401
// Expired or bad-signature → 401
// Binding mismatch (session/IP/UA) → 403
// Insufficient scope → 403
// Valid + bound + scoped → request passes through
func Protect(issuer *Issuer, trustProxy bool, requiredScopes ...string) func(http.Handler) http.Handler {
	if issuer == nil {
		panic("ephemeralauth: Protect requires a non-nil Issuer")
	}
	// Reuse the Issuer's pre-derived key for context-binding HMAC.
	derivedKey := issuer.signingKey
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract Bearer token.
			auth := r.Header.Get("Authorization")
			if auth == "" {
				http.Error(w, "unauthorized: missing Authorization header", http.StatusUnauthorized)
				return
			}
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, "unauthorized: malformed Authorization header", http.StatusUnauthorized)
				return
			}
			tokenString := strings.TrimPrefix(auth, "Bearer ")
			if tokenString == "" {
				http.Error(w, "unauthorized: empty Bearer token", http.StatusUnauthorized)
				return
			}

			// Verify signature + expiry (stateless).
			claims, err := issuer.Verify(tokenString)
			if err != nil {
				slog.Debug("ephemeralauth: token verification failed", "error", err)
				http.Error(w, "unauthorized: invalid token", http.StatusUnauthorized)
				return
			}

			// Check session binding: token's sub must match the current guest cookie.
			sessionID, ok := guestSessionIDWithKey(r, derivedKey)
			if !ok || !hmac.Equal([]byte(sessionID), []byte(claims.Subject)) {
				slog.Debug("ephemeralauth: session binding mismatch",
					"cookie_valid", ok,
					"token_sub", claims.Subject,
					"cookie_session", sessionID)
				http.Error(w, "forbidden: session mismatch", http.StatusForbidden)
				return
			}

			// Check IP binding.
			remoteIP := clientIP(r, trustProxy)
			ipHash := hashContextWithKey(derivedKey, remoteIP)
			if !hmac.Equal([]byte(ipHash), []byte(claims.IPHash)) {
				slog.Debug("ephemeralauth: IP binding mismatch",
					"remote_ip", remoteIP)
				http.Error(w, "forbidden: IP mismatch", http.StatusForbidden)
				return
			}

			// Check User-Agent binding.
			uaHash := hashContextWithKey(derivedKey, r.UserAgent())
			if !hmac.Equal([]byte(uaHash), []byte(claims.UAHash)) {
				slog.Debug("ephemeralauth: User-Agent binding mismatch")
				http.Error(w, "forbidden: User-Agent mismatch", http.StatusForbidden)
				return
			}

			// Check scopes.
			if len(requiredScopes) > 0 && !hasRequiredScopes(claims.Scopes, requiredScopes) {
				slog.Debug("ephemeralauth: insufficient scope",
					"required", requiredScopes, "token_scopes", claims.Scopes)
				http.Error(w, "forbidden: insufficient scope", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// hasRequiredScopes checks that every required scope is present in the token's scopes.
// Uses linear scan, which is efficient for the typical case of 1–4 scopes.
func hasRequiredScopes(tokenScopes, required []string) bool {
	for _, r := range required {
		found := false
		for _, s := range tokenScopes {
			if s == r {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
