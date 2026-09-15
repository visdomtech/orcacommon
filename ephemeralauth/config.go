package ephemeralauth

import (
	"log/slog"
	"time"
)

// Config holds all configuration for the ephemeralauth package.
// Struct tags follow the caarlos0/env convention. The env key names are
// intentionally prefix-free so that Config can be embedded in a parent
// struct with an envPrefix tag:
//
//	type AppConfig struct {
//	    EphemeralAuth ephemeralauth.Config `envPrefix:"EPHEMERAL_"`
//	}
//
// With the prefix above, the effective env var for SigningKey becomes
// EPHEMERAL_SIGNING_KEY.
type Config struct {
	// SigningKey is the HMAC-SHA256 key used for JWT signing and guest
	// cookie HMAC. Required. Any non-empty value is accepted; it is
	// normalised to 32 bytes via SHA-256 (DeriveKey).
	// Env: SIGNING_KEY (prefix supplied by embedding struct).
	SigningKey string `env:"SIGNING_KEY"`

	// TokenTTLSeconds is the token lifetime in seconds. Clamped to [60, 180].
	// Default: 120. Env: TOKEN_TTL_SECONDS.
	TokenTTLSeconds int `env:"TOKEN_TTL_SECONDS" envDefault:"120"`

	// TurnstileSecret is the Cloudflare Turnstile secret for the default
	// BotVerifier implementation. If empty, the consumer must supply a
	// BotVerifier. Env: TURNSTILE_SECRET.
	TurnstileSecret string `env:"TURNSTILE_SECRET"`

	// TurnstileSitekey is the Cloudflare Turnstile sitekey for the
	// frontend widget. This is a public key (not secret) that the
	// frontend uses to render the challenge. Env: TURNSTILE_SITEKEY.
	TurnstileSitekey string `env:"TURNSTILE_SITEKEY"`

	// TrustProxy enables reading X-Forwarded-For for client IP extraction.
	// Default: false (use r.RemoteAddr). Env: TRUST_PROXY.
	TrustProxy bool `env:"TRUST_PROXY" envDefault:"false"`

	// Scopes is the default scope list issued to new tokens.
	// Default: ["public:read"]. Env: SCOPES.
	Scopes []string `env:"SCOPES" envDefault:"public:read"`

	// RequiredScopes is the list of scopes that must be present in a token
	// for the protection middleware to allow the request through. When empty,
	// any valid token is accepted. Env: REQUIRED_SCOPES.
	RequiredScopes []string `env:"REQUIRED_SCOPES"`
}

// TTL returns the configured TTL as a time.Duration, clamped to [60s, 180s].
func (c Config) TTL() time.Duration {
	return clampTTL(time.Duration(c.TokenTTLSeconds) * time.Second)
}

// Issuer creates an Issuer from this config.
func (c Config) Issuer() *Issuer {
	return NewIssuer([]byte(c.SigningKey), c.TTL())
}

// LogValue implements slog.LogValuer, redacting the signing key and
// Turnstile secret.
func (c Config) LogValue() slog.Value {
	signing := "[REDACTED]"
	if c.SigningKey == "" {
		signing = ""
	}
	turnstile := "[REDACTED]"
	if c.TurnstileSecret == "" {
		turnstile = ""
	}
	return slog.GroupValue(
		slog.String("signing_key", signing),
		slog.Int("token_ttl_seconds", c.TokenTTLSeconds),
		slog.String("turnstile_secret", turnstile),
		slog.String("turnstile_sitekey", c.TurnstileSitekey), // public key, not redacted
		slog.Bool("trust_proxy", c.TrustProxy),
		slog.Any("scopes", c.Scopes),
		slog.Any("required_scopes", c.RequiredScopes),
	)
}
