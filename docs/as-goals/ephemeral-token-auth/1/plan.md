# Plan — Iteration 1

**Goal:** `ephemeralauth` package: stateless short-lived JWT gating for public API endpoints, gorilla/mux-compatible middleware, litespaserver config integration.

**Codebase facts that shape this plan** (from exploration):
- No `gorilla/mux`, no JWT library in `go.mod` — both are new direct deps and must be justified (AGENTS.md §2).
- No middleware exists anywhere in the repo — this is the first. Signature: `func(http.Handler) http.Handler` (mux.MiddlewareFunc-compatible).
- `litespaserver` exposes only `Server.ServeRoot(w, r)`; the consumer owns routing. It has no mux to wire into — so "auto-wire" means litespaserver returns/accepts handlers the consumer mounts, OR litespaserver wraps its own `ServeRoot`. **Decision below.**
- Tests: plain `testing` + `httptest`, no testify, one-test-per-scenario named `TestX_Scenario`.
- Config: caarlos0/env tags, consumer parses; `slog.LogValuer` redaction via `slog.GroupValue` with `"[REDACTED]"`.

---

## Architecture Decisions (Architect's authority)

**AD-1: JWT library.** Use `github.com/golang-jwt/jwt/v5`. Justification: the goal mandates a signed JWT; implementing JWT by hand is a security risk. golang-jwt v5 is the de-facto standard, actively maintained, zero transitive deps. Pin HS256 via `jwt.WithValidMethods([]string{"HS256"})` at parse time (algorithm pinning — Security gate).

**AD-2: gorilla/mux.** Add `github.com/gorilla/mux v1.8.1`. Justification: the task explicitly requires gorilla/mux-compatible middleware and the integration test must build a real `mux.Router`. Middleware itself is written as `func(http.Handler) http.Handler` so it also works with plain net/http — mux is needed for the test + consumer convenience, not for the middleware to compile.

**AD-3: Stateless guest cookie.** Cookie value = `<base64url(random-16-byte-id)>.<base64url(hmac-sha256(id, signingKey))>`. Verify = recompute HMAC, `hmac.Equal` (constant time). No store. Same key as JWT signing (single secret, per goal).

**AD-4: Context binding.** Claims: `sub` = guest session ID, plus custom claims `ip_hash` and `ua_hash` (HMAC-SHA256 of client IP / User-Agent, keyed with signing key, truncated to 16 bytes, base64url). Hashing avoids putting raw IP/UA in the token. Middleware recomputes from the current request and compares with `hmac.Equal`. IP extraction: `r.RemoteAddr` host only by default; optional `TrustProxy` config to read `X-Forwarded-For` first entry (Security: spoofing risk documented, default off).

**AD-5: litespaserver integration shape.** litespaserver owns no router, so it cannot `mux.Use`. Integration = two things when `Config.PublicAuth *ephemeralauth.Config` is non-nil:
  (a) `Server.ServeRoot` is wrapped so every SPA page response sets/refreshes the guest cookie (guest middleware applied inside `ServeRoot`);
  (b) `Server` gains a method `PublicAuthHandler() http.Handler` returning the issuance handler (`POST /api/auth/ephemeral-token`) for the consumer to mount on their mux, plus `PublicAuthMiddleware() func(http.Handler) http.Handler` returning the protection middleware for the consumer to wrap protected API routes.
  This keeps litespaserver router-agnostic while making integration one config field + two mounts. When `PublicAuth` is nil: zero behavior change, zero new code paths.

**AD-6: Config.** `ephemeralauth.Config` fields: `SigningKey` (env `EPHEMERAL_SIGNING_KEY`, required, redacted), `TokenTTLSeconds` (env, default 120, clamped to [60,180]), `TurnstileSecret` (env, optional — if empty, the Turnstile verifier is not constructed; consumer must supply a `BotVerifier`), `TrustProxy` (env bool, default false), `Scopes` ([]string, default `["public:read"]`). Implements `slog.LogValuer` redacting `SigningKey` and `TurnstileSecret`.

