# Turnstile Dummy Keys Integration

## Goal
Enable local development and automated testing of the `ephemeralauth` Turnstile verification flow using Cloudflare's official dummy test keys, with real API integration tests that exercise the live siteverify endpoint and parse error codes from the response.

## Context
The `ephemeralauth` package already has a `TurnstileVerifier` that POSTs to Cloudflare's siteverify endpoint and parses the `success` field. Tests currently mock the endpoint with `httptest.Server`. The `turnstileResponse` struct does not capture `error-codes`, which is needed for handling the `timeout-or-duplicate` scenario. There are no exported constants for Cloudflare's official dummy test keys, and no integration tests against the real Cloudflare API. The `Config` struct has `TurnstileSecret` but no `TurnstileSitekey` field.

## Success Criteria
- Exported constants for all six Cloudflare dummy test keys (sitekey + secret pairs for always-pass, always-fail, and token-expired)
- `turnstileResponse` struct captures `error-codes` from the siteverify response
- Integration tests against the real `challenges.cloudflare.com` endpoint using dummy keys verify: (1) always-pass secret returns success, (2) always-fail secret returns failure, (3) token-expired secret returns error codes
- Existing unit tests (httptest-based) continue to pass unchanged
- `go build ./...` clean; `go test -race ./...` green
- `Config` documents the dummy key constants for development use

## Constraints
- Integration tests must be guarded with a build tag (`//go:build integration`) so `go test -race ./...` does not make live API calls by default
- No production credentials committed; dummy keys are public Cloudflare test keys
- No changes to the `BotVerifier` interface or middleware logic
- No changes to postgres, email, utils, or litespaserver packages

## Out of Scope
- Frontend Turnstile widget integration (JS)
- New `BotVerifier` implementations beyond the existing Turnstile one
- Production key rotation or secret management tooling
- Changes to token issuance or protection middleware

## Created
2026-09-14
