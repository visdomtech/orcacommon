# Implementation Plan — Iteration 1

## Context
Building a complete e2e test suite for the ephemeralauth package (standalone + litespaserver integration) following orcaagents patterns. The ephemeralauth package has thorough unit tests but no e2e tests through a real HTTP server. We need: e2e server, harness, Taskfile, CRUD tests, workflow tests, and edge case tests.

## Architecture Decisions

### Server Design
- **Single e2e server** (`cmd/e2eserver/main.go`) that wires both standalone ephemeralauth routes AND litespaserver PublicAuth routes on one gorilla/mux router
- Server uses **httptest.NewServer** (not a separate process with containers) — ephemeralauth has no database dependency, so no testcontainers needed
- **Manifest file** at `/tmp/orcacommon-e2e-server.json` with `base_url` — harness reads this to connect
- Turnstile verification uses **live Cloudflare API with dummy keys** (always-pass, always-fail, token-expired secrets)

### Test Organization
Following the user's requirement: `tests/e2e/[user-facing-feature]/` with grouped test files.

```
tests/e2e/
├── harness/           # Shared infrastructure
│   ├── harness.go     # Stack struct, Start/Close
│   ├── runmain.go     # RunMain TestMain boilerplate
│   └── helpers.go     # Do/DoRaw HTTP helpers, cookie helpers
├── crud/              # CRUD tests (single endpoint)
│   ├── main_test.go   # TestMain
│   └── crud_test.go   # All CRUD tests
├── workflow/          # Workflow tests (multi-step)
│   ├── main_test.go   # TestMain
│   └── workflow_test.go # All workflow tests
└── edgecase/          # Edge case & security tests
    ├── main_test.go   # TestMain
    └── edgecase_test.go # All edge case tests
```

### Key Differences from orcaagents
- **No database** — ephemeralauth is stateless, no pgxpool needed in harness
- **No testcontainers** — no Postgres, no GCS, no Firestore
- **No auth-go devserver** — no user management, no workspace isolation
- **Simpler harness** — Stack holds only BaseURL + Client
- **No seed data step** — no demo data to populate

## Tasks

### Task 1: E2E Server (`cmd/e2eserver/main.go`)
**Gate:** Gate 1 (E2E Infrastructure)
**Files:**
- `cmd/e2eserver/main.go`

**Description:**
Create a standalone e2e server that:
1. Creates an ephemeralauth.Config with test signing key and Turnstile dummy keys
2. Creates an Issuer from the config
3. Sets up gorilla/mux router with:
   - `GET /` — SPA page route wrapped with GuestSession middleware (sets guest cookie)
   - `POST /api/auth/ephemeral-token` — IssuanceHandler with TurnstileVerifier (always-pass secret)
   - `POST /api/auth/ephemeral-token-fail` — IssuanceHandler with TurnstileVerifier (always-fail secret)
   - `GET /api/public/data` — Protected endpoint requiring "public:read" scope
   - `GET /api/admin/data` — Protected endpoint requiring "admin:write" scope
   - `GET /healthz` — Health check endpoint
4. Also wires litespaserver PublicAuth routes:
   - `GET /spa/*` — litespaserver ServeRoot with PublicAuth (guest cookie auto-set)
   - `POST /api/spa/auth/ephemeral-token` — litespaserver PublicAuthHandler()
   - `GET /api/spa/public/data` — litespaserver PublicAuthMiddleware()(apiHandler)
5. Starts httptest.NewServer
6. Writes manifest JSON to `/tmp/orcacommon-e2e-server.json` with `base_url`
7. Blocks on SIGTERM/SIGINT, graceful shutdown

**Acceptance Criteria:**
- Server compiles and starts
- Manifest file is written with valid base_url
- Health check returns 200
- Guest cookie is set on GET /
- Token issuance endpoint responds to POST

---

### Task 2: Test Harness (`tests/e2e/harness/`)
**Gate:** Gate 1 (E2E Infrastructure)
**Files:**
- `tests/e2e/harness/harness.go`
- `tests/e2e/harness/runmain.go`
- `tests/e2e/harness/helpers.go`

**Description:**
Create the test harness package with `//go:build e2e` tag:

