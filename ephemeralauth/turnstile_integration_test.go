//go:build integration

package ephemeralauth

import (
	"context"
	"slices"
	"testing"
)

// TestTurnstileIntegration_AlwaysPass verifies that the always-pass dummy
// secret returns success: true against the real Cloudflare siteverify endpoint.
func TestTurnstileIntegration_AlwaysPass(t *testing.T) {
	tv := NewTurnstileVerifier(TurnstileTestAlwaysPassSecret)

	// The always-pass secret accepts any non-empty token.
	ok, err := tv.Verify(context.Background(), "test-token-for-always-pass", "")
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if !ok {
		t.Error("Verify() = false, want true for always-pass dummy secret")
	}
}

// TestTurnstileIntegration_AlwaysFail verifies that the always-fail dummy
// secret returns success: false against the real Cloudflare siteverify endpoint.
func TestTurnstileIntegration_AlwaysFail(t *testing.T) {
	tv := NewTurnstileVerifier(TurnstileTestAlwaysFailSecret)

	ok, err := tv.Verify(context.Background(), "test-token-for-always-fail", "")
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if ok {
		t.Error("Verify() = true, want false for always-fail dummy secret")
	}
}

// TestTurnstileIntegration_TokenExpired verifies that the token-expired dummy
// secret returns error codes including "timeout-or-duplicate" when verified
// against the real Cloudflare siteverify endpoint.
func TestTurnstileIntegration_TokenExpired(t *testing.T) {
	tv := NewTurnstileVerifier(TurnstileTestTokenExpiredSecret)

	result, err := tv.VerifyWithDetails(context.Background(), "test-token-for-expired", "")
	if err != nil {
		t.Fatalf("VerifyWithDetails() error: %v", err)
	}
	if result.Success {
		t.Error("Success = true, want false for token-expired dummy secret")
	}
	if !slices.Contains(result.ErrorCodes, "timeout-or-duplicate") {
		t.Errorf("ErrorCodes = %v, want to contain 'timeout-or-duplicate'", result.ErrorCodes)
	}
}