**AD-7: BotVerifier interface.** `type BotVerifier interface { Verify(ctx context.Context, token string, remoteIP string) (bool, error) }`. bool = pass/fail (Turnstile returns success boolean; score-based providers can map score→bool internally). Fail-closed: `err != nil` OR `!ok` → reject issuance. Turnstile impl POSTs to `https://challenges.cloudflare.com/turnstile/v0/siteverify` with secret + response + remoteip, 10s timeout, fail-closed on non-2xx/parse error.

---

## Tasks (ordered, WIP = 1)

### Task 1 — Token sign/verify core → Gate 1
- **Files:** `ephemeralauth/token.go`, `ephemeralauth/token_test.go`, `go.mod` (add golang-jwt/v5)
- `Issuer` struct holding signing key + TTL. `Issue(sessionID, ipHash, uaHash string, scopes []string) (tokenString string, expiresIn int, err error)` — clamps exp to ≤180s. `Verify(tokenString string) (*Claims, error)` — `WithValidMethods(["HS256"])`, validates exp, returns typed claims.
- Claims struct embedding `jwt.RegisteredClaims` + `IPHash`, `UAHash`, `Scopes`.
- **Tests:** valid round-trip; tampered signature rejected; expired token rejected (issue with past exp via test hook); issuance with TTL>180 clamped to 180; token with `alg=none` and with RS256 header rejected (algorithm pinning).
- **Acceptance:** `go test -race ./ephemeralauth/ -run TestToken` green; every case a distinct `TestToken_<Scenario>`.

### Task 2 — Guest session middleware → Gate 2
- **Files:** `ephemeralauth/guest.go`, `ephemeralauth/guest_test.go`
- `GuestSession(cfg) func(http.Handler) http.Handler`. On request: if valid cookie present, pass through; else generate ID + HMAC, `Set-Cookie` (`SameSite=Strict; HttpOnly; Secure; Path=/`), pass through. Helper `GuestSessionID(r) (string, bool)` extracts+verifies the cookie for use by issuance handler and protection middleware. `hmac.Equal` for comparison.
- **Tests:** cookie set on first visit with all three flags asserted; valid cookie accepted (no re-issue); tampered cookie (flip a byte) rejected → new cookie issued; `GuestSessionID` returns false on missing/tampered.
- **Acceptance:** `go test -race ./ephemeralauth/ -run TestGuest` green.

### Task 3 — BotVerifier + Turnstile → Gate 3 (partial)
- **Files:** `ephemeralauth/botverifier.go`, `ephemeralauth/turnstile.go`, `ephemeralauth/turnstile_test.go`
- Interface per AD-7. `NewTurnstileVerifier(secret string) *TurnstileVerifier` with injectable `httpClient` + `endpoint` (for httptest). Fail-closed everywhere.
- **Tests:** httptest server returning `{"success":true}` → pass; `{"success":false}` → fail; HTTP 500 → error → fail-closed; malformed JSON → error → fail-closed.
- **Acceptance:** `go test -race ./ephemeralauth/ -run TestTurnstile` green.

### Task 4 — Token issuance handler → Gate 3
- **Files:** `ephemeralauth/issuance.go`, `ephemeralauth/issuance_test.go`
- `IssuanceHandler(cfg, verifier, issuer) http.Handler`. POST only (405 else). Reads guest cookie via `GuestSessionID` → 401 if absent/invalid. Reads bot token from JSON body `{"bot_token":"..."}` → 400 if missing. Calls `verifier.Verify` → 403 on fail/error. On success: compute ipHash/uaHash from request, `issuer.Issue(...)`, write 200 `{"token":"...","expires_in":N}`.
- **Tests (stub BotVerifier):** no cookie → 401; missing bot_token → 400; verifier returns false → 403; verifier returns error → 403 (fail-closed); success → 200, decode returned token and assert `sub`=session ID, `ip_hash`/`ua_hash` present, `expires_in`≤180, scopes present.
- **Acceptance:** `go test -race ./ephemeralauth/ -run TestIssuance` green; success test decodes the real JWT.

