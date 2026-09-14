# Evidence Manifest — Iteration 1

## Gate Status

| Gate | Status | Evidence | Owner |
|------|--------|----------|-------|
| 1. Token Sign & Verify Unit | ✅ Pass | `ephemeralauth/token.go`, `ephemeralauth/token_test.go` | Engineer |
| 2. Guest Session Middleware | ✅ Pass | `ephemeralauth/guest.go`, `ephemeralauth/guest_test.go` | Engineer |
| 3. Token Issuance Endpoint | ✅ Pass | `ephemeralauth/issuance.go`, `ephemeralauth/botverifier.go`, `ephemeralauth/turnstile.go`, `ephemeralauth/issuance_test.go`, `ephemeralauth/turnstile_test.go` | Engineer |
| 4. Protection Middleware | ✅ Pass | `ephemeralauth/middleware.go`, `ephemeralauth/middleware_test.go` | Engineer |
| 5. End-to-End Integration | ✅ Pass | `ephemeralauth/integration_test.go` | Engineer |
| 6. LiteSPA Server Integration | ✅ Pass | `litespaserver/litespaserver.go`, `litespaserver/serve.go`, `litespaserver/serve_test.go` | Engineer |
| 7. Frontend Contract & Docs | ✅ Pass | `ephemeralauth/AGENTS.md`, `docs/adr-ephemeral-auth.md`, `AGENTS.md` | Documentation Engineer |

## Gate Verification Details

### Gate 1: Token Sign & Verify Unit
- **Algorithm pinning:** `jwt.WithValidMethods([]string{"HS256"})` + type assertion in keyfunc → `token.go:80-84`
- **Test: valid round-trip** → `TestToken_ValidRoundTrip` (asserts sub, ip_hash, ua_hash, scopes)
- **Test: tampered signature** → `TestToken_TamperedSignatureRejected` (flips last sig byte)
- **Test: expired** → `TestToken_ExpiredRejected` (manually crafted expired claims)
- **Test: over-long TTL clamped** → `TestToken_OverLongTTlClamped` (300s → 180s)
- **Test: wrong algorithm** → `TestToken_AlgorithmNoneRejected` + `TestToken_RS256ConfusionRejected`

### Gate 2: Guest Session Middleware
- **Cookie flags asserted in test body:** `TestGuest_CookieSetOnFirstVisit` asserts `SameSite=Strict`, `HttpOnly=true`, `Secure=true`, `Path="/"`
- **Valid cookie accepted:** `TestGuest_ValidCookieAccepted` (no re-issue)
- **Tampered cookie rejected:** `TestGuest_TamperedCookieRejected` (flips MAC byte, new cookie issued)
- **Missing cookie:** `TestGuest_MissingCookieRejectedOnAPIPaths` (GuestSessionID returns false)
- **HMAC verification:** `verifyGuestCookie` uses `hmac.Equal` (constant-time) → `guest.go:77`

### Gate 3: Token Issuance Endpoint
- **No cookie → 401:** `TestIssuance_NoCookie_401`
- **Missing bot_token → 400:** `TestIssuance_MissingBotToken_400`
- **Verifier false → 403:** `TestIssuance_VerifierReturnsFalse_403`
- **Verifier error → 403 (fail-closed):** `TestIssuance_VerifierReturnsError_403`
- **Success → 200 with binding claims:** `TestIssuance_Success_200` (decodes real JWT, asserts sub/ip_hash/ua_hash/scopes)
- **Turnstile success/failure/error/malformed:** All 4 scenarios in `turnstile_test.go`
- **expires_in ≤ 180:** Asserted in `TestIssuance_Success_200`

### Gate 4: Protection Middleware
- **No header → 401:** `TestProtect_NoHeader_401`
- **Malformed header → 401:** `TestProtect_MalformedHeader_401`
- **Expired → 401:** `TestProtect_ExpiredToken_401`
- **Bad signature → 401:** `TestProtect_BadSignature_401`
- **Session mismatch → 403:** `TestProtect_SessionMismatch_403`
- **IP mismatch → 403:** `TestProtect_IPMismatch_403`
- **UA mismatch → 403:** `TestProtect_UAMismatch_403`
- **Scope mismatch → 403:** `TestProtect_MissingScope_403`
- **Happy path:** `TestProtect_HappyPath` (handler reached, 200)
- **No I/O confirmed:** Test Engineer read `middleware.go` — only in-memory operations (JWT parse, HMAC, map lookup)

