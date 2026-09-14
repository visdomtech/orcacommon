# Evidence Manifest — Iteration 1

## Gate Status

| Gate | Status | Evidence | Owner |
|------|--------|----------|-------|
| Gate 1: E2E Infrastructure | ✅ Pass | `cmd/e2eserver/main.go`, `tests/e2e/harness/harness.go`, `tests/e2e/harness/helpers.go`, `Taskfile.yml` | Engineer |
| Gate 2: CRUD Tests | ✅ Pass | `tests/e2e/crud/crud_test.go` (17 tests), `tests/e2e/crud/main_test.go` | Engineer |
| Gate 3: Workflow Tests | ✅ Pass | `tests/e2e/workflow/workflow_test.go` (5 tests), `tests/e2e/workflow/main_test.go` | Engineer |
| Gate 4: Edge Case & Security Tests | ✅ Pass | `tests/e2e/edgecase/edgecase_test.go` (13 tests), `tests/e2e/edgecase/main_test.go` | Engineer |
| Gate 5: Full Suite Green | ✅ Pass | `task e2e` exit 0, `go test -race ./...` green, `git status` clean | Engineer |

## Gate Details

### Gate 1: E2E Infrastructure
- `cmd/e2eserver/main.go` — standalone e2e server wiring standalone ephemeralauth + litespaserver PublicAuth on gorilla/mux
- `tests/e2e/harness/harness.go` — Stack struct with Start/Close, RunMain, manifest reading, cookie-jar client
- `tests/e2e/harness/helpers.go` — Do/DoRaw/DoWithHeaders/DoNoCookie, GetCookie, ReadBody, GetToken, AuthRequest, CraftJWT, TamperJWT
- `Taskfile.yml` — `task e2e` with server lifecycle: start, wait-for-server (healthz poll), test, stop (defer)
- Server writes manifest to `/tmp/orcacommon-e2e-server.json` with `base_url`
- `task e2e` boots server, waits for readiness, runs tests, stops server — exit 0

### Gate 2: CRUD Tests (17 tests)
- Guest session (4): CookieSetOnFirstVisit, ValidCookieAccepted, TamperedCookieRejected, CookieFlags
- Token issuance (6): Success_200, NoCookie_401, MissingBotToken_400, OversizedBody_400, BotVerificationFails_403, WrongMethod_405
- Protect middleware (7): HappyPath_200, NoAuthHeader_401, MalformedHeader_401, ExpiredToken_401, SessionMismatch_403, InsufficientScope_403, TamperedToken_401
- All use live Cloudflare Turnstile dummy keys (always-pass, always-fail)

### Gate 3: Workflow Tests (5 tests)
- FullFlow: GET / → cookie → POST issuance → JWT → GET /api/public/data → 200
- TokenRefresh: get token → use → craft expired JWT → 401 → re-issue → retry → 200
- LiteSPA_GuestCookie: GET /spa/ → guest cookie set by litespaserver
- LiteSPA_FullFlow: GET /spa/ → cookie → POST /api/spa/auth/ephemeral-token → JWT → GET /api/spa/public/data → 200
- ScopeEnforcement: public:read token → /api/public/data (200), /api/admin/data (403); admin:write token → /api/admin/data (200)

### Gate 4: Edge Case & Security Tests (13 tests)
- Cookie: TamperedCookieValue, CookieFromDifferentSession
- Token: ExpiredToken, AlgorithmNone, RS256Confusion, MalformedJWT
- Binding: UAMismatch, IPMismatch (via X-Forwarded-For with TrustProxy)
- Turnstile: AlwaysFail, TokenExpired
- Protocol: OversizedBody, WrongMethod (GET/PUT/DELETE), EmptyAuthHeader

### Gate 5: Full Suite Green
- `task e2e` → exit 0 (all 35 tests across 3 packages)
- `go test -race ./ephemeralauth/... ./litespaserver/... ./email/...` → all pass
- Race detector enabled (-race flag)
- Test caching disabled (-count=1 flag)
- Working tree clean (`git status --short` → empty)

## Code Quality Findings
- Critical: 0
- Warning: 0
- Suggestion: 0

## Commits Reviewed
- `beed491`: feat(e2e): add complete e2e test suite for ephemeralauth
