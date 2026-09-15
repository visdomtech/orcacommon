package ephemeralauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubVerifier is a test double for BotVerifier.
type stubVerifier struct {
	pass bool
	err  error
}

func (s *stubVerifier) Verify(_ context.Context, _ string, _ string) (bool, error) {
	return s.pass, s.err
}

func newTestConfig() Config {
	return Config{
		SigningKey:      "test-signing-key-32bytes-long!!!",
		TokenTTLSeconds: 120,
		Scopes:          []string{"public:read"},
	}
}

func makeGuestCookie(t *testing.T, signingKey []byte) *http.Cookie {
	t.Helper()
	id, err := generateGuestID()
	if err != nil {
		t.Fatalf("generateGuestID: %v", err)
	}
	return &http.Cookie{
		Name:  guestCookieName,
		Value: signGuestCookie(id, DeriveKey(signingKey)),
	}
}

func TestIssuance_NoCookie_401(t *testing.T) {
	cfg := newTestConfig()
	handler := IssuanceHandler(cfg, &stubVerifier{pass: true}, cfg.Issuer())

	req := httptest.NewRequest("POST", "/api/auth/ephemeral-token",
		bytes.NewBufferString(`{"bot_token":"test"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestIssuance_MissingBotToken_400(t *testing.T) {
	cfg := newTestConfig()
	handler := IssuanceHandler(cfg, &stubVerifier{pass: true}, cfg.Issuer())

	req := httptest.NewRequest("POST", "/api/auth/ephemeral-token",
		bytes.NewBufferString(`{}`))
	req.AddCookie(makeGuestCookie(t, []byte(cfg.SigningKey)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestIssuance_VerifierReturnsFalse_403(t *testing.T) {
	cfg := newTestConfig()
	handler := IssuanceHandler(cfg, &stubVerifier{pass: false}, cfg.Issuer())

	req := httptest.NewRequest("POST", "/api/auth/ephemeral-token",
		bytes.NewBufferString(`{"bot_token":"test"}`))
	req.AddCookie(makeGuestCookie(t, []byte(cfg.SigningKey)))
	req.RemoteAddr = "1.2.3.4:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestIssuance_VerifierReturnsError_403(t *testing.T) {
	cfg := newTestConfig()
	handler := IssuanceHandler(cfg, &stubVerifier{pass: false, err: errors.New("timeout")}, cfg.Issuer())

	req := httptest.NewRequest("POST", "/api/auth/ephemeral-token",
		bytes.NewBufferString(`{"bot_token":"test"}`))
	req.AddCookie(makeGuestCookie(t, []byte(cfg.SigningKey)))
	req.RemoteAddr = "1.2.3.4:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (fail-closed)", rec.Code)
	}
}

func TestIssuance_Success_200(t *testing.T) {
	cfg := newTestConfig()
	issuer := cfg.Issuer()
	handler := IssuanceHandler(cfg, &stubVerifier{pass: true}, issuer)

	req := httptest.NewRequest("POST", "/api/auth/ephemeral-token",
		bytes.NewBufferString(`{"bot_token":"valid-token"}`))
	req.AddCookie(makeGuestCookie(t, []byte(cfg.SigningKey)))
	req.RemoteAddr = "1.2.3.4:1234"
	req.Header.Set("User-Agent", "TestAgent/1.0")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp issuanceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token == "" {
		t.Error("token is empty")
	}
	if resp.ExpiresIn > 180 {
		t.Errorf("expires_in = %d, want <= 180", resp.ExpiresIn)
	}

	// Decode the real JWT and assert binding claims.
	claims, err := issuer.Verify(resp.Token)
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	if claims.Subject == "" {
		t.Error("token sub is empty")
	}
	if claims.IPHash == "" {
		t.Error("token ip_hash is empty")
	}
	if claims.UAHash == "" {
		t.Error("token ua_hash is empty")
	}
	if len(claims.Scopes) == 0 {
		t.Error("token scopes is empty")
	}

	// Verify the IP hash matches what we expect.
	expectedIPHash := HashContext([]byte(cfg.SigningKey), "1.2.3.4")
	if claims.IPHash != expectedIPHash {
		t.Errorf("IPHash = %q, want %q", claims.IPHash, expectedIPHash)
	}

	expectedUAHash := HashContext([]byte(cfg.SigningKey), "TestAgent/1.0")
	if claims.UAHash != expectedUAHash {
		t.Errorf("UAHash = %q, want %q", claims.UAHash, expectedUAHash)
	}
}

func TestIssuance_GetMethod_405(t *testing.T) {
	cfg := newTestConfig()
	handler := IssuanceHandler(cfg, &stubVerifier{pass: true}, cfg.Issuer())

	req := httptest.NewRequest("GET", "/api/auth/ephemeral-token", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestIssuance_OversizedBody_400(t *testing.T) {
	cfg := newTestConfig()
	handler := IssuanceHandler(cfg, &stubVerifier{pass: true}, cfg.Issuer())

	// Send a body larger than the 1KB limit.
	bigBody := make([]byte, 2048)
	for i := range bigBody {
		bigBody[i] = 'A'
	}
	req := httptest.NewRequest("POST", "/api/auth/ephemeral-token",
		bytes.NewReader(bigBody))
	req.AddCookie(makeGuestCookie(t, []byte(cfg.SigningKey)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for oversized body", rec.Code)
	}
}

func TestIssuance_OversizedValidJSON_413(t *testing.T) {
	cfg := newTestConfig()
	handler := IssuanceHandler(cfg, &stubVerifier{pass: true}, cfg.Issuer())

	// Create valid JSON that exceeds the 1KB limit.
	// A large bot_token value that's valid JSON but > 1024 bytes.
	bigToken := strings.Repeat("x", 1100)
	body := `{"bot_token":"` + bigToken + `"}`
	req := httptest.NewRequest("POST", "/api/auth/ephemeral-token",
		strings.NewReader(body))
	req.AddCookie(makeGuestCookie(t, []byte(cfg.SigningKey)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 for oversized valid JSON body", rec.Code)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		trustProxy bool
		xff        string
		remoteAddr string
		want       string
	}{
		{"trust_proxy_with_xff", true, "1.2.3.4", "10.0.0.1:1234", "1.2.3.4"},
		{"trust_proxy_with_xff_chain", true, "1.2.3.4, 5.6.7.8", "10.0.0.1:1234", "1.2.3.4"},
		{"trust_proxy_no_xff", true, "", "10.0.0.1:1234", "10.0.0.1"},
		{"no_trust_proxy_with_xff", false, "1.2.3.4", "10.0.0.1:1234", "10.0.0.1"},
		{"remote_addr_no_port", false, "", "10.0.0.1", "10.0.0.1"},
		{"remote_addr_unparseable", false, "", "not-valid", "not-valid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := clientIP(req, tt.trustProxy); got != tt.want {
				t.Errorf("clientIP(trustProxy=%v) = %q, want %q", tt.trustProxy, got, tt.want)
			}
		})
	}
}