### Gate 5: End-to-End Integration
- **Real mux.Router:** `integration_test.go:29` — `mux.NewRouter()`
- **Real middlewares:** GuestSession, IssuanceHandler, Protect — all real
- **Only BotVerifier stubbed:** `&stubVerifier{pass: true}` — the only stub
- **Scenario 1 (no token → 401):** `TestIntegration/NoToken_401`
- **Scenario 2 (full flow → 200):** `TestIntegration/FullFlow_200` (page → cookie → issuance → protected route)
- **Scenario 3 (expired → 401):** `TestIntegration/ExpiredToken_401`
- **Scenario 4 (binding mismatch → 403):** `TestIntegration/DifferentBinding_403`

### Gate 6: LiteSPA Server Integration
- **Config field:** `Config.PublicAuth *ephemeralauth.Config` → `litespaserver.go:59`
- **Guest middleware wired:** `ServeRoot` applies `guestMiddleware` when set → `serve.go:113-118`
- **PublicAuthHandler():** Returns issuance handler → `serve.go:283-286`
- **PublicAuthMiddleware():** Returns protection middleware → `serve.go:289-297`
- **Wired path tested:** `TestServeRoot_PublicAuth_GuestCookieSet` (cookie set on SPA response)
- **Unwired path tested:** `TestServeRoot_NoPublicAuth_NoGuestCookie` (no cookie when PublicAuth nil)
- **Nil accessors:** `TestPublicAuthHandler_NilWhenNotConfigured` (both return nil)
- **Non-nil accessors:** `TestPublicAuthHandler_NonNilWhenConfigured` (both non-nil)
- **Secret redaction:** `TestConfig_LogValue_RedactsSecrets` (key/secret never in output, [REDACTED] present)

### Gate 7: Frontend Contract & Docs
- **Contract doc:** `ephemeralauth/AGENTS.md` "Frontend Integration Contract" section
  - Endpoint: `POST /api/auth/ephemeral-token` ✓
  - Request body: `{"bot_token": "..."}` ✓
  - Response: `{"token", "expires_in"}` ✓
  - Bearer usage: `Authorization: Bearer <token>` ✓
  - Refresh-retry-once: "401 → Re-solve → Refresh → Retry Once" ✓
  - Cookie requirement: `credentials: "include"` / same-origin ✓
- **Package guide:** `ephemeralauth/AGENTS.md` ✓
- **Root agents.md updated:** 5th sub-module entry added ✓
- **ADR:** `docs/adr-ephemeral-auth.md` covering:
  - JWT HS256 ✓
  - Stateless HMAC guest cookie ✓
  - BotVerifier + Turnstile ✓
  - ≤180s expiry ✓
  - No-JS-in-repo ✓
  - gorilla/mux justification ✓

## Return Shipments (Failed Gates)

None — all gates pass.

## Code Quality Findings
- Critical: 0
- Warning: 0
- Suggestion: 2 (non-blocking)

## Commits Reviewed
- `1427736`: feat(ephemeralauth): add token sign/verify core with HS256 algorithm pinning
- `70424f6`: feat(ephemeralauth): add guest session middleware with HMAC-signed cookie
- `063775f`: feat(ephemeralauth): add BotVerifier interface and Turnstile implementation
- `9a6646b`: feat(ephemeralauth): add token issuance handler with bot verification
- `5c34e1f`: feat(ephemeralauth): add protection middleware with stateless verification
- `c9ca05e`: test(ephemeralauth): add end-to-end integration test with gorilla/mux
- `bee3561`: feat(litespaserver): add ephemeral auth integration via PublicAuth config
- `b7e8501`: docs(ephemeralauth): add package guide, frontend contract, ADR, and root agents.md update
