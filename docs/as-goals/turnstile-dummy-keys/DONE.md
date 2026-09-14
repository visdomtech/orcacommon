# Goal Achieved — Turnstile Dummy Keys Integration

## Iterations: 1/10

## Gates Passed
- [x] Gate 1: Dummy Key Constants — all six Cloudflare dummy test keys exported with godoc
- [x] Gate 2: Error Code Parsing — `turnstileResponse` captures `error-codes`, `VerifyWithDetails` surfaces them
- [x] Gate 3: Integration Tests — build-tag-guarded tests against live Cloudflare API (always-pass, always-fail, token-expired)

## Commits
- `34588c5`: feat(ephemeralauth): add Turnstile dummy test key constants and error-code parsing
- `8255008`: feat(ephemeralauth): add TurnstileSitekey to Config for frontend widget
- `1c2675f`: test(ephemeralauth): add Turnstile integration tests with Cloudflare dummy keys
- `8a5ba8f`: docs(ephemeralauth): document Turnstile dummy test keys and integration tests
- `3d83c45`: fix(ephemeralauth): fix flaky signature tampering tests

## Working Tree
- Status: clean (all implementation committed; pipeline artifacts staged for final commit)
- Branch: current

## Unresolved Findings (non-blocking)
- Warning: none
- Suggestion: `VerifyWithDetails` could benefit from an explicit context timeout default (low impact — HTTP client already has 10s timeout)

## Summary

The `ephemeralauth` package now has:
1. **Exported dummy key constants** for all six Cloudflare official test keys (sitekey + secret pairs for always-pass, always-fail, and token-expired/spent)
2. **Error code parsing** via `ErrorCodes []string` on `turnstileResponse` and `TurnstileResult`, surfaced through `VerifyWithDetails`
3. **`TurnstileSitekey` config field** for the frontend widget's public key
4. **Integration tests** guarded by `//go:build integration` that exercise the live Cloudflare siteverify endpoint
5. **Backward-compatible API** — `Verify` still returns `(bool, error)` for `BotVerifier` interface compliance
6. **Updated documentation** in AGENTS.md with constant table, dev config example, and test commands
