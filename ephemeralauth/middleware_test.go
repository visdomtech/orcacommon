package ephemeralauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// issueTestToken creates a signed token for testing the protection middleware.
func issueTestToken(t *testing.T, key []byte, sessionID, ip, ua string, scopes []string, ttl time.Duration) string {
	t.Helper()
	iss := NewIssuer(key, ttl)
	ipHash := HashContext(key, ip)
	uaHash := HashContext(key, ua)
	token, _, err := iss.Issue(sessionID, ipHash, uaHash, scopes)
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return token
}

// makeSessionCookie creates a valid guest cookie and returns the cookie + session ID.
func makeSessionCookie(t *testing.T, key []byte) (*http.Cookie, string) {
	t.Helper()
	id, err := generateGuestID()
	if err != nil {
		t.Fatalf("generateGuestID: %v", err)
	}
	sessionID := encodeBase64URL(id)
	value := signGuestCookie(id, DeriveKey(key))
	return &http.Cookie{
		Name:  guestCookieName,
		Value: value,
	}, sessionID
}

func TestProtect_NoHeader_401(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	mw := Protect(iss, testKey, false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/data", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestProtect_MalformedHeader_401(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	mw := Protect(iss, testKey, false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Authorization", "Basic abc123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestProtect_ExpiredToken_401(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	mw := Protect(iss, testKey, false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Create an expired token.
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "session-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-10 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-70 * time.Second)),
			Issuer:    "ephemeralauth",
		},
		IPHash: "ip",
		UAHash: "ua",
		Scopes: []string{"public:read"},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := tok.SignedString(testKey)

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestProtect_BadSignature_401(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	mw := Protect(iss, testKey, false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cookie, sessionID := makeSessionCookie(t, testKey)
	token := issueTestToken(t, testKey, sessionID, "1.2.3.4", "TestAgent", []string{"public:read"}, 120*time.Second)

	// Tamper with the token signature by replacing it entirely.
	parts := splitToken(token)
	tampered := parts[0] + "." + parts[1] + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestProtect_SessionMismatch_403(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	mw := Protect(iss, testKey, false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Create a token bound to one session, but use a different cookie.
	_, sessionID := makeSessionCookie(t, testKey)
	token := issueTestToken(t, testKey, sessionID, "1.2.3.4", "TestAgent", []string{"public:read"}, 120*time.Second)

	// Use a different cookie for the request.
	differentCookie, _ := makeSessionCookie(t, testKey)

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.AddCookie(differentCookie)
	req.RemoteAddr = "1.2.3.4:1234"
	req.Header.Set("User-Agent", "TestAgent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestProtect_IPMismatch_403(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	mw := Protect(iss, testKey, false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cookie, sessionID := makeSessionCookie(t, testKey)
	// Token bound to IP "1.2.3.4".
	token := issueTestToken(t, testKey, sessionID, "1.2.3.4", "TestAgent", []string{"public:read"}, 120*time.Second)

	// Request comes from IP "5.6.7.8".
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.AddCookie(cookie)
	req.RemoteAddr = "5.6.7.8:1234"
	req.Header.Set("User-Agent", "TestAgent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestProtect_UAMismatch_403(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	mw := Protect(iss, testKey, false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cookie, sessionID := makeSessionCookie(t, testKey)
	token := issueTestToken(t, testKey, sessionID, "1.2.3.4", "TestAgent", []string{"public:read"}, 120*time.Second)

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.AddCookie(cookie)
	req.RemoteAddr = "1.2.3.4:1234"
	req.Header.Set("User-Agent", "DifferentAgent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestProtect_MissingScope_403(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	mw := Protect(iss, testKey, false, "form:submit") // requires form:submit
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cookie, sessionID := makeSessionCookie(t, testKey)
	token := issueTestToken(t, testKey, sessionID, "1.2.3.4", "TestAgent", []string{"public:read"}, 120*time.Second)

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.AddCookie(cookie)
	req.RemoteAddr = "1.2.3.4:1234"
	req.Header.Set("User-Agent", "TestAgent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestProtect_HappyPath(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	mw := Protect(iss, testKey, false, "public:read")
	var handlerCalled bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	cookie, sessionID := makeSessionCookie(t, testKey)
	token := issueTestToken(t, testKey, sessionID, "1.2.3.4", "TestAgent", []string{"public:read"}, 120*time.Second)

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.AddCookie(cookie)
	req.RemoteAddr = "1.2.3.4:1234"
	req.Header.Set("User-Agent", "TestAgent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !handlerCalled {
		t.Error("handler not called")
	}
}

// splitToken splits a JWT into its three parts.
func splitToken(token string) [3]string {
	var result [3]string
	i := 0
	start := 0
	for j := 0; j < len(token) && i < 3; j++ {
		if token[j] == '.' {
			result[i] = token[start:j]
			start = j + 1
			i++
		}
	}
	if i < 3 {
		result[i] = token[start:]
	}
	return result
}
