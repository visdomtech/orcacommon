//go:build e2e

package workflow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/visdomtech/orcacommon/tests/e2e/harness"
)

// TestWorkflow_FullFlow exercises the complete page-to-API flow:
// visit page → get guest cookie → solve Turnstile → get JWT → call protected API.
func TestWorkflow_FullFlow(t *testing.T) {
	client := harness.NewClientWithJar(t)

	// Step 1: Visit page to get guest cookie.
	req, _ := http.NewRequest("GET", stack.BaseURL+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}
	cookie := harness.GetCookie(resp, "guest_session")
	if cookie == nil {
		t.Fatal("no guest_session cookie on first visit")
	}

	// Step 2: Request token issuance with dummy bot token.
	var issuance harness.IssuanceResponse
	req2, _ := http.NewRequest("POST", stack.BaseURL+"/api/auth/ephemeral-token",
		strings.NewReader(`{"bot_token":"dummy-turnstile-token"}`))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("POST issuance: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body := harness.ReadBody(t, resp2)
		t.Fatalf("POST issuance status = %d, want 200 (body: %s)", resp2.StatusCode, body)
	}
	if err := json.NewDecoder(resp2.Body).Decode(&issuance); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if issuance.Token == "" {
		t.Fatal("empty token")
	}
	if issuance.ExpiresIn <= 0 {
		t.Fatalf("expires_in = %d, want > 0", issuance.ExpiresIn)
	}

	// Step 3: Call protected API with the JWT.
	var data map[string]string
	req3, _ := http.NewRequest("GET", stack.BaseURL+"/api/public/data", nil)
	req3.Header.Set("Authorization", "Bearer "+issuance.Token)
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatalf("GET /api/public/data: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/public/data status = %d, want 200", resp3.StatusCode)
	}
	if err := json.NewDecoder(resp3.Body).Decode(&data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data["data"] != "public" {
		t.Errorf("data = %q, want %q", data["data"], "public")
	}
}

// TestWorkflow_TokenRefresh tests the token refresh flow:
// get token → use it → simulate expiry → re-solve → get new token → retry.
func TestWorkflow_TokenRefresh(t *testing.T) {
	// Step 1: Get a valid token.
	token := stack.GetToken(t, "/api/auth/ephemeral-token")

	// Step 2: Use the token successfully.
	var data map[string]string
	resp := stack.AuthRequest(t, "/api/public/data", token, &data)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first call status = %d, want 200", resp.StatusCode)
	}

	// Step 3: Simulate token expiry by crafting an expired JWT.
	// (Waiting 60+ seconds for real expiry is impractical in tests.)
	past := time.Now().Unix() - 3600
	payload := fmt.Sprintf(
		`{"sub":"test-session","exp":%d,"iat":%d,"iss":"ephemeralauth","ip_hash":"x","ua_hash":"y","scopes":["public:read"]}`,
		past, past)
	expiredJWT := harness.CraftJWT(t, []byte(harness.SigningKey), `{"alg":"HS256","typ":"JWT"}`, payload)

	resp = stack.AuthRequest(t, "/api/public/data", expiredJWT, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token status = %d, want 401", resp.StatusCode)
	}

	// Step 4: Re-solve Turnstile and get a new token.
	newToken := stack.GetToken(t, "/api/auth/ephemeral-token")

	// Step 5: Retry with the new token.
	resp = stack.AuthRequest(t, "/api/public/data", newToken, &data)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want 200", resp.StatusCode)
	}
}

// TestWorkflow_LiteSPA_GuestCookie verifies that the litespaserver PublicAuth
// integration sets the guest cookie on SPA page visits.
func TestWorkflow_LiteSPA_GuestCookie(t *testing.T) {
	client := harness.NewClientWithJar(t)
	req, _ := http.NewRequest("GET", stack.BaseURL+"/spa/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /spa/: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	cookie := harness.GetCookie(resp, "guest_session")
	if cookie == nil {
		t.Fatal("no guest_session cookie set by litespaserver")
	}
	if !cookie.HttpOnly {
		t.Error("HttpOnly = false, want true")
	}
}

// TestWorkflow_LiteSPA_FullFlow exercises the full flow through the
// litespaserver PublicAuth integration:
// GET /spa/ → cookie → POST /api/spa/auth/ephemeral-token → JWT → GET /api/spa/public/data.
func TestWorkflow_LiteSPA_FullFlow(t *testing.T) {
	// Step 1: Visit SPA page to get guest cookie.
	resp := stack.Do(t, "GET", "/spa/", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /spa/ status = %d, want 200", resp.StatusCode)
	}

	// Step 2: Request token through litespaserver issuance endpoint.
	var issuance harness.IssuanceResponse
	resp = stack.Do(t, "POST", "/api/spa/auth/ephemeral-token",
		map[string]string{"bot_token": "dummy-turnstile-token"}, &issuance)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body := harness.ReadBody(t, resp)
		t.Fatalf("POST /api/spa/auth/ephemeral-token status = %d, want 200 (body: %s)", resp.StatusCode, body)
	}
	if issuance.Token == "" {
		t.Fatal("empty token from litespaserver issuance")
	}

	// Step 3: Call litespaserver protected API.
	var data map[string]string
	resp = stack.AuthRequest(t, "/api/spa/public/data", issuance.Token, &data)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/spa/public/data status = %d, want 200", resp.StatusCode)
	}
	if data["data"] != "spa-public" {
		t.Errorf("data = %q, want %q", data["data"], "spa-public")
	}
}

// TestWorkflow_ScopeEnforcement verifies that scope checks work:
// token with public:read can access /api/public/data but not /api/admin/data.
func TestWorkflow_ScopeEnforcement(t *testing.T) {
	token := stack.GetToken(t, "/api/auth/ephemeral-token")

	// Should succeed for public:read endpoint.
	var data map[string]string
	resp := stack.AuthRequest(t, "/api/public/data", token, &data)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public:read endpoint status = %d, want 200", resp.StatusCode)
	}

	// Should fail for admin:write endpoint.
	resp = stack.AuthRequest(t, "/api/admin/data", token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin:write endpoint status = %d, want 403", resp.StatusCode)
	}

	// Get a token with admin:write scope and verify it can access both.
	adminToken := stack.GetToken(t, "/api/auth/ephemeral-token-admin")

	resp = stack.AuthRequest(t, "/api/admin/data", adminToken, &data)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin token on admin endpoint status = %d, want 200", resp.StatusCode)
	}
}