### Task 5 — Protection middleware → Gate 4
- **Files:** `ephemeralauth/middleware.go`, `ephemeralauth/middleware_test.go`
- `Protect(issuer, requiredScopes ...string) func(http.Handler) http.Handler`. Parses `Authorization: Bearer`. Missing/malformed → 401. `issuer.Verify` err (expired, bad sig, wrong alg) → 401. Context binding: recompute ipHash/uaHash from request + read guest cookie; mismatch on session/IP/UA → 403. Scope check: every required scope present in token's scopes → else 403. All stateless.
- **Tests:** one per branch — no header 401, malformed header 401, expired 401, bad signature 401, session mismatch 403, IP mismatch 403, UA mismatch 403, missing scope 403, happy path passes (handler reached).
- **Acceptance:** `go test -race ./ephemeralauth/ -run TestProtect` green; middleware body contains no I/O (Test Engineer confirms by reading).

### Task 6 — End-to-end integration test → Gate 5
- **Files:** `ephemeralauth/integration_test.go`, `go.mod` (add gorilla/mux)
- Build a real `mux.Router`: page route wrapped with `GuestSession`, `POST /api/auth/ephemeral-token` → real issuance handler (stub-passing BotVerifier only), `GET /api/public/data` wrapped with `Protect`. Run the four scenarios from Gate 5: no token → 401; full flow (GET page → capture cookie → POST issuance with cookie → GET protected with Bearer + cookie) → 200; expired token → 401; token from a second session/IP/UA → 403.
- **Acceptance:** real `mux.Router`, real middlewares (only BotVerifier stubbed), all four outcomes asserted. `go test -race ./ephemeralauth/ -run TestIntegration` green.

### Task 7 — litespaserver config integration → Gate 6
- **Files:** `litespaserver/litespaserver.go` (add `PublicAuth *ephemeralauth.Config` to `Config`), `litespaserver/serve.go` (wrap `ServeRoot` with guest middleware when set; add `PublicAuthHandler()` + `PublicAuthMiddleware()` methods), `litespaserver/serve_test.go` (new tests)
- When `PublicAuth` non-nil: `NewServer` constructs the guest middleware + issuer + handler once; `ServeRoot` applies guest cookie; the two accessor methods expose handler/middleware for the consumer's mux. When nil: identical behavior to today.
- **Tests:** with option set — SPA route response carries guest Set-Cookie, `PublicAuthHandler()` non-nil and reachable; with option unset — no Set-Cookie, accessors return nil. `slog.LogValuer` redaction test asserting `SigningKey`/`TurnstileSecret` never appear in log output.
- **Acceptance:** `go test -race ./litespaserver/` green; both wired and unwired paths tested.

### Task 8 — Docs: contract, package guide, ADR, root agents.md → Gate 7
- **Files:** `ephemeralauth/AGENTS.md` (package guide + frontend contract section), `docs/adr-ephemeral-auth.md`, root `agents.md` (add 5th sub-module entry to tree + numbered list)
- Contract section must include: `POST /api/auth/ephemeral-token`, request `{"bot_token":"..."}`, response `{"token","expires_in"}`, `Authorization: Bearer` usage, 401 → re-solve challenge → refresh → retry-once, cookie requirement (`credentials:"include"` / same-origin).
- ADR covers: JWT HS256, stateless HMAC guest cookie, BotVerifier+Turnstile, ≤180s expiry, no-JS-in-repo, AD-2 mux justification.
- **Acceptance:** Documentation Engineer confirms every contract element + all ADR decisions present.

---

## Dependency justification (for AGENTS.md §2)
- `github.com/golang-jwt/jwt/v5` — required for spec-compliant JWT; hand-rolling is a security risk. Zero transitive deps.
- `github.com/gorilla/mux` — task requirement; used by integration test and consumers. Single package, no transitive deps.

## Out of scope (unchanged from goal)
No JS in repo, no reCAPTCHA impl, no PASETO, no server-side session store/revocation, no rate limiting, no changes to postgres/email/utils.

## Definition of done for this iteration
All 8 tasks committed (conventional commits), `go build ./...` clean, `go test -race ./...` green, all 7 gates evidenced.
