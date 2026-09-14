# Gate: Integration Tests Against Live Turnstile API

## Condition
Integration tests (guarded by `//go:build integration` build tag) exercise the real Cloudflare siteverify endpoint using dummy keys and assert: (1) always-pass secret with a non-empty token returns success; (2) always-fail secret returns failure (success=false); (3) token-expired/spent secret returns error codes including `timeout-or-duplicate`. Standard `go test -race ./ephemeralauth/...` (without integration tag) does NOT make live API calls.

## Evidence Required
- [ ] Integration test file with build tag → `ephemeralauth/turnstile_integration_test.go`
- [ ] Standard test run (`go test -race ./ephemeralauth/...`) does not call live API

## Verification Method
Test Engineer reads the test file and confirms:
1. `//go:build integration` tag present
2. Test uses `TurnstileTestAlwaysPassSecret` (or equivalent constant)
3. Test uses `TurnstileTestAlwaysFailSecret` and asserts `success=false`
4. Test uses `TurnstileTestTokenExpiredSecret` and asserts error codes include `timeout-or-duplicate`
5. Test Engineer runs `go test -race ./ephemeralauth/...` and confirms no live API calls (no network errors)

## Owner
Engineer
