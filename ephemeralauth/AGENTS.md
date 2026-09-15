# Ephemeral Auth Package

Short-lived (≤180s) stateless JWT gating for unauthenticated public API endpoints, with bot verification via Cloudflare Turnstile.

## Responsibility

Provides reusable middleware for:
1. **Guest session cookies** — stateless HMAC-signed cookies set on SPA page loads
2. **Token issuance** — `POST /api/auth/ephemeral-token` endpoint requiring guest cookie + bot verification
3. **API protection** — Bearer token middleware verifying signature, expiry, context binding (session/IP/UA), and scopes

All middleware is `func(http.Handler) http.Handler` — compatible with gorilla/mux and plain net/http.

## Files

| File | Responsibility |
|------|---------------|
| `config.go` | Config struct (caarlos0/env tags), slog.LogValuer with secret redaction |
| `token.go` | Issuer (Issue/Verify), Claims, DeriveKey (SHA-256 key normalisation), HashContext helper |
| `guest.go` | GuestSession middleware, GuestSessionID helper |
| `botverifier.go` | BotVerifier interface |
| `turnstile.go` | TurnstileVerifier (Cloudflare Turnstile HTTP impl), dummy test key constants, `VerifyWithDetails` |
| `issuance.go` | IssuanceHandler (POST endpoint, 1KB body limit) |
| `middleware.go` | Protect middleware (stateless verification, scope enforcement) |
| `helpers.go` | base64url encode/decode |
| `frontend-integration.md` | Frontend integration contract: Turnstile widget setup, token issuance, renewal flow, and reference JS implementation |

## Configuration

```go
cfg := ephemeralauth.Config{
    SigningKey:        os.Getenv("EPHEMERAL_SIGNING_KEY"),
    TokenTTLSeconds:   120,                                   // clamped to [60, 180]
    TurnstileSecret:   os.Getenv("EPHEMERAL_TURNSTILE_SECRET"),   // optional
    TurnstileSitekey:  os.Getenv("EPHEMERAL_TURNSTILE_SITEKEY"),  // frontend widget key (public)
    TrustProxy:        false,                                  // read X-Forwarded-For
    Scopes:            []string{"public:read"},                // scopes issued to new tokens
    RequiredScopes:    []string{"public:read"},                // scopes required by Protect middleware
}
```

## Standalone Usage (gorilla/mux)

```go
signingKey := []byte(cfg.SigningKey)
issuer := cfg.Issuer()

// Guest session on page routes.
router.Handle("/", ephemeralauth.GuestSession(signingKey)(pageHandler))

// Issuance endpoint.
router.Handle("/api/auth/ephemeral-token",
    ephemeralauth.IssuanceHandler(cfg, verifier, issuer))

// Protection on API routes.
router.Handle("/api/public/data",
    ephemeralauth.Protect(issuer, cfg.TrustProxy, "public:read")(apiHandler))
```

## LiteSPA Server Integration

Set `Config.PublicAuth` in litespaserver to auto-wire guest session middleware:

```go
server := litespaserver.NewServer(ctx, pool, litespaserver.Config{
    PublicAuth: &ephemeralauth.Config{...},
    // ... other fields
})
// Consumer mounts these on their own router:
mux.Handle("/api/auth/ephemeral-token", server.PublicAuthHandler())
mux.Handle("/api/public/*", server.PublicAuthMiddleware()(apiHandler))
```

## Frontend Integration Contract

See [frontend-integration.md](frontend-integration.md) for the complete frontend integration contract, including architecture overview, request flow diagram, Turnstile widget setup, token renewal flow, and a reference JavaScript implementation.

## Security Notes

- Any non-empty signing key is accepted; it is normalised to 32 bytes via SHA-256 (`DeriveKey`). Keys already 32 bytes are preserved as-is for backward compatibility. Keys shorter than 32 bytes produce a logged warning about reduced entropy.
- JWT is HS256 only; algorithm pinning rejects `alg=none` and RS256 confusion
- Guest cookie: `SameSite=Strict; HttpOnly; Secure`
- Verification is fully stateless — no server-side session store or token revocation
- Context binding (IP hash + UA hash) prevents token sharing across devices/IPs
- Bot verification is fail-closed: verifier error → reject issuance
- Signing key and Turnstile secret are redacted in slog output via `slog.LogValuer`
- Issuance handler enforces a 1KB body size limit via `http.MaxBytesReader`
- `RequiredScopes` in Config enables scope enforcement through the protection middleware

## Testing

```bash
# Unit tests (no external deps)
go test -race ./ephemeralauth/...

# Integration tests (requires network — hits live Cloudflare API)
go test -race -tags=integration ./ephemeralauth/...

# All tests
go test -race ./...
```

## Cloudflare Turnstile Dummy Test Keys

For local development and testing, Cloudflare provides official dummy test keys that always produce predictable results against the live siteverify endpoint:

| Constant | Value | Behavior |
|----------|-------|----------|
| `TurnstileTestAlwaysPassSitekey` | `1x00000000000000000000AA` | Always passes the challenge |
| `TurnstileTestAlwaysPassSecret` | `1x0000000000000000000000000000000AA` | Always returns `success: true` |
| `TurnstileTestAlwaysFailSitekey` | `2x00000000000000000000AB` | Always fails the challenge |
| `TurnstileTestAlwaysFailSecret` | `2x0000000000000000000000000000000AA` | Always returns `success: false` |
| `TurnstileTestForcesChallengeSitekey` | `3x00000000000000000000FF` | Forces interactive challenge |
| `TurnstileTestTokenExpiredSecret` | `3x0000000000000000000000000000000AA` | Returns `timeout-or-duplicate` |

**Development configuration example:**
```go
cfg := ephemeralauth.Config{
    SigningKey:       "dev-signing-key",
    TurnstileSecret:  ephemeralauth.TurnstileTestAlwaysPassSecret,
    TurnstileSitekey: ephemeralauth.TurnstileTestAlwaysPassSitekey,
}
```

These constants are exported from the package for use in development environments. Never use them in production.

## Dependencies

- `github.com/golang-jwt/jwt/v5` — JWT signing/verification (zero transitive deps)
- `github.com/gorilla/mux` — Integration tests only; consumer-facing compatibility
