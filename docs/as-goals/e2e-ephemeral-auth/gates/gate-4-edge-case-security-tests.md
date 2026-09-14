# Gate: Edge Case & Security Tests

## Condition
Security edge case e2e tests exist and pass. These tests validate the security boundaries of the ephemeralauth system against attacks and misuse.

## Evidence Required
- [ ] Tampered cookie: modify guest cookie value → expect rejection (new cookie issued or 401)
- [ ] Expired token: use a token past its TTL → expect 401
- [ ] Wrong IP binding: obtain token from one IP, use from different IP (via X-Forwarded-For with TrustProxy) → expect 403
- [ ] Wrong UA binding: obtain token with one User-Agent, use with different → expect 403
- [ ] Scope escalation: token with `public:read` attempts to access endpoint requiring `admin:write` → expect 403
- [ ] Algorithm confusion: craft JWT with `alg=none` or RS256 header → expect 401
- [ ] Oversized body: POST to issuance endpoint with body > 1KB → expect 400
- [ ] Method not allowed: GET/PUT/DELETE on issuance endpoint → expect 405
- [ ] Empty Authorization header → 401
- [ ] Malformed Bearer token (not JWT format) → 401
- [ ] Turnstile always-fail key: bot verification always fails → 403
- [ ] Turnstile token-expired key: verification returns timeout-or-duplicate → 403
- [ ] All tests pass with `go test -race -tags=e2e -count=1`

## Verification Method
Run the edge case tests and verify each security boundary is properly enforced. Security Engineer reviews coverage completeness — no known attack vector should be untested.

## Owner
Engineer (implementation), Security Engineer (coverage validation)
