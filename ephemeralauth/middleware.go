package ephemeralauth

import (
	"crypto/hmac"
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
func Protect(issuer *Issuer, signingKey []byte, trustProxy bool, requiredScopes ...string) func(http.Handler) http.Handler {
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
				http.Error(w, "unauthorized: invalid token", http.StatusUnauthorized)
				return
			}

			// Check session binding: token's sub must match the current guest cookie.
			sessionID, ok := GuestSessionID(r, signingKey)
			if !ok || sessionID != claims.Subject {
				http.Error(w, "forbidden: session mismatch", http.StatusForbidden)
				return
			}

			// Check IP binding.
			remoteIP := clientIP(r, trustProxy)
			ipHash := HashContext(signingKey, remoteIP)
			if !hmac.Equal([]byte(ipHash), []byte(claims.IPHash)) {
				http.Error(w, "forbidden: IP mismatch", http.StatusForbidden)
				return
			}

			// Check User-Agent binding.
			uaHash := HashContext(signingKey, r.UserAgent())
			if !hmac.Equal([]byte(uaHash), []byte(claims.UAHash)) {
				http.Error(w, "forbidden: User-Agent mismatch", http.StatusForbidden)
				return
			}

			// Check scopes.
			if len(requiredScopes) > 0 && !hasRequiredScopes(claims.Scopes, requiredScopes) {
				http.Error(w, "forbidden: insufficient scope", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// hasRequiredScopes checks that every required scope is present in the token's scopes.
func hasRequiredScopes(tokenScopes, required []string) bool {
	set := make(map[string]struct{}, len(tokenScopes))
	for _, s := range tokenScopes {
		set[s] = struct{}{}
	}
	for _, r := range required {
		if _, ok := set[r]; !ok {
			return false
		}
	}
	return true
}
