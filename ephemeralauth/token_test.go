package ephemeralauth

import (
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

	// Flip the last character of the signature (after the second dot).
	parts := strings.Split(token, ".")
	sig := parts[2]
	if sig[len(sig)-1] == 'A' {
		sig = sig[:len(sig)-1] + "B"
	} else {
		sig = sig[:len(sig)-1] + "A"
	}
	tampered := parts[0] + "." + parts[1] + "." + sig

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
	signed, err := tok.SignedString(testKey)
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
	signed, err := tok.SignedString(testKey)
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
