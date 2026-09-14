//go:build e2e

package crud

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/visdomtech/orcacommon/tests/e2e/harness"
)

// --- Guest Session Tests ---
// These tests use fresh clients to isolate cookie jar behavior.

func TestGuest_CookieSetOnFirstVisit(t *testing.T) {
	client := harness.NewClientWithJar(t)
	req, _ := http.NewRequest("GET", stack.BaseURL+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}

	harness.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	cookie := harness.GetCookie(resp, "guest_session")
	if cookie == nil {
		t.Fatal("guest_session cookie not set on first visit")
	}
	if cookie.Value == "" {
		t.Fatal("guest_session cookie has empty value")
	}
	if parts := strings.SplitN(cookie.Value, ".", 2); len(parts) != 2 {
		t.Fatalf("guest_session cookie format = %q, want id.hmac", cookie.Value)
	}
}

func TestGuest_ValidCookieAccepted(t *testing.T) {
	client := harness.NewClientWithJar(t)

	// First visit.
	req1, _ := http.NewRequest("GET", stack.BaseURL+"/", nil)
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("first GET /: %v", err)
	}
	resp1.Body.Close()
	cookie1 := harness.GetCookie(resp1, "guest_session")
	if cookie1 == nil {
		t.Fatal("no guest_session cookie on first visit")
	}

	// Second visit — jar sends cookie, server accepts it.
	req2, _ := http.NewRequest("GET", stack.BaseURL+"/", nil)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("second GET /: %v", err)
	}

	harness.AssertStatus(t, resp2, http.StatusOK)
	resp2.Body.Close()

	// Cookie should not be re-issued.
	cookie2 := harness.GetCookie(resp2, "guest_session")
	if cookie2 != nil {
		t.Errorf("valid guest cookie was re-issued (new: %s, old: %s)", cookie2.Value, cookie1.Value)
	}
}

func TestGuest_TamperedCookieRejected(t *testing.T) {
	client := harness.NewClientWithJar(t)
	req, _ := http.NewRequest("GET", stack.BaseURL+"/", nil)
	req.Header.Set("Cookie", "guest_session=tampered.invalid")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	harness.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	newCookie := harness.GetCookie(resp, "guest_session")
	if newCookie == nil {
		t.Fatal("no new guest_session cookie set after tampered cookie")
	}
}

