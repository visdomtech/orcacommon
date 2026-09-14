//go:build e2e

package edgecase

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/visdomtech/orcacommon/tests/e2e/harness"
)

// --- Cookie Security ---

func TestEdge_TamperedCookieValue(t *testing.T) {
	// Make a request with a tampered cookie value.
	req, _ := http.NewRequest("GET", stack.BaseURL+"/", nil)
	req.Header.Set("Cookie", "guest_session=tampered-value.hmac")
	client := harness.NewClientWithJar(t)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	harness.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// A new valid cookie should be issued.
	cookie := harness.GetCookie(resp, "guest_session")
	if cookie == nil {
		t.Fatal("no new guest_session cookie set after tampered cookie")
	}
}

func TestEdge_CookieFromDifferentSession(t *testing.T) {
	// Get a token from session A.
	token := stack.GetToken(t, "/api/auth/ephemeral-token")

	// Create a fresh client (session B) and visit the page to get a different cookie.
	newClient := harness.NewClientWithJar(t)
	req, _ := http.NewRequest("GET", stack.BaseURL+"/", nil)
	resp1, err := newClient.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp1.Body.Close()

	// Try to use session A's token with session B's cookie.
	req2, _ := http.NewRequest("GET", stack.BaseURL+"/api/public/data", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	resp2, err := newClient.Do(req2)
	if err != nil {
		t.Fatalf("GET /api/public/data: %v", err)
	}
	harness.AssertStatus(t, resp2, http.StatusForbidden)
	resp2.Body.Close()
}

// --- Token Security ---

func TestEdge_ExpiredToken(t *testing.T) {
	past := time.Now().Unix() - 3600
	payload := fmt.Sprintf(
		`{"sub":"test","exp":%d,"iat":%d,"iss":"ephemeralauth","ip_hash":"x","ua_hash":"y","scopes":["public:read"]}`,
		past, past)
	expiredJWT := harness.CraftJWT(t, []byte(harness.SigningKey), `{"alg":"HS256","typ":"JWT"}`, payload)

	resp := stack.AuthRequest(t, "/api/public/data", expiredJWT, nil)
	harness.AssertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestEdge_AlgorithmNone(t *testing.T) {
	// Craft a JWT with alg=none — should be rejected by algorithm pinning.
	header := `{"alg":"none","typ":"JWT"}`
	payload := `{"sub":"test","exp":9999999999,"iat":1700000000,"iss":"ephemeralauth","ip_hash":"x","ua_hash":"y","scopes":["public:read"]}`
	noneJWT := harness.CraftJWTNoSig(header, payload)

	resp := stack.AuthRequest(t, "/api/public/data", noneJWT, nil)
	harness.AssertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestEdge_RS256Confusion(t *testing.T) {
	// Craft a JWT with RS256 header — should be rejected by algorithm pinning.
	header := `{"alg":"RS256","typ":"JWT"}`
	payload := `{"sub":"test","exp":9999999999,"iat":1700000000,"iss":"ephemeralauth","ip_hash":"x","ua_hash":"y","scopes":["public:read"]}`
	rs256JWT := harness.CraftJWTNoSig(header, payload)

	resp := stack.AuthRequest(t, "/api/public/data", rs256JWT, nil)
	harness.AssertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestEdge_MalformedJWT(t *testing.T) {
	resp := stack.AuthRequest(t, "/api/public/data", "not-a-jwt", nil)
	harness.AssertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

// --- Binding Violations ---

func TestEdge_UAMismatch(t *testing.T) {
	// Get a token with a custom User-Agent.
	customUA := "TestBot/1.0"
	token := stack.GetTokenWithHeaders(t, "/api/auth/ephemeral-token",
		map[string]string{"User-Agent": customUA})

	// Use the token with a different User-Agent.
	resp := stack.AuthRequestWithHeaders(t, "/api/public/data", token,
		map[string]string{"User-Agent": "DifferentBot/2.0"}, nil)
	harness.AssertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

func TestEdge_IPMismatch(t *testing.T) {
	// Get a token bound to a specific IP via X-Forwarded-For.
	token := stack.GetTokenWithHeaders(t, "/api/auth/ephemeral-token",
		map[string]string{"X-Forwarded-For": "10.0.0.1"})

	// Use the token with a different X-Forwarded-For IP.
	resp := stack.AuthRequestWithHeaders(t, "/api/public/data", token,
		map[string]string{"X-Forwarded-For": "10.0.0.2"}, nil)
	harness.AssertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

// --- Turnstile Edge Cases ---

func TestEdge_TurnstileAlwaysFail(t *testing.T) {
	// Get guest cookie.
	resp := stack.Do(t, "GET", "/", nil, nil)
	resp.Body.Close()

	resp = stack.Do(t, "POST", "/api/auth/ephemeral-token-fail",
		map[string]string{"bot_token": "dummy-turnstile-token"}, nil)
	harness.AssertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

func TestEdge_TurnstileTokenExpired(t *testing.T) {
	// Get guest cookie.
	resp := stack.Do(t, "GET", "/", nil, nil)
	resp.Body.Close()

	resp = stack.Do(t, "POST", "/api/auth/ephemeral-token-expired",
		map[string]string{"bot_token": "dummy-turnstile-token"}, nil)
	harness.AssertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

// --- Protocol Violations ---

func TestEdge_OversizedBody(t *testing.T) {
	// Get guest cookie.
	resp := stack.Do(t, "GET", "/", nil, nil)
	resp.Body.Close()

	bigBody := make([]byte, 2048)
	for i := range bigBody {
		bigBody[i] = 'X'
	}

	resp = stack.DoRaw(t, "POST", "/api/auth/ephemeral-token", bigBody, nil)
	harness.AssertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestEdge_WrongMethod(t *testing.T) {
	// GET on issuance endpoint.
	resp := stack.Do(t, "GET", "/api/auth/ephemeral-token", nil, nil)
	harness.AssertStatus(t, resp, http.StatusMethodNotAllowed)
	resp.Body.Close()

	// PUT on issuance endpoint.
	resp = stack.DoRaw(t, "PUT", "/api/auth/ephemeral-token", []byte(`{"bot_token":"x"}`), nil)
	harness.AssertStatus(t, resp, http.StatusMethodNotAllowed)
	resp.Body.Close()

	// DELETE on issuance endpoint.
	resp = stack.Do(t, "DELETE", "/api/auth/ephemeral-token", nil, nil)
	harness.AssertStatus(t, resp, http.StatusMethodNotAllowed)
	resp.Body.Close()
}

func TestEdge_EmptyAuthHeader(t *testing.T) {
	resp := stack.DoWithHeaders(t, "GET", "/api/public/data",
		map[string]string{"Authorization": ""}, nil, nil)
	harness.AssertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}
