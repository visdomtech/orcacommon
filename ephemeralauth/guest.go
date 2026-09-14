package ephemeralauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// guestCookieName is the name of the guest session cookie.
const guestCookieName = "guest_session"

// guestIDLength is the byte length of the random guest session ID.
const guestIDLength = 16

// GuestSession returns middleware that ensures every request carries a valid
// HMAC-signed guest session cookie. If no valid cookie is present, a new one
// is generated and set via Set-Cookie.
func GuestSession(signingKey []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try to read and verify the existing cookie.
			if cookie, err := r.Cookie(guestCookieName); err == nil {
				if _, ok := verifyGuestCookie(cookie.Value, signingKey); ok {
					next.ServeHTTP(w, r)
					return
				}
			}

			// No valid cookie — generate a new guest session.
			id, err := generateGuestID()
			if err != nil {
				slog.Error("ephemeralauth: generate guest ID", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			value := signGuestCookie(id, signingKey)

			http.SetCookie(w, &http.Cookie{
				Name:     guestCookieName,
				Value:    value,
				Path:     "/",
				SameSite: http.SameSiteStrictMode,
				HttpOnly: true,
				Secure:   true,
			})

			next.ServeHTTP(w, r)
		})
	}
}

// GuestSessionID extracts and verifies the guest session cookie from the
// request. Returns the session ID and true if valid, empty string and false
// otherwise.
func GuestSessionID(r *http.Request, signingKey []byte) (string, bool) {
	cookie, err := r.Cookie(guestCookieName)
	if err != nil {
		return "", false
	}
	return verifyGuestCookie(cookie.Value, signingKey)
}

// signGuestCookie produces the cookie value: <base64url(id)>.<base64url(hmac)>.
func signGuestCookie(id []byte, key []byte) string {
	h := hmac.New(sha256.New, key)
	h.Write(id)
	mac := h.Sum(nil)
	return encodeBase64URL(id) + "." + encodeBase64URL(mac)
}

// verifyGuestCookie parses and validates the cookie value.
// Returns the base64url session ID string and true on success.
func verifyGuestCookie(value string, key []byte) (string, bool) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	idBytes, err := decodeBase64URL(parts[0])
	if err != nil {
		return "", false
	}
	macBytes, err := decodeBase64URL(parts[1])
	if err != nil {
		return "", false
	}

	h := hmac.New(sha256.New, key)
	h.Write(idBytes)
	expected := h.Sum(nil)

	if !hmac.Equal(macBytes, expected) {
		return "", false
	}

	return parts[0], true
}

// generateGuestID produces a crypto-random ID of guestIDLength bytes.
func generateGuestID() ([]byte, error) {
	b := make([]byte, guestIDLength)
	_, err := io.ReadFull(rand.Reader, b)
	return b, err
}
