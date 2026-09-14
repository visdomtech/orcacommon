# Gate: Protection Middleware

## Condition
A gorilla/mux-compatible middleware protects public API routes. It extracts `Authorization: Bearer <token>`, verifies signature + `exp` statelessly (no DB/Redis), and checks context binding (guest session ID, client IP, User-Agent hash) against the current request. Missing/malformed header → 401. Expired or bad-signature token → 401. Valid token whose binding does not match the current request → 403. Insufficient scope → 403. Valid + bound + scoped → request passes through.

## Evidence Required
- [ ] Verification middleware → `ephemeralauth/middleware.go`
- [ ] Unit tests covering each branch: no header 401, malformed header 401, expired 401, bad signature 401, session mismatch 403, IP mismatch 403, UA mismatch 403, scope mismatch 403, happy path passes → `ephemeralauth/middleware_test.go`

## Verification Method
Test Engineer runs the tests and confirms each status-code branch has a distinct test, and that the middleware performs no I/O (stateless) by reading its implementation.

## Owner
Engineer