**harness.go:**
- `Stack` struct: `BaseURL string`, `Client *http.Client` (no-redirect, cookie jar)
- `Start(ctx)` — reads manifest from `/tmp/orcacommon-e2e-server.json`, returns `*Stack`
- `Close(ctx)` — closes idle connections

**runmain.go:**
- `RunMain(m *testing.M, out **Stack)` — canonical TestMain boilerplate

**helpers.go:**
- `Do(t, method, path, body, out)` — HTTP request helper with JSON marshal/decode, cookie jar support
- `DoRaw(t, method, path, rawBody, out)` — raw body variant
- `DoWithHeaders(t, method, path, headers, body, out)` — custom headers variant (for IP/UA manipulation)
- `GetCookie(t, resp, name)` — extract cookie from response
- `ExtractToken(t, resp)` — extract JWT from issuance response body

**Acceptance Criteria:**
- Harness compiles with `//go:build e2e` tag
- Stack can read manifest and connect to server
- Do helper makes requests and decodes responses
- Cookie jar persists cookies across requests

---

### Task 3: Taskfile (`Taskfile.yml`)
**Gate:** Gate 1 (E2E Infrastructure)
**Files:**
- `Taskfile.yml`

**Description:**
Create Taskfile.yml with e2e lifecycle tasks:
- `e2e` — main entry: defer stop-server, start-server, wait-for-server, run tests
- `e2e:start-server` (internal) — build and boot e2e server in background
- `e2e:wait-for-server` (internal) — poll for manifest + healthz (60s timeout, shorter than orcaagents since no containers)
- `e2e:stop-server` (internal) — kill PID, clean up temp files

**Acceptance Criteria:**
- `task e2e` runs the full pipeline
- Server starts and stops cleanly
- Tests execute with `-race -tags=e2e -count=1`

---

### Task 4: CRUD Tests (`tests/e2e/crud/`)
**Gate:** Gate 2 (CRUD Tests)
**Files:**
- `tests/e2e/crud/main_test.go`
- `tests/e2e/crud/crud_test.go`

**Description:**
Write CRUD tests covering all individual endpoint behaviors:

**Guest Session (4 tests):**
- `TestGuest_CookieSetOnFirstVisit` — GET / → 200, guest_session cookie present with correct flags
- `TestGuest_ValidCookieAccepted` — GET / with valid cookie → 200, same cookie not re-issued
- `TestGuest_TamperedCookieRejected` — GET / with tampered cookie → new cookie issued
- `TestGuest_CookieFlags` — verify SameSite=Strict, HttpOnly, Secure, Path=/

**Token Issuance (6 tests):**
- `TestIssuance_Success_200` — valid cookie + valid bot_token → 200, JWT + expires_in
- `TestIssuance_NoCookie_401` — no cookie → 401
- `TestIssuance_MissingBotToken_400` — empty bot_token → 400
- `TestIssuance_OversizedBody_400` — body > 1KB → 400
- `TestIssuance_BotVerificationFails_403` — always-fail endpoint → 403
- `TestIssuance_WrongMethod_405` — GET instead of POST → 405

**Protect Middleware (7 tests):**
- `TestProtect_HappyPath_200` — valid token + cookie → 200
- `TestProtect_NoAuthHeader_401` — missing Authorization → 401
- `TestProtect_MalformedHeader_401` — non-Bearer → 401
- `TestProtect_ExpiredToken_401` — expired JWT → 401
- `TestProtect_SessionMismatch_403` — token from different session → 403
- `TestProtect_InsufficientScope_403` — token with public:read accessing admin:write endpoint → 403
- `TestProtect_TamperedToken_401` — modified JWT signature → 401

**Acceptance Criteria:**
- All 17 tests pass with `go test -race -tags=e2e -count=1`
- Tests use live Turnstile dummy keys
- Assertions cover status codes, response bodies, cookie attributes, JWT claims

---

### Task 5: Workflow Tests (`tests/e2e/workflow/`)
**Gate:** Gate 3 (Workflow Tests)
**Files:**
- `tests/e2e/workflow/main_test.go`
- `tests/e2e/workflow/workflow_test.go`

**Description:**
Write workflow tests covering multi-step user flows:

**Full Page-to-API Flow (1 test):**
- `TestWorkflow_FullFlow` — GET / → cookie → POST /api/auth/ephemeral-token → JWT → GET /api/public/data with Bearer + cookie → 200