func TestGuest_CookieFlags(t *testing.T) {
	client := harness.NewClientWithJar(t)
	req, _ := http.NewRequest("GET", stack.BaseURL+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp.Body.Close()

	cookie := harness.GetCookie(resp, "guest_session")
	if cookie == nil {
		t.Fatal("guest_session cookie not set")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", cookie.SameSite)
	}
	if !cookie.HttpOnly {
		t.Error("HttpOnly = false, want true")
	}
	if !cookie.Secure {
		t.Error("Secure = false, want true")
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, want /", cookie.Path)
	}
}

// --- Token Issuance Tests ---
// These tests use the shared stack client (cookie jar persists cookies).

func TestIssuance_Success_200(t *testing.T) {
	// Visit page to set guest cookie in the jar.
	resp := stack.Do(t, "GET", "/", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}

	var out harness.IssuanceResponse
	resp = stack.Do(t, "POST", "/api/auth/ephemeral-token",
		map[string]string{"bot_token": "dummy-turnstile-token"}, &out)
	harness.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	if out.Token == "" {
		t.Fatal("token is empty")
	}
	if out.ExpiresIn <= 0 || out.ExpiresIn > 180 {
		t.Errorf("expires_in = %d, want [1, 180]", out.ExpiresIn)
	}
	if parts := strings.SplitN(out.Token, ".", 3); len(parts) != 3 {
		t.Fatalf("token format = %q, want JWT (3 segments)", out.Token)
	}
}

func TestIssuance_NoCookie_401(t *testing.T) {
	// Use a fresh client with no cookies.
	client := harness.NewClientWithJar(t)
	req, _ := http.NewRequest("POST", stack.BaseURL+"/api/auth/ephemeral-token",
		strings.NewReader(`{"bot_token":"dummy-turnstile-token"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST issuance: %v", err)
	}

	harness.AssertStatus(t, resp, http.StatusUnauthorized)
}

func TestIssuance_MissingBotToken_400(t *testing.T) {
	// Visit page to set guest cookie.
	resp := stack.Do(t, "GET", "/", nil, nil)
	resp.Body.Close()

	// Post with empty bot_token.
	req, _ := http.NewRequest("POST", stack.BaseURL+"/api/auth/ephemeral-token",
		strings.NewReader(`{"bot_token":""}`))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := stack.Client.Do(req)
	if err != nil {
		t.Fatalf("POST issuance: %v", err)
	}

	harness.AssertStatus(t, resp2, http.StatusBadRequest)
}

func TestIssuance_OversizedBody_400(t *testing.T) {
	// Visit page to set guest cookie.
	resp := stack.Do(t, "GET", "/", nil, nil)
	resp.Body.Close()

	bigBody := make([]byte, 2048)
	for i := range bigBody {
		bigBody[i] = 'A'
	}
	resp2 := stack.DoRaw(t, "POST", "/api/auth/ephemeral-token", bigBody, nil)
	harness.AssertStatus(t, resp2, http.StatusBadRequest)
	resp2.Body.Close()
}

func TestIssuance_BotVerificationFails_403(t *testing.T) {
	// Visit page to set guest cookie.
	resp := stack.Do(t, "GET", "/", nil, nil)
	resp.Body.Close()

	req, _ := http.NewRequest("POST", stack.BaseURL+"/api/auth/ephemeral-token-fail",
		strings.NewReader(`{"bot_token":"dummy-turnstile-token"}`))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := stack.Client.Do(req)
	if err != nil {
		t.Fatalf("POST issuance-fail: %v", err)
	}

	harness.AssertStatus(t, resp2, http.StatusForbidden)
}

func TestIssuance_WrongMethod_405(t *testing.T) {
	resp := stack.Do(t, "GET", "/api/auth/ephemeral-token", nil, nil)
	harness.AssertStatus(t, resp, http.StatusMethodNotAllowed)
}

// --- Protect Middleware Tests ---
// These use stack.GetToken which ensures the same cookie jar has the guest cookie.

func TestProtect_HappyPath_200(t *testing.T) {
	token := stack.GetToken(t, "/api/auth/ephemeral-token")

	var data map[string]string
	resp := stack.AuthRequest(t, "/api/public/data", token, &data)
	harness.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	if data["data"] != "public" {
		t.Errorf("data = %q, want %q", data["data"], "public")
	}
}

func TestProtect_NoAuthHeader_401(t *testing.T) {
	resp := stack.Do(t, "GET", "/api/public/data", nil, nil)
	harness.AssertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestProtect_MalformedHeader_401(t *testing.T) {
	resp := stack.DoWithHeaders(t, "GET", "/api/public/data",
		map[string]string{"Authorization": "Basic dXNlcjpwYXNz"}, nil, nil)
	harness.AssertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestProtect_ExpiredToken_401(t *testing.T) {
	past := time.Now().Unix() - 3600
	payload := fmt.Sprintf(
		`{"sub":"test-session","exp":%d,"iat":%d,"iss":"ephemeralauth","ip_hash":"xxx","ua_hash":"yyy","scopes":["public:read"]}`,
		past, past)
	expiredJWT := harness.CraftJWT(t, []byte(harness.SigningKey), `{"alg":"HS256","typ":"JWT"}`, payload)

	resp := stack.AuthRequest(t, "/api/public/data", expiredJWT, nil)
	harness.AssertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestProtect_SessionMismatch_403(t *testing.T) {
	// Get a token from session A (using a fresh client).
	clientA := harness.NewClientWithJar(t)
	req, _ := http.NewRequest("GET", stack.BaseURL+"/", nil)
	resp1, err := clientA.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp1.Body.Close()

	req2, _ := http.NewRequest("POST", stack.BaseURL+"/api/auth/ephemeral-token",
		strings.NewReader(`{"bot_token":"dummy-turnstile-token"}`))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := clientA.Do(req2)
	if err != nil {
		t.Fatalf("POST issuance: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		resp2.Body.Close()
		t.Fatalf("issuance status = %d, want 200", resp2.StatusCode)
	}
	var issuance harness.IssuanceResponse
	if err := parseJSON(resp2, &issuance); err != nil {
		resp2.Body.Close()
		t.Fatalf("decode: %v", err)
	}
	resp2.Body.Close()

	// Use token from A with session B (different fresh client).
	clientB := harness.NewClientWithJar(t)
	req3, _ := http.NewRequest("GET", stack.BaseURL+"/", nil)
	resp3, err := clientB.Do(req3)
	if err != nil {
		t.Fatalf("GET / (B): %v", err)
	}
	resp3.Body.Close()

	req4, _ := http.NewRequest("GET", stack.BaseURL+"/api/public/data", nil)
	req4.Header.Set("Authorization", "Bearer "+issuance.Token)
	resp4, err := clientB.Do(req4)
	if err != nil {
		t.Fatalf("GET /api/public/data: %v", err)
	}
	harness.AssertStatus(t, resp4, http.StatusForbidden)
	resp4.Body.Close()
}

func TestProtect_InsufficientScope_403(t *testing.T) {
	token := stack.GetToken(t, "/api/auth/ephemeral-token")
	resp := stack.AuthRequest(t, "/api/admin/data", token, nil)
	harness.AssertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

func TestProtect_TamperedToken_401(t *testing.T) {
	token := stack.GetToken(t, "/api/auth/ephemeral-token")
	tampered := harness.TamperJWT(token)
	resp := stack.AuthRequest(t, "/api/public/data", tampered, nil)
	harness.AssertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

// --- Helpers ---

func parseJSON(resp *http.Response, v any) error {
	return json.NewDecoder(resp.Body).Decode(v)
}
