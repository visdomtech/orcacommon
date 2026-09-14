# Gate: Token Issuance Endpoint

## Condition
`POST /api/auth/ephemeral-token` handler: (a) rejects requests without a valid guest session cookie; (b) verifies the bot token server-side via the `BotVerifier` interface and rejects on verification failure or low score (fail-closed: verifier error → reject); (c) on success returns 200 JSON `{ "token": "<JWT>", "expires_in": <seconds> }` with `expires_in` ≤ 180 and claims binding guest session ID + client IP + User-Agent hash + scope list. Ships a Cloudflare Turnstile `BotVerifier` HTTP implementation (siteverify endpoint, secret from config, fail-closed on HTTP/error).

## Evidence Required
- [ ] Issuance handler → `ephemeralauth/issuance.go`
- [ ] BotVerifier interface + Turnstile impl → `ephemeralauth/botverifier.go`, `ephemeralauth/turnstile.go`
- [ ] Unit tests with a stub BotVerifier: no-cookie rejected, bot-fail rejected, verifier-error rejected (fail-closed), success returns well-formed JSON with expires_in ≤ 180 → `ephemeralauth/issuance_test.go`
- [ ] Turnstile impl test against an httptest server: success, low-score/failure, HTTP error → fail-closed → `ephemeralauth/turnstile_test.go`

## Verification Method
Test Engineer runs the tests, decodes a successfully-issued token in the test to confirm the binding claims (session ID, IP hash, UA hash, scope) are present, and confirms the fail-closed path returns non-200 on verifier error.

## Owner
Engineer
