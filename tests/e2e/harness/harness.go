//go:build e2e

// Package harness provides shared infrastructure for e2e tests of the
// ephemeralauth package. It reads the e2e server manifest and provides
// HTTP helper methods for making requests against the test server.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"testing"
	"time"
)

// manifestPath is the path where the e2e server writes its manifest.
const manifestPath = "/tmp/orcacommon-e2e-server.json"

// Stack holds the connection to the running e2e server.
type Stack struct {
	BaseURL string
	Client  *http.Client
}

// manifest is the JSON structure written by the e2e server.
type manifest struct {
	BaseURL string `json:"base_url"`
}

// Start reads the e2e server manifest and returns a connected Stack.
// The HTTP client has a cookie jar (for guest session cookies) and
// does NOT follow redirects.
func Start(_ context.Context) (*Stack, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.BaseURL == "" {
		return nil, fmt.Errorf("manifest has empty base_url")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}

	client := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		// Do not follow redirects — tests need to inspect 3xx responses.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &Stack{
		BaseURL: m.BaseURL,
		Client:  client,
	}, nil
}

// Close shuts down the Stack's HTTP client connections.
func (s *Stack) Close(_ context.Context) {
	s.Client.CloseIdleConnections()
}

// RunMain is the canonical TestMain boilerplate for e2e test packages.
// It starts the Stack, runs the test suite, and closes the Stack.
// out receives the Stack pointer for tests to access.
func RunMain(m *testing.M, out **Stack) {
	ctx := context.Background()
	stack, err := Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness: start failed: %v\n", err)
		os.Exit(1)
	}
	*out = stack
	code := m.Run()
	stack.Close(ctx)
	os.Exit(code)
}
