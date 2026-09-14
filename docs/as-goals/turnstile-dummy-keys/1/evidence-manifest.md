# Evidence Manifest — Iteration 1

## Gate Status

| Gate | Status | Evidence | Owner |
|------|--------|----------|-------|
| Gate 1: Dummy Key Constants | ✅ Pass | `ephemeralauth/turnstile.go` (lines 20-40), `ephemeralauth/config.go` (lines 24-27), `ephemeralauth/AGENTS.md` (lines 152-174) | Engineer |
| Gate 2: Error Code Parsing | ✅ Pass | `ephemeralauth/turnstile.go` (lines 62-74, 78-84, 89-127), `ephemeralauth/turnstile_test.go` (all tests green) | Engineer |
| Gate 3: Integration Tests | ✅ Pass | `ephemeralauth/turnstile_integration_test.go` (lines 1-57, build tag on line 1), `go test -race ./ephemeralauth/...` (no live API calls) | Engineer |

## Detailed Evidence

### Gate 1: Dummy Key Constants
All six Cloudflare official dummy test keys are exported as named constants with godoc comments in `ephemeralauth/turnstile.go`:
- `TurnstileTestAlwaysPassSitekey` = `1x00000000000000000000AA`
- `TurnstileTestAlwaysPassSecret` = `1x0000000000000000000000000000000AA`
- `TurnstileTestAlwaysFailSitekey` = `2x00000000000000000000AB`
- `TurnstileTestAlwaysFailSecret` = `2x0000000000000000000000000000000AA`
- `TurnstileTestForcesChallengeSitekey` = `3x00000000000000000000FF`
- `TurnstileTestTokenExpiredSecret` = `3x0000000000000000000000000000000AA`

`Config` struct has `TurnstileSitekey` field with `env:"EPHEMERAL_TURNSTILE_SITEKEY"` tag. `LogValue` includes sitekey (NOT redacted — it's public). AGENTS.md documents all constants with a development config example.

### Gate 2: Error Code Parsing
- `turnstileResponse` struct has `ErrorCodes []string` with `json:"error-codes"` tag
- Exported `TurnstileResult` struct: `{ Success bool; ErrorCodes []string }`
- `VerifyWithDetails(ctx, token, remoteIP) (*TurnstileResult, error)` returns full details
- `Verify` delegates to `VerifyWithDetails` and discards error codes — backward compatible with `BotVerifier` interface
- All existing unit tests in `turnstile_test.go` pass unchanged

### Gate 3: Integration Tests
- `turnstile_integration_test.go` has `//go:build integration` on line 1
- `TestTurnstileIntegration_AlwaysPass`: uses `TurnstileTestAlwaysPassSecret`, asserts `Verify() = true`
- `TestTurnstileIntegration_AlwaysFail`: uses `TurnstileTestAlwaysFailSecret`, asserts `Verify() = false`
- `TestTurnstileIntegration_TokenExpired`: uses `TurnstileTestTokenExpiredSecret` with `VerifyWithDetails`, asserts `ErrorCodes` contains `"timeout-or-duplicate"`
- Standard `go test -race ./ephemeralauth/...` (without `-tags=integration`) does NOT make live API calls — confirmed by clean test run with no network errors

## Return Shipments (Failed Gates)
None — all gates pass.

## Code Quality Findings
- Critical: 0
- Warning: 0
- Suggestion: 1 — `VerifyWithDetails` could benefit from an explicit context timeout default (low impact, HTTP client already has 10s timeout)

## Commits Reviewed
- `34588c5`: feat(ephemeralauth): add Turnstile dummy test key constants and error-code parsing
- `8255008`: feat(ephemeralauth): add TurnstileSitekey to Config for frontend widget
- `1c2675f`: test(ephemeralauth): add Turnstile integration tests with Cloudflare dummy keys
- `8a5ba8f`: docs(ephemeralauth): document Turnstile dummy test keys and integration tests
- `3d83c45`: fix(ephemeralauth): fix flaky signature tampering tests

## Hygiene Checks
- [x] Working tree clean (only untracked `docs/as-goals/turnstile-dummy-keys/1/` pipeline artifacts)
- [x] Full test suite green: `go test -race ./...` passes all packages
- [x] `go build ./...` clean
- [x] No unresolved Critical findings
