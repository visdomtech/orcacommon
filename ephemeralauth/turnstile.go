package ephemeralauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// turnstileEndpoint is the default Cloudflare Turnstile siteverify URL.
const turnstileEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// turnstileTimeout is the HTTP timeout for Turnstile verification requests.
const turnstileTimeout = 10 * time.Second

// TurnstileVerifier implements BotVerifier using Cloudflare Turnstile.
type TurnstileVerifier struct {
	secret     string
	endpoint   string
	httpClient *http.Client
}

// NewTurnstileVerifier creates a TurnstileVerifier with the given secret.
// Uses the default endpoint and a 10s timeout.
func NewTurnstileVerifier(secret string) *TurnstileVerifier {
	return &TurnstileVerifier{
		secret:   secret,
		endpoint: turnstileEndpoint,
		httpClient: &http.Client{
			Timeout: turnstileTimeout,
		},
	}
}

// turnstileResponse is the JSON response from the Turnstile siteverify API.
type turnstileResponse struct {
	Success bool `json:"success"`
}

// Verify implements BotVerifier. Fail-closed: any error or non-success
// response returns (false, error).
func (tv *TurnstileVerifier) Verify(ctx context.Context, token string, remoteIP string) (bool, error) {
	if token == "" {
		return false, fmt.Errorf("ephemeralauth: empty bot token")
	}

	form := url.Values{
		"secret":   {tv.secret},
		"response": {token},
	}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tv.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return false, fmt.Errorf("ephemeralauth: build turnstile request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tv.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("ephemeralauth: turnstile request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("ephemeralauth: turnstile returned status %d: %s", resp.StatusCode, body)
	}

	var result turnstileResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("ephemeralauth: decode turnstile response: %w", err)
	}

	if !result.Success {
		return false, nil
	}
	return true, nil
}
