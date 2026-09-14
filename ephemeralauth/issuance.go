package ephemeralauth

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// issuanceRequest is the expected JSON body for token issuance.
type issuanceRequest struct {
	BotToken string `json:"bot_token"`
}

// issuanceResponse is the JSON response on successful token issuance.
type issuanceResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

// IssuanceHandler returns an http.Handler for the ephemeral token issuance
// endpoint (POST /api/auth/ephemeral-token).
func IssuanceHandler(cfg Config, verifier BotVerifier, issuer *Issuer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Require a valid guest session cookie.
		sessionID, ok := GuestSessionID(r, []byte(cfg.SigningKey))
		if !ok {
			http.Error(w, "unauthorized: no valid guest session", http.StatusUnauthorized)
			return
		}

		// Parse the bot token from the request body (limit to 1KB).
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var req issuanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BotToken == "" {
			http.Error(w, "bad request: bot_token required", http.StatusBadRequest)
			return
		}

		// Extract client IP.
		remoteIP := clientIP(r, cfg.TrustProxy)

		// Verify the bot token (fail-closed).
		passed, err := verifier.Verify(r.Context(), req.BotToken, remoteIP)
		if err != nil || !passed {
			http.Error(w, "forbidden: bot verification failed", http.StatusForbidden)
			return
		}

		// Compute context binding hashes.
		signingKey := []byte(cfg.SigningKey)
		ipHash := HashContext(signingKey, remoteIP)
		uaHash := HashContext(signingKey, r.UserAgent())

		// Issue the token.
		token, expiresIn, err := issuer.Issue(sessionID, ipHash, uaHash, cfg.DefaultScopes())
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issuanceResponse{
			Token:     token,
			ExpiresIn: expiresIn,
		})
	})
}

// clientIP extracts the client IP from the request. When trustProxy is true,
// it reads the first entry from X-Forwarded-For. Otherwise it uses RemoteAddr.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Take the first IP in the chain.
			if idx := strings.Index(xff, ","); idx != -1 {
				return strings.TrimSpace(xff[:idx])
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if host == "" {
		return "unknown"
	}
	return host
}
