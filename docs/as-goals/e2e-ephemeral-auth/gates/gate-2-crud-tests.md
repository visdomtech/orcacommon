# Gate: CRUD Tests

## Condition
All CRUD e2e tests exist in `tests/e2e/crud/` and pass. These tests exercise individual endpoints/middleware in isolation with status-code and response-shape assertions.

## Evidence Required
- [ ] `tests/e2e/crud/crud_test.go` (or grouped files) with `//go:build e2e` tag
- [ ] Guest session tests: cookie set on first visit, valid cookie accepted, tampered cookie rejected, cookie flags verified (SameSite=Strict, HttpOnly, Secure, Path=/)
- [ ] Token issuance tests: 200 success with valid bot_token + cookie, 401 no cookie, 400 missing bot_token, 400 oversized body, 403 bot verification failure, 405 wrong method
- [ ] Protect middleware tests: 200 happy path, 401 missing/malformed/expired token, 403 session mismatch, 403 IP mismatch, 403 UA mismatch, 403 insufficient scope
- [ ] All tests use live Cloudflare Turnstile dummy keys (always-pass for success, always-fail for 403)
- [ ] All tests pass with `go test -race -tags=e2e -count=1`

## Verification Method
Run `go test -race -tags=e2e -count=1 -v ./tests/e2e/crud/...` and verify all tests pass. Review test assertions for correctness (not just status codes but response body shape, cookie attributes, JWT claims).

## Owner
Engineer
