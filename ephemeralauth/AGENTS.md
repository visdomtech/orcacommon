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
| `token.go` | Issuer (Issue/Verify), Claims, HashContext helper |
| `guest.go` | GuestSession middleware, GuestSessionID helper |
| `botverifier.go` | BotVerifier interface |
| `turnstile.go` | TurnstileVerifier (Cloudflare Turnstile HTTP impl) |
| `issuance.go` | IssuanceHandler (POST endpoint) |
| `middleware.go` | Protect middleware (stateless verification) |
| `helpers.go` | base64url encode/decode |

## Configuration

```go
cfg := ephemeralauth.Config{
    SigningKey:      os.Getenv("EPHEMERAL_SIGNING_KEY"),
    TokenTTLSeconds: 120,                          // clamped to [60, 180]
    TurnstileSecret: os.Getenv("EPHEMERAL_TURNSTILE_SECRET"), // optional
    TrustProxy:      false,                        // read X-Forwarded-For
    Scopes:          []string{"public:read"},
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
    ephemeralauth.Protect(issuer, signingKey, cfg.TrustProxy, "public:read")(apiHandler))
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

### Token Endpoint

```
POST /api/auth/ephemeral-token
Content-Type: application/json

{
  "bot_token": "<turnstile-response-token>"
}
```

**Requirements:**
- Must include the guest session cookie (`credentials: "include"` / same-origin)
- The `bot_token` is obtained from the Cloudflare Turnstile widget challenge

**Success Response (200):**
```json
{
  "token": "<JWT>",
  "expires_in": 120
}
```

**Error Responses:**
- `401` — No valid guest session cookie → solve/re-solve the Turnstile challenge first, then retry
- `400` — Missing or malformed `bot_token`
- `403` — Bot verification failed (low score or error)
- `405` — Method not allowed (only POST)

### Using the Token

```
Authorization: Bearer <token>
```

Include this header on all protected API requests.

### Cookie Requirement

The guest session cookie must be sent with every request to the token endpoint and protected API routes. Frontends must use `credentials: "include"` (fetch) or `withCredentials: true` (XMLHttpRequest) and ensure same-origin.

### Refresh Flow (401 → Re-solve → Refresh → Retry Once)

1. Protected API call returns `401`
2. Re-solve the Turnstile challenge (render widget again)
3. `POST /api/auth/ephemeral-token` with new `bot_token` + guest cookie
4. Retry the original API call with the new token

Do NOT retry more than once without user interaction.

### Token Lifetime

- `expires_in` is always ≤ 180 seconds
- Frontends should treat tokens as opaque — do not decode or cache beyond `expires_in`
- After a 401, always re-issue rather than reusing an expired token

## Security Notes

- JWT is HS256 only; algorithm pinning rejects `alg=none` and RS256 confusion
- Guest cookie: `SameSite=Strict; HttpOnly; Secure`
- Verification is fully stateless — no server-side session store or token revocation
- Context binding (IP hash + UA hash) prevents token sharing across devices/IPs
- Bot verification is fail-closed: verifier error → reject issuance
- Signing key is redacted in slog output via `slog.LogValuer`

## Testing

```bash
# Unit tests (no external deps)
go test -race ./ephemeralauth/...

# All tests
go test -race ./...
```

## Dependencies

- `github.com/golang-jwt/jwt/v5` — JWT signing/verification (zero transitive deps)
- `github.com/gorilla/mux` — Integration tests only; consumer-facing compatibility
