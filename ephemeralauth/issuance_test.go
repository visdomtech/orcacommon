package ephemeralauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
		Value: signGuestCookie(id, signingKey),
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
