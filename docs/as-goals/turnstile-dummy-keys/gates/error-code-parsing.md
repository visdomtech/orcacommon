# Gate: Error Code Parsing

## Condition
The `turnstileResponse` struct captures the `error-codes` field from the Cloudflare siteverify JSON response. The `TurnstileVerifier.Verify` method or an enhanced return signature surfaces error codes to the caller so that specific error conditions (e.g. `timeout-or-duplicate`) are distinguishable from a simple pass/fail. Existing unit tests continue to pass.

## Evidence Required
- [ ] Updated response struct with `error-codes` field → `ephemeralauth/turnstile.go`
- [ ] Existing unit tests still pass → `ephemeralauth/turnstile_test.go`

## Verification Method
Test Engineer reads the struct definition and confirms `ErrorCodes []string` (or similar) is present with `json:"error-codes"` tag. Runs existing unit tests to confirm no regressions. Confirms the Verify method signature or a new method surfaces error codes.

## Owner
Engineer
