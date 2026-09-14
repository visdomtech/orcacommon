# Gate: Guest Session Middleware

## Condition
A gorilla/mux-compatible middleware sets a guest session cookie on public page loads and rejects API calls lacking a valid one. Cookie: crypto-random ID, HMAC-signed (stateless, same signing key), `SameSite=Strict; HttpOnly; Secure`. Verification recomputes the HMAC — no server state. Tampered cookie value → rejected.

## Evidence Required
- [ ] Guest session middleware → `ephemeralauth/guest.go`
- [ ] Unit tests: cookie set with required flags, valid cookie accepted, tampered cookie rejected, missing cookie rejected on API paths → `ephemeralauth/guest_test.go`

## Verification Method
Test Engineer runs the tests and inspects the Set-Cookie attributes asserted in test bodies (Strict, HttpOnly, Secure all present) and confirms the tamper test flips a byte in the cookie value.

## Owner
Engineer
