# Plan — Iteration 1

**Goal:** Integrate Cloudflare Turnstile dummy test keys into `ephemeralauth` for development and integration testing against the live siteverify endpoint.

**Codebase facts:**
- `ephemeralauth/turnstile.go` has `TurnstileVerifier` with `Verify(ctx, token, remoteIP) (bool, error)`
- `turnstileResponse` struct only has `Success bool` — no error-codes field
- Existing tests use `httptest.Server` mocks — no live API calls
- No exported test key constants exist
- `ephemeralauth/config.go` has `TurnstileSecret` but no `TurnstileSitekey`

---

## Architecture Decisions

**AD-1: Test key constants file.** Add exported constants to `turnstile.go` alongside the existing `TurnstileVerifier`. Naming: `TurnstileTestAlwaysPassSitekey`, `TurnstileTestAlwaysPassSecret`, `TurnstileTestAlwaysFailSitekey`, `TurnstileTestAlwaysFailSecret`, `TurnstileTestForcesChallengeSitekey`, `TurnstileTestTokenExpiredSecret`. Each gets a godoc comment explaining its behavior.

**AD-2: Error code surfacing.** Add `ErrorCodes []string` to `turnstileResponse` with `json:"error-codes"` tag. Add a new exported method `VerifyWithDetails(ctx, token, remoteIP) (*TurnstileResult, error)` that returns a `TurnstileResult` struct containing `Success bool` and `ErrorCodes []string`. The existing `Verify` method delegates to `VerifyWithDetails` and discards error codes, preserving backward compatibility with the `BotVerifier` interface (which returns `(bool, error)` only).

**AD-3: Integration test build tag.** Create `turnstile_integration_test.go` with `//go:build integration` guard. Tests hit the real `challenges.cloudflare.com` endpoint. Standard `go test ./ephemeralauth/...` never makes network calls.

**AD-4: Config TurnstileSitekey field.** Add `TurnstileSitekey string` to `Config` with `env:"EPHEMERAL_TURNSTILE_SITEKEY"` tag. This is the frontend-facing key (not secret). Update `LogValue` — sitekey is NOT redacted (it's public). Update existing documentation in AGENTS.md.

---

## Tasks (ordered, WIP = 1)

### Task 1 — Dummy key constants + error-code parsing → Gates 1 & 2
- **Files:** `ephemeralauth/turnstile.go`
- Add 6 exported constants for Cloudflare dummy test keys
- Add `ErrorCodes []string` to `turnstileResponse` with `json:"error-codes"` tag
- Add `TurnstileResult` struct: `{ Success bool; ErrorCodes []string }`
- Add `VerifyWithDetails(ctx, token, remoteIP) (*TurnstileResult, error)` method
- Refactor existing `Verify` to delegate to `VerifyWithDetails` (backward compatible)
- **Acceptance:** `go build ./ephemeralauth/...` clean; `go test -race ./ephemeralauth/...` green (existing tests unchanged)

### Task 2 — Config: add TurnstileSitekey → Gate 1 (supplementary)
- **Files:** `ephemeralauth/config.go`
- Add `TurnstileSitekey string` field with `env:"EPHEMERAL_TURNSTILE_SITEKEY"`
- Update `LogValue` to include sitekey (NOT redacted — it's a public key)
- **Acceptance:** `go build ./...` clean; existing tests green

### Task 3 — Integration tests against live Turnstile API → Gate 3
- **Files:** `ephemeralauth/turnstile_integration_test.go` (new, `//go:build integration`)
- Test 1: `TestTurnstileIntegration_AlwaysPass` — use `TurnstileTestAlwaysPassSecret` with a dummy token, assert `Verify` returns `(true, nil)`
- Test 2: `TestTurnstileIntegration_AlwaysFail` — use `TurnstileTestAlwaysFailSecret`, assert `Verify` returns `(false, nil)`
- Test 3: `TestTurnstileIntegration_TokenExpired` — use `TurnstileTestTokenExpiredSecret`, assert `VerifyWithDetails` returns error codes containing `timeout-or-duplicate`
- **Acceptance:** `go test -race -tags=integration ./ephemeralauth/...` green (requires network access)

### Task 4 — Update AGENTS.md documentation → supplementary
- **Files:** `ephemeralauth/AGENTS.md`
- Add section documenting the dummy test key constants
- Add guidance on running integration tests (`go test -race -tags=integration`)
- **Acceptance:** Documentation Engineer confirms all constants and commands documented

---

## Definition of done for this iteration
All 4 tasks committed, `go build ./...` clean, `go test -race ./...` green, all 3 gates evidenced.
