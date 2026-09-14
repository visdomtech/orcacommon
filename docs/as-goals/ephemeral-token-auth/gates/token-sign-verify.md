# Gate: Token Sign & Verify Unit

## Condition
The `ephemeralauth` package can issue an HS256 JWT and verify it statelessly. Verification rejects: bad signature, token with `exp` in the past, token with `exp` more than 180s from issuance (issuer-side clamp), and any JWT whose `alg` header is not HS256 (algorithm pinning — no `alg=none`, no RS256-confusion).

## Evidence Required
- [ ] Token issuer/verifier implementation → `ephemeralauth/token.go`
- [ ] Unit tests: valid round-trip, tampered signature rejected, expired token rejected, over-long expiry clamped at issuance, wrong-alg token rejected → `ephemeralauth/token_test.go`

## Verification Method
Test Engineer runs `go test -race ./ephemeralauth/...` and confirms each case above has a distinct test that exercises it (reads the test bodies, not just names).

## Owner
Engineer
