//go:build e2e

package harness

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"
)

// Do makes an HTTP request to the e2e server with a JSON body and decodes
// the response into out (if non-nil). Returns the raw response for header
// and cookie inspection.
func (s *Stack) Do(t *testing.T, method, path string, body any, out any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, s.BaseURL+path, bodyReader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.Client.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}

	if out != nil && resp.Body != nil {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			t.Fatalf("decode response %s %s: %v", method, path, err)
		}
	}

	return resp
}

// DoRaw makes an HTTP request with a raw body (not JSON-encoded).
func (s *Stack) DoRaw(t *testing.T, method, path string, rawBody []byte, out any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if rawBody != nil {
		bodyReader = bytes.NewReader(rawBody)
	}

	req, err := http.NewRequest(method, s.BaseURL+path, bodyReader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	resp, err := s.Client.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}

	if out != nil && resp.Body != nil {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			t.Fatalf("decode response %s %s: %v", method, path, err)
		}
	}

	return resp
}

// DoWithHeaders makes an HTTP request with custom headers.
func (s *Stack) DoWithHeaders(t *testing.T, method, path string, headers map[string]string, body any, out any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, s.BaseURL+path, bodyReader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := s.Client.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}

	if out != nil && resp.Body != nil {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			t.Fatalf("decode response %s %s: %v", method, path, err)
		}
	}

	return resp
}

// DoNoCookie makes an HTTP request with a fresh client (no cookie jar),
// so no guest session cookie is sent. Useful for testing 401 on issuance.
func (s *Stack) DoNoCookie(t *testing.T, method, path string, body any, out any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, s.BaseURL+path, bodyReader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: s.Client.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}

	if out != nil && resp.Body != nil {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			t.Fatalf("decode response %s %s: %v", method, path, err)
		}
	}

	return resp
}

// GetCookie extracts a named cookie from the response. Returns nil if not found.
func GetCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ReadBody reads and returns the full response body as a string.
func ReadBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

// IssuanceResponse is the JSON shape of the token issuance response.
type IssuanceResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

// GetToken is a convenience that performs the full token issuance flow:
// visit the page to get a guest cookie, then POST to the issuance endpoint
// with a dummy bot token. Returns the JWT string.
func (s *Stack) GetToken(t *testing.T, issuancePath string) string {
	t.Helper()

	// Step 1: Visit page to get guest cookie.
	resp := s.Do(t, "GET", "/", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}

	// Step 2: Request token issuance.
	var out IssuanceResponse
	resp = s.Do(t, "POST", issuancePath, map[string]string{
		"bot_token": "dummy-turnstile-token",
	}, &out)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s status = %d, want 200", issuancePath, resp.StatusCode)
	}
	if out.Token == "" {
		t.Fatalf("POST %s returned empty token", issuancePath)
	}

	return out.Token
}

// GetTokenWithHeaders is like GetToken but allows setting custom headers
// on the issuance request (e.g., X-Forwarded-For for IP binding tests).
func (s *Stack) GetTokenWithHeaders(t *testing.T, issuancePath string, headers map[string]string) string {
	t.Helper()

	// Step 1: Visit page to get guest cookie.
	resp := s.Do(t, "GET", "/", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}

	// Step 2: Request token issuance with custom headers.
	var out IssuanceResponse
	resp = s.DoWithHeaders(t, "POST", issuancePath, headers, map[string]string{
		"bot_token": "dummy-turnstile-token",
	}, &out)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body := ReadBody(t, resp)
		t.Fatalf("POST %s status = %d, want 200 (body: %s)", issuancePath, resp.StatusCode, body)
	}
	if out.Token == "" {
		t.Fatalf("POST %s returned empty token", issuancePath)
	}

	return out.Token
}

// AuthRequest makes a GET request to a protected endpoint with a Bearer token.
func (s *Stack) AuthRequest(t *testing.T, path, token string, out any) *http.Response {
	t.Helper()
	return s.DoWithHeaders(t, "GET", path, map[string]string{
		"Authorization": "Bearer " + token,
	}, nil, out)
}

// AuthRequestWithHeaders makes a GET request to a protected endpoint with
// a Bearer token and additional custom headers.
func (s *Stack) AuthRequestWithHeaders(t *testing.T, path, token string, headers map[string]string, out any) *http.Response {
	t.Helper()
	allHeaders := map[string]string{
		"Authorization": "Bearer " + token,
	}
	for k, v := range headers {
		allHeaders[k] = v
	}
	return s.DoWithHeaders(t, "GET", path, allHeaders, nil, out)
}

// NewClientWithJar creates a fresh HTTP client with a new cookie jar,
// useful for simulating a different user session.
func NewClientWithJar(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// CraftJWT creates a JWT with custom header and payload JSON strings,
// signed with HMAC-SHA256 using the given key. Used for testing algorithm
// confusion and expiry attacks.
func CraftJWT(t *testing.T, signingKey []byte, headerJSON, payloadJSON string) string {
	t.Helper()
	h := base64URLEncode([]byte(headerJSON))
	p := base64URLEncode([]byte(payloadJSON))
	mac := hmacSHA256(signingKey, h+"."+p)
	sig := base64URLEncode(mac)
	return h + "." + p + "." + sig
}

// CraftJWTNoSig creates a JWT with custom header and payload JSON strings
// but no valid signature. Used for testing alg=none attacks.
func CraftJWTNoSig(headerJSON, payloadJSON string) string {
	h := base64URLEncode([]byte(headerJSON))
	p := base64URLEncode([]byte(payloadJSON))
	return h + "." + p + "."
}

func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func hmacSHA256(key []byte, message string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(message))
	return h.Sum(nil)
}

// SigningKey is the test signing key (must match the e2e server constant).
const SigningKey = "e2e-test-signing-key-must-be-long-enough"

// TamperJWT flips a character in the payload section of a JWT to invalidate
// the signature. Returns the tampered JWT string.
func TamperJWT(token string) string {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return token
	}
	// Flip a character in the payload.
	payload := []byte(parts[1])
	if len(payload) > 0 {
		if payload[0] == 'A' {
			payload[0] = 'B'
		} else {
			payload[0] = 'A'
		}
	}
	return parts[0] + "." + string(payload) + "." + parts[2]
}
