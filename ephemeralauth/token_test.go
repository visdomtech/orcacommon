package ephemeralauth

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testKey = []byte("test-signing-key-32bytes-long!!!")

func TestToken_ValidRoundTrip(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	token, expiresIn, err := iss.Issue("session-1", "ip-hash-1", "ua-hash-1", []string{"public:read"})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	if expiresIn != 120 {
		t.Fatalf("expires_in = %d, want 120", expiresIn)
	}

	claims, err := iss.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if claims.Subject != "session-1" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "session-1")
	}
	if claims.IPHash != "ip-hash-1" {
		t.Errorf("IPHash = %q, want %q", claims.IPHash, "ip-hash-1")
	}
	if claims.UAHash != "ua-hash-1" {
		t.Errorf("UAHash = %q, want %q", claims.UAHash, "ua-hash-1")
	}
	if len(claims.Scopes) != 1 || claims.Scopes[0] != "public:read" {
		t.Errorf("Scopes = %v, want [public:read]", claims.Scopes)
	}
}

func TestToken_TamperedSignatureRejected(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)
	token, _, err := iss.Issue("session-1", "ip-hash-1", "ua-hash-1", []string{"public:read"})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	// Replace the signature entirely with a fixed bogus value.
	parts := strings.Split(token, ".")
	tampered := parts[0] + "." + parts[1] + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	_, err = iss.Verify(tampered)
	if err == nil {
		t.Fatal("Verify() should reject tampered signature, got nil error")
	}
}

func TestToken_ExpiredRejected(t *testing.T) {
	iss := NewIssuer(testKey, 60*time.Second)

	// Create a token that's already expired by crafting claims manually.
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "session-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-10 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-70 * time.Second)),
			Issuer:    "ephemeralauth",
		},
		IPHash: "ip",
		UAHash: "ua",
		Scopes: []string{"public:read"},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(DeriveKey(testKey))
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}

	_, err = iss.Verify(signed)
	if err == nil {
		t.Fatal("Verify() should reject expired token, got nil error")
	}
}

func TestToken_OverLongTTlClamped(t *testing.T) {
	iss := NewIssuer(testKey, 300*time.Second) // exceeds 180s
	_, expiresIn, err := iss.Issue("s", "ip", "ua", []string{"public:read"})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	if expiresIn != 180 {
		t.Errorf("expires_in = %d, want 180 (clamped from 300s)", expiresIn)
	}
}

func TestToken_AlgorithmNoneRejected(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)

	// Craft a token with alg=none.
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "session-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(120 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "ephemeralauth",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	noneToken, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}

	_, err = iss.Verify(noneToken)
	if err == nil {
		t.Fatal("Verify() should reject alg=none token, got nil error")
	}
}

func TestToken_RS256ConfusionRejected(t *testing.T) {
	iss := NewIssuer(testKey, 120*time.Second)

	// Craft a token that claims RS256 but was signed with HMAC.
	// The verification should reject because the method whitelist is HS256 only.
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "session-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(120 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "ephemeralauth",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(DeriveKey(testKey))
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}

	// Tamper with the header to claim RS256.
	parts := strings.Split(signed, ".")
	// Create a header claiming RS256.
	header := `{"alg":"RS256","typ":"JWT"}`
	parts[0] = encodeBase64URL([]byte(header))
	fakeToken := strings.Join(parts, ".")

	_, err = iss.Verify(fakeToken)
	if err == nil {
		t.Fatal("Verify() should reject token claiming RS256, got nil error")
	}
}

func TestToken_TTLClampMin(t *testing.T) {
	iss := NewIssuer(testKey, 10*time.Second) // below 60s
	_, expiresIn, err := iss.Issue("s", "ip", "ua", []string{"public:read"})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	if expiresIn != 60 {
		t.Errorf("expires_in = %d, want 60 (clamped from 10s)", expiresIn)
	}
}

func TestToken_DefaultTTL(t *testing.T) {
	iss := NewIssuer(testKey, 0) // zero means default
	_, expiresIn, err := iss.Issue("s", "ip", "ua", []string{"public:read"})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	if expiresIn != 120 {
		t.Errorf("expires_in = %d, want 120 (default)", expiresIn)
	}
}

