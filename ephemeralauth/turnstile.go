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

// Cloudflare Turnstile official dummy test keys for development and testing.
// See: https://developers.cloudflare.com/turnstile/troubleshooting/testing/
const (
	// TurnstileTestAlwaysPassSitekey always triggers a passing challenge.
	TurnstileTestAlwaysPassSitekey = "1x00000000000000000000AA"

	// TurnstileTestAlwaysPassSecret always returns success: true.
	TurnstileTestAlwaysPassSecret = "1x0000000000000000000000000000000AA"

	// TurnstileTestAlwaysFailSitekey always triggers a failing challenge.
	TurnstileTestAlwaysFailSitekey = "2x00000000000000000000AB"

	// TurnstileTestAlwaysFailSecret always returns success: false.
	TurnstileTestAlwaysFailSecret = "2x0000000000000000000000000000000AA"

	// TurnstileTestForcesChallengeSitekey forces an interactive challenge.
	// No automated integration test exists — this key requires human
	// interaction to solve the challenge in a browser.
	TurnstileTestForcesChallengeSitekey = "3x00000000000000000000FF"

	// TurnstileTestTokenExpiredSecret returns a timeout-or-duplicate error.
	TurnstileTestTokenExpiredSecret = "3x0000000000000000000000000000000AA"
)

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
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 10,
				MaxConnsPerHost:     50,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// turnstileResponse is the JSON response from the Turnstile siteverify API.
type turnstileResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

// TurnstileResult holds the detailed outcome of a Turnstile verification.
type TurnstileResult struct {
	// Success indicates whether the challenge was solved correctly.
	Success bool
	// ErrorCodes contains error codes returned by Cloudflare (e.g.
	// "timeout-or-duplicate", "invalid-input-secret").
	ErrorCodes []string
}

// Verify implements BotVerifier. Fail-closed: any error or non-success
// response returns (false, error). Delegates to VerifyWithDetails.
func (tv *TurnstileVerifier) Verify(ctx context.Context, token string, remoteIP string) (bool, error) {
	result, err := tv.VerifyWithDetails(ctx, token, remoteIP)
	if err != nil {
		return false, err
	}
	return result.Success, nil
}

// VerifyWithDetails verifies the Turnstile token and returns the full result
// including any error codes from Cloudflare. Fail-closed: any HTTP or
// parsing error returns a non-nil error.
func (tv *TurnstileVerifier) VerifyWithDetails(ctx context.Context, token string, remoteIP string) (*TurnstileResult, error) {
	if token == "" {
		return nil, fmt.Errorf("ephemeralauth: empty bot token")
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
		return nil, fmt.Errorf("ephemeralauth: build turnstile request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tv.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ephemeralauth: turnstile request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ephemeralauth: turnstile returned status %d: %s", resp.StatusCode, body)
	}

	var raw turnstileResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("ephemeralauth: decode turnstile response: %w", err)
	}

	return &TurnstileResult{
		Success:    raw.Success,
		ErrorCodes: raw.ErrorCodes,
	}, nil
}
