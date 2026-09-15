package ephemeralauth

import (
	"encoding/json"
	"errors"
	"log/slog"
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
	derivedKey := DeriveKey([]byte(cfg.SigningKey)) // derive once, not per request
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Require a valid guest session cookie.
		sessionID, ok := guestSessionIDWithKey(r, derivedKey)
		if !ok {
			http.Error(w, "unauthorized: no valid guest session", http.StatusUnauthorized)
			return
		}

		// Parse the bot token from the request body (limit to 1KB).
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var req issuanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "bad request: invalid JSON", http.StatusBadRequest)
			return
		}
		if req.BotToken == "" {
			http.Error(w, "bad request: bot_token required", http.StatusBadRequest)
			return
		}

		// Extract client IP.
		remoteIP := clientIP(r, cfg.TrustProxy)

		// Verify the bot token (fail-closed).
		passed, err := verifier.Verify(r.Context(), req.BotToken, remoteIP)
		if err != nil {
			slog.Warn("ephemeralauth: bot verification error",
				"error", err, "remote_ip", remoteIP)
			http.Error(w, "forbidden: bot verification failed", http.StatusForbidden)
			return
		}
		if !passed {
			slog.Warn("ephemeralauth: bot verification rejected",
				"remote_ip", remoteIP)
			http.Error(w, "forbidden: bot verification failed", http.StatusForbidden)
			return
		}

		// Compute context binding hashes.
		ipHash := hashContextWithKey(derivedKey, remoteIP)
		uaHash := hashContextWithKey(derivedKey, r.UserAgent())

		// Issue the token.
		token, expiresIn, err := issuer.Issue(sessionID, ipHash, uaHash, cfg.DefaultScopes())
		if err != nil {
			slog.Error("ephemeralauth: issue token", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(issuanceResponse{
			Token:     token,
			ExpiresIn: expiresIn,
		}); err != nil {
			slog.ErrorContext(r.Context(), "ephemeralauth: encode issuance response", "error", err)
		}
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
