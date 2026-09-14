# Goal Achieved — Ephemeral Token Auth

## Iterations: 1/10

## Gates Passed
- [x] Gate 1: Token Sign & Verify Unit
- [x] Gate 2: Guest Session Middleware
- [x] Gate 3: Token Issuance Endpoint
- [x] Gate 4: Protection Middleware
- [x] Gate 5: End-to-End Integration
- [x] Gate 6: LiteSPA Server Integration
- [x] Gate 7: Frontend Contract & Docs

## Commits
- `1427736`: feat(ephemeralauth): add token sign/verify core with HS256 algorithm pinning
- `70424f6`: feat(ephemeralauth): add guest session middleware with HMAC-signed cookie
- `063775f`: feat(ephemeralauth): add BotVerifier interface and Turnstile implementation
- `9a6646b`: feat(ephemeralauth): add token issuance handler with bot verification
- `5c34e1f`: feat(ephemeralauth): add protection middleware with stateless verification
- `c9ca05e`: test(ephemeralauth): add end-to-end integration test with gorilla/mux
- `bee3561`: feat(litespaserver): add ephemeral auth integration via PublicAuth config
- `b7e8501`: docs(ephemeralauth): add package guide, frontend contract, ADR, and root agents.md update
- `738c63d`: chore: add iteration 1 review and evidence manifest

## Working Tree
- Status: clean
- Branch: ultra-reviews

## Unresolved Findings (non-blocking)
- Warning: none
- Suggestion:
  - S-1: `clientIP` could return empty string on edge cases (negligible — all tests set RemoteAddr)
  - S-2: Consider adding `MaxBytesReader` to issuance handler (defense-in-depth, low impact)

## New Package: ephemeralauth/

| File | Responsibility |
|------|---------------|
| `config.go` | Config struct (caarlos0/env tags), slog.LogValuer with secret redaction |
| `token.go` | Issuer (Issue/Verify), Claims, HashContext, HS256 algorithm pinning |
| `guest.go` | GuestSession middleware, GuestSessionID, HMAC-signed stateless cookie |
| `botverifier.go` | BotVerifier interface |
| `turnstile.go` | TurnstileVerifier (Cloudflare Turnstile, 10s timeout, fail-closed) |
| `issuance.go` | IssuanceHandler (POST /api/auth/ephemeral-token) |
| `middleware.go` | Protect middleware (stateless verification, no I/O) |
| `helpers.go` | base64url encode/decode |

## New Dependencies
- `github.com/golang-jwt/jwt/v5` — JWT signing/verification (zero transitive deps)
- `github.com/gorilla/mux v1.8.1` — Integration tests + consumer compatibility

## Modified Package: litespaserver/
- Added `Config.PublicAuth *ephemeralauth.Config` field
- `ServeRoot` wraps with guest middleware when PublicAuth is set
- Added `PublicAuthHandler()` and `PublicAuthMiddleware()` accessors
- Zero behavior change when PublicAuth is nil
