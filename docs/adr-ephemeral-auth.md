# ADR: Ephemeral Token Authentication

**Status:** Accepted
**Date:** 2026-09-14
**Deciders:** Architecture team

## Context

Downstream Visdom/Orca services expose unauthenticated public API endpoints (form submissions, public reads) that are vulnerable to automated scraping and spam. A shared, reusable mechanism is needed to gate these endpoints without introducing server-side state (no session store, no Redis, no token revocation list).

## Decision

Implement an `ephemeralauth` package in orcacommon providing short-lived (≤180s) stateless JWTs gated by:
1. A stateless HMAC-signed guest session cookie
2. Server-side bot verification via Cloudflare Turnstile

### Key Decisions

#### 1. JWT HS256 (Symmetric HMAC-SHA256)

**Decision:** Use HS256 with a single shared signing key.

**Rationale:** The token is issued and verified by the same service (no cross-service verification needed). HS256 is simpler, faster, and has a smaller key footprint than RSA. Algorithm pinning (`WithValidMethods(["HS256"])`) prevents algorithm confusion attacks (`alg=none`, RS256-confusion).

**Trade-off:** Any party with the signing key can forge tokens. This is acceptable because the key is server-side only and never exposed to frontends.

#### 2. Stateless HMAC Guest Cookie

**Decision:** Cookie value = `<base64url(random-16-byte-id)>.<base64url(hmac-sha256(id, signingKey))>`. Verification recomputes the HMAC — no server state.

**Rationale:** Avoids server-side session storage entirely. The same signing key used for JWTs signs the cookie, reducing configuration burden. `SameSite=Strict; HttpOnly; Secure` flags provide defense-in-depth.

#### 3. BotVerifier Interface + Turnstile Implementation

**Decision:** Abstract bot verification behind a `BotVerifier` interface with a Cloudflare Turnstile HTTP implementation.

**Rationale:** Interface allows consumers to plug in alternative providers (hCaptcha, score-based APIs). Turnstile is chosen as the reference implementation because it's widely used in the Visdom/Orca stack. Fail-closed semantics (error → reject) ensure safety.

**Trade-off:** No reCAPTCHA implementation in this pass. The interface makes it trivial to add later.

#### 4. Token Expiry ≤180 Seconds

**Decision:** Hard upper bound of 180s on token lifetime. Config clamps to [60s, 180s].

**Rationale:** Short-lived tokens reduce the window for replay attacks. Combined with context binding (IP hash + UA hash), stolen tokens become useless when the client moves to a different network or changes their User-Agent.

**Trade-off:** Frontend must implement a refresh flow (401 → re-solve challenge → re-issue → retry once). This adds complexity to the frontend but improves security.

#### 5. No JavaScript in Repository

**Decision:** This Go library contains no JavaScript. The frontend deliverable is a written integration contract document.

**Rationale:** orcacommon is a Go library. Frontend code lives in separate frontend repositories. The contract document (endpoint URL, request/response shape, Bearer usage, refresh-retry-once behavior, cookie requirements) gives frontend teams everything they need to implement against.

#### 6. gorilla/mux Dependency

**Decision:** Add `github.com/gorilla/mux v1.8.1` as a direct dependency.

**Rationale:** The goal explicitly requires gorilla/mux-compatible middleware. The integration test must build a real `mux.Router` to verify compatibility. Middleware is written as `func(http.Handler) http.Handler` so it also works with plain `net/http` — mux is needed for tests and consumer convenience, not for the middleware to compile.

**Trade-off:** One additional direct dependency. gorilla/mux has zero transitive dependencies, is actively maintained, and is already used by many downstream services.

### Rejected Alternatives

- **PASETO:** More secure by design, but not widely supported by client libraries. JWT is universally understood.
- **Server-side session store:** Adds operational complexity (Redis/DB dependency). The goal mandates stateless verification.
- **Per-endpoint rate limiting:** Separate concern. Can be layered independently.
- **Asymmetric keys (RS256/ES256):** Over-engineering for same-service issue+verify. Adds key management complexity.

## Consequences

### Positive
- Zero new infrastructure (no Redis, no session DB)
- Stateless verification scales horizontally without coordination
- Reusable across all Visdom/Orca services
- Clean separation: middleware works standalone or via litespaserver integration

### Negative
- Frontend teams must implement the refresh flow
- Context binding (IP/UA) may cause false rejections for users behind load-balanced proxies (mitigated by `TrustProxy` config)
- Single signing key means compromise requires key rotation across all services

## Implementation

- **Package:** `ephemeralauth/`
- **Files:** config.go, token.go, guest.go, botverifier.go, turnstile.go, issuance.go, middleware.go, helpers.go
- **Dependencies:** `golang-jwt/jwt/v5`, `gorilla/mux`
- **Integration:** `litespaserver.Config.PublicAuth` field
- **Tests:** Unit tests per component + gorilla/mux integration test + litespaserver integration tests
