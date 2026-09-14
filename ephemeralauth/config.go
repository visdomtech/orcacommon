package ephemeralauth

import (
	"log/slog"
	"time"
)

// Config holds all configuration for the ephemeralauth package.
// Struct tags follow the caarlos0/env convention.
type Config struct {
	// SigningKey is the HMAC-SHA256 key used for JWT signing and guest
	// cookie HMAC. Required. Env: EPHEMERAL_SIGNING_KEY.
	SigningKey string `env:"EPHEMERAL_SIGNING_KEY"`

	// TokenTTLSeconds is the token lifetime in seconds. Clamped to [60, 180].
	// Default: 120. Env: EPHEMERAL_TOKEN_TTL_SECONDS.
	TokenTTLSeconds int `env:"EPHEMERAL_TOKEN_TTL_SECONDS"`

	// TurnstileSecret is the Cloudflare Turnstile secret for the default
	// BotVerifier implementation. If empty, the consumer must supply a
	// BotVerifier. Env: EPHEMERAL_TURNSTILE_SECRET.
	TurnstileSecret string `env:"EPHEMERAL_TURNSTILE_SECRET"`

	// TurnstileSitekey is the Cloudflare Turnstile sitekey for the
	// frontend widget. This is a public key (not secret) that the
	// frontend uses to render the challenge. Env: EPHEMERAL_TURNSTILE_SITEKEY.
	TurnstileSitekey string `env:"EPHEMERAL_TURNSTILE_SITEKEY"`

	// TrustProxy enables reading X-Forwarded-For for client IP extraction.
	// Default: false (use r.RemoteAddr). Env: EPHEMERAL_TRUST_PROXY.
	TrustProxy bool `env:"EPHEMERAL_TRUST_PROXY"`

	// Scopes is the default scope list issued to new tokens.
	// Default: ["public:read"]. Env: EPHEMERAL_SCOPES.
	Scopes []string `env:"EPHEMERAL_SCOPES"`

	// RequiredScopes is the list of scopes that must be present in a token
	// for the protection middleware to allow the request through. When empty,
	// any valid token is accepted. Env: EPHEMERAL_REQUIRED_SCOPES.
	RequiredScopes []string `env:"EPHEMERAL_REQUIRED_SCOPES"`
}

// TTL returns the configured TTL as a time.Duration, clamped to [60s, 180s].
func (c Config) TTL() time.Duration {
	return clampTTL(time.Duration(c.TokenTTLSeconds) * time.Second)
}

// Issuer creates an Issuer from this config.
func (c Config) Issuer() *Issuer {
	return NewIssuer([]byte(c.SigningKey), c.TTL())
}

// DefaultScopes returns the configured scopes or the default ["public:read"].
func (c Config) DefaultScopes() []string {
	if len(c.Scopes) == 0 {
		return []string{"public:read"}
	}
	return c.Scopes
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
