package litespaserver

import (
	"strings"
	"testing"
)

func TestCSPRule_WithNonce(t *testing.T) {
	rule := cspRule(CSPConfig{}, "abc123")

	if !strings.Contains(rule, "'nonce-abc123'") {
		t.Errorf("expected nonce in style-src, got: %q", rule)
	}
	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self' https://hc-cdn.doublefin.com",
		"connect-src 'self' https://hc-cdn.doublefin.com",
	} {
		if !strings.Contains(rule, want) {
			t.Errorf("CSP rule missing %q\nfull: %q", want, rule)
		}
	}
}

func TestCSPRule_CustomSources(t *testing.T) {
	csp := CSPConfig{
		ScriptSrcs: []string{"'self'", "https://cdn.example.com"},
	}
	rule := cspRule(csp, "")
	if !strings.Contains(rule, "script-src 'self' https://cdn.example.com") {
		t.Errorf("custom script-src not applied, got: %q", rule)
	}
}

func TestCSPRule_NoNonce(t *testing.T) {
	rule := cspRule(CSPConfig{}, "")
	if strings.Contains(rule, "nonce-") {
		t.Errorf("did not expect a nonce, got: %q", rule)
	}
}

func TestGenerateNonce(t *testing.T) {
	n1, err := generateNonce(nonceLength)
	if err != nil {
		t.Fatalf("generateNonce: %v", err)
	}
	n2, err := generateNonce(nonceLength)
	if err != nil {
		t.Fatalf("generateNonce: %v", err)
	}

	if len(n1) != nonceLength {
		t.Errorf("nonce length = %d, want %d", len(n1), nonceLength)
	}
	for _, r := range n1 {
		if !strings.ContainsRune(nonceAlphabet, r) {
			t.Errorf("nonce contains out-of-alphabet rune %q", r)
		}
	}
	if n1 == n2 {
		t.Errorf("expected distinct nonces, both = %q", n1)
	}
}