func TestConfig_TTL_Clamp(t *testing.T) {
	tests := []struct {
		seconds int
		want    time.Duration
	}{
		{0, 120 * time.Second},   // zero → default
		{10, 60 * time.Second},   // below min → 60s
		{120, 120 * time.Second}, // valid
		{300, 180 * time.Second}, // above max → 180s
	}
	for _, tt := range tests {
		cfg := Config{TokenTTLSeconds: tt.seconds}
		if got := cfg.TTL(); got != tt.want {
			t.Errorf("Config{TokenTTLSeconds: %d}.TTL() = %v, want %v", tt.seconds, got, tt.want)
		}
	}
}

func TestConfig_LogValue_RedactsSecrets(t *testing.T) {
	cfg := Config{
		SigningKey:      "super-secret-key-12345678901234",
		TurnstileSecret: "turnstile-secret-value",
	}
	val := cfg.LogValue()
	str := val.String()
	if strings.Contains(str, "super-secret-key") {
		t.Error("signing key leaked in LogValue output")
	}
	if strings.Contains(str, "turnstile-secret-value") {
		t.Error("turnstile secret leaked in LogValue output")
	}
	if !strings.Contains(str, "[REDACTED]") {
		t.Error("expected [REDACTED] in LogValue output")
	}
}

func TestConfig_LogValue_EmptyKeysNotRedacted(t *testing.T) {
	cfg := Config{}
	val := cfg.LogValue()
	str := val.String()
	if strings.Contains(str, "[REDACTED]") {
		t.Error("empty keys should not produce [REDACTED]")
	}
}

func TestDeriveKey_Produces32Bytes(t *testing.T) {
	derived := DeriveKey([]byte("short"))
	if len(derived) != 32 {
		t.Errorf("DeriveKey() length = %d, want 32", len(derived))
	}
}

func TestDeriveKey_Deterministic(t *testing.T) {
	a := DeriveKey([]byte("my-key"))
	b := DeriveKey([]byte("my-key"))
	if string(a) != string(b) {
		t.Error("DeriveKey() not deterministic")
	}
}

func TestDeriveKey_DifferentInputsDifferentOutput(t *testing.T) {
	a := DeriveKey([]byte("key-a"))
	b := DeriveKey([]byte("key-b"))
	if string(a) == string(b) {
		t.Error("DeriveKey() produced same output for different inputs")
	}
}

func TestDeriveKey_PanicsOnNilInput(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("DeriveKey(nil) should panic")
		}
	}()
	DeriveKey(nil)
}

func TestDeriveKey_PanicsOnEmptyInput(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("DeriveKey([]byte{}) should panic")
		}
	}()
	DeriveKey([]byte{})
}

func TestDeriveKey_LongInput(t *testing.T) {
	long := bytes.Repeat([]byte("a"), 10_000)
	derived := DeriveKey(long)
	if len(derived) != 32 {
		t.Errorf("DeriveKey(10KB) length = %d, want 32", len(derived))
	}
}

func TestDeriveKey_32ByteKeyPreserved(t *testing.T) {
	// A 32-byte key should be returned as-is for backward compatibility.
	raw := []byte("exactly-32-bytes-long-key!!!!!!!")
	if len(raw) != 32 {
		t.Fatalf("test setup error: raw key length = %d, want 32", len(raw))
	}
	derived := DeriveKey(raw)
	if string(derived) != string(raw) {
		t.Error("DeriveKey() should preserve 32-byte keys for backward compatibility")
	}
}

func TestNewIssuer_AcceptsShortKey(t *testing.T) {
	// Any non-empty key should work — no panic.
	iss := NewIssuer([]byte("short"), 120*time.Second)
	if iss == nil {
		t.Fatal("NewIssuer() returned nil for short key")
	}
	// Verify it can issue and verify tokens.
	tok, _, err := iss.Issue("s", "ip", "ua", []string{"public:read"})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	if _, err := iss.Verify(tok); err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
}

func TestNewIssuer_PanicsOnEmptyKey(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewIssuer() should panic on empty key")
		}
	}()
	NewIssuer([]byte{}, 120*time.Second)
}
