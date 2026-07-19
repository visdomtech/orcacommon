# Extraction Plan: Reusable Code for `orcacommon`

Analysis of `/Users/jiangzhaohua/visdom/orcaagents` and `/Users/jiangzhaohua/visdom/auth-go` to identify reusable code suitable for extraction into this `orcacommon` shared library.

---

## High Priority (Zero/Low business coupling, immediate reuse)

### 1. `postgres/pgerr` — PostgreSQL Error Helpers

**Source:** `orcaagents/internal/pgerr/pgerr.go`

- `IsCode(err, code)`, `UniqueViolation`, `FKViolation` constants
- Complements the existing `postgres` package perfectly
- 21 lines, zero business deps, uses only `pgx/v5/pgconn` (already in orcacommon)

### 2. `ratelimit` — In-Memory Sliding-Window Rate Limiter

**Source:** `auth-go/internal/auth/ratelimit/ratelimit.go`

- Complete rate limiter with LRU eviction, configurable limits, testable `Clock` interface
- 273 lines, zero external deps (only `sync` + `time`)
- Already well-tested and production-proven

### 3. `httputil` — HTTP Middleware & Response Helpers

**Sources (deduplicated across both repos):**

| Function | Source | Notes |
|---|---|---|
| `WriteJSON` / `WriteError` / `WriteResult` | auth-go `middleware/result.go` | Extends existing `utils.WriteJSONResponse` with error + result-wrapper variants |
| `Logging()` middleware | auth-go `middleware/middleware.go` | Logs method, path, status, duration |
| `StripTrailingSlash()` | auth-go `middleware/middleware.go` | Generic URL normalization |
| `CORS(allowedOrigins []string)` | Both repos (duplicated) | Generalize to accept `[]string` instead of `*config.AppConfig` |
| `LogAndRespond(w, status, err, msg)` | orcaagents `handler/web/middleware.go` | Smart log-level based on status code |
| `Route`, `RouteParam`, `Routable` | orcaagents `handler/web/registry.go` | Generic API route metadata for OpenAPI generation |

### 4. `env` — Environment Detection Helpers

**Sources:** Duplicated in both `orcaagents/service/config/config.go` and `auth-go/internal/config/config.go`

- `IsProdEnv()`, `IsDevEnv()`, `IsLocalEnv()` — identical implementations in both repos
- `SetupGlobalJSONHandler()` — slog setup using existing `utils.SplitLevelHandler`

### 5. `email` — Mailgun Email Client

**Source:** `orcaagents/service/email/mailgun.go`

- Complete Mailgun client with attachment support
- 134 lines, zero business deps (only stdlib `net/http`, `mime/multipart`)

---

## Medium Priority (Generic core with some refactoring needed)

### 6. `auth/claims` — JWT Claims & Verification (Shared Contract)

**Sources:** Both repos implement JWT differently but share the **same claim keys and role constants**:

| Item | orcaagents `service/auth/claims.go` | auth-go `internal/auth/jwt.go` |
|---|---|---|
| Signing | HS256 (consumer side) | RS256 (issuer side) |
| `Claims` struct | Yes (session cookie) | `AccessTokenClaims` / `RefreshTokenClaims` |
| Role constants | `SYSTEM_ADMIN`, `CUSTOMER_ADMIN`, `USER` | Same via DB |
| Context helpers | `ClaimsFromContext`, `ContextWithClaims` | `VerifiedClaims` |

**Recommendation:** Extract shared types (`Claims`, role constants, context key helpers, `ParseAndVerifyJWT`) into `auth/claims`. The RS256 issuer (`TokenCreator`) stays in auth-go since it depends on `KeyStore`.

### 7. `auth/password` — Bcrypt Password Helpers

**Source:** `auth-go/internal/auth/password.go`

- `HashPassword(plain)` + `CheckPassword(plain, hash)` — 22 lines
- Only dep: `golang.org/x/crypto/bcrypt` (already indirect in orcacommon)

### 8. `auth/cookies` — Domain-Aware Cookie Helpers

**Source:** `auth-go/internal/auth/cookies.go`

- `SetTokenCookie`, `ClearAuthCookies`, `stripPort`, `extractRootDomain`
- Would need to generalize cookie names (currently hardcoded to `s`, `r`, `v`)

### 9. `crypto/signing` — HMAC-SHA256 Signed State

**Source:** `auth-go/internal/auth/oauth2/state.go`

- `EncodeState` / `DecodeState` with HMAC-SHA256 signing
- Generic pattern reusable for any tamper-proof state parameter (OAuth2, SAML relay state, CSRF tokens)

---

## Low Priority (Useful but more domain-specific)

### 10. `jobqueue` — River Job Queue Client

**Source:** `orcaagents/service/jobqueue/client.go`

- River client lifecycle (create, start, stop, migrate)
- Would add `river` as a new dependency to orcacommon — consider whether this is warranted

### 11. `auth/authclient` — Typed Auth-Go API Client

**Source:** `orcaagents/service/authclient/client.go` (336 lines)

- Only if multiple projects need to call auth-go's console API
- The generic JSON HTTP client pattern (`do`/`postJSON`/`getJSON`) could be extracted separately as `httputil/jsonclient`

---

## Summary Table

| Priority | Package | Effort | New Deps |
|---|---|---|---|
| **High** | `postgres/pgerr` | Trivial (copy) | None |
| **High** | `ratelimit` | Trivial (copy) | None |
| **High** | `httputil` (middleware + response + route registry) | Moderate (merge from both) | None |
| **High** | `env` (or add to `utils`) | Trivial (copy) | None |
| **High** | `email` | Trivial (copy) | None |
| **Medium** | `auth/claims` (shared types + HS256 verify) | Moderate (decouple) | None |
| **Medium** | `auth/password` | Trivial (copy) | `golang.org/x/crypto` |
| **Medium** | `auth/cookies` | Moderate (generalize) | None |
| **Medium** | `crypto/signing` | Minor (generalize) | None |
| **Low** | `jobqueue` | Trivial (copy) | `river` |
| **Low** | `auth/authclient` | Moderate | None |