**Token Refresh Flow (1 test):**
- `TestWorkflow_TokenRefresh` — get token → use token → wait for expiry (or use short TTL) → 401 → re-solve Turnstile → get new token → retry → 200

**LiteSPA PublicAuth Integration (2 tests):**
- `TestWorkflow_LiteSPA_GuestCookie` — GET /spa/ → guest cookie set by litespaserver
- `TestWorkflow_LiteSPA_FullFlow` — GET /spa/ → cookie → POST /api/spa/auth/ephemeral-token → JWT → GET /api/spa/public/data → 200

**Multi-Scope Flow (1 test):**
- `TestWorkflow_ScopeEnforcement` — get token with public:read scope → access /api/public/data → 200 → access /api/admin/data → 403

**Acceptance Criteria:**
- All 5 tests pass with `go test -race -tags=e2e -count=1`
- Tests chain multiple endpoints with state progression
- LiteSPA integration tests verify auto-wiring works end-to-end

---

### Task 6: Edge Case & Security Tests (`tests/e2e/edgecase/`)
**Gate:** Gate 4 (Edge Case & Security Tests)
**Files:**
- `tests/e2e/edgecase/main_test.go`
- `tests/e2e/edgecase/edgecase_test.go`

**Description:**
Write security edge case tests:

**Cookie Security (2 tests):**
- `TestEdge_TamperedCookieValue` — modify cookie value → new cookie issued (not crash)
- `TestEdge_CookieFromDifferentSession` — use cookie from one session with token from another → 403

**Token Security (4 tests):**
- `TestEdge_ExpiredToken` — use token past TTL → 401
- `TestEdge_AlgorithmNone` — craft alg=none JWT → 401
- `TestEdge_RS256Confusion` — craft RS256 header JWT → 401
- `TestEdge_MalformedJWT` — not-a-JWT string as Bearer token → 401

**Binding Violations (2 tests):**
- `TestEdge_UAMismatch` — get token with UA-A, use with UA-B → 403
- `TestEdge_IPMismatch` — get token from IP-A (via X-Forwarded-For with TrustProxy), use from IP-B → 403

**Turnstile Edge Cases (2 tests):**
- `TestEdge_TurnstileAlwaysFail` — always-fail secret → 403 on issuance
- `TestEdge_TurnstileTokenExpired` — token-expired secret → 403 on issuance

**Protocol Violations (3 tests):**
- `TestEdge_OversizedBody` — POST body > 1KB → 400
- `TestEdge_WrongMethod` — GET/PUT/DELETE on issuance → 405
- `TestEdge_EmptyAuthHeader` — Authorization: "" → 401

**Acceptance Criteria:**
- All 13 tests pass with `go test -race -tags=e2e -count=1`
- Security boundaries properly enforced
- No known attack vector untested

---

### Task 7: Full Suite Verification
**Gate:** Gate 5 (Full Suite Green)
**Description:**
- Run `task e2e` — verify exit code 0
- Run `go test -race ./...` — verify no regressions in existing unit tests
- Verify working tree clean

**Acceptance Criteria:**
- `task e2e` exits 0
- All existing tests still pass
- `git status` clean

## Task Dependencies

```
Task 1 (Server) ──→ Task 2 (Harness) ──→ Task 3 (Taskfile) ──→ Task 4 (CRUD)
                                                              ──→ Task 5 (Workflow)
                                                              ──→ Task 6 (Edge Case)
                                                              ──→ Task 7 (Full Suite)
```

Tasks 4, 5, 6 can be done in any order after Task 3, but WIP limit = 1 task at a time.

## Notes
- The e2e server needs TWO Turnstile verifier instances: one with always-pass secret (for success paths) and one with always-fail secret (for failure paths). These are mounted at different endpoints.
- For the token-expired Turnstile test, a third verifier with the token-expired secret is needed.
- The litespaserver integration requires an embedded FS or CDN mock — use `EmbeddedContent` with a minimal `fs.FS` containing a test `index.html`.
- For IP mismatch testing, the server must have `TrustProxy: true` on at least one protected route so X-Forwarded-For can simulate different client IPs.
- JWT TTL is clamped to [60, 180] seconds — for expired token tests, use the minimum 60s TTL and either wait or craft an expired token directly (preferred: craft with Issuer using a past expiry).
