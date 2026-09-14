# Gate: Workflow Tests

## Condition
All workflow e2e tests exist in `tests/e2e/workflow/` and pass. These tests chain 2+ endpoints in sequential state progression, simulating real user flows.

## Evidence Required
- [ ] `tests/e2e/workflow/workflow_test.go` (or grouped files) with `//go:build e2e` tag
- [ ] Full page-to-API flow: visit page → receive guest cookie → solve Turnstile → POST token → receive JWT → call protected API → 200
- [ ] Token refresh flow: get token → use token → token expires → 401 → re-solve Turnstile → get new token → retry → 200
- [ ] LiteSPA server PublicAuth integration: boot litespaserver with PublicAuth config → guest session auto-wired → token issuance → protected API call
- [ ] Multi-scope flow: token issued with specific scopes → access scope-protected endpoint → verify scope enforcement
- [ ] All tests use live Cloudflare Turnstile dummy keys
- [ ] All tests pass with `go test -race -tags=e2e -count=1`

## Verification Method
Run `go test -race -tags=e2e -count=1 -v ./tests/e2e/workflow/...` and verify all tests pass. Review that workflows chain multiple endpoints and verify state progression at each step.

## Owner
Engineer
