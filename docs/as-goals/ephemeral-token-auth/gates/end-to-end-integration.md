# Gate: End-to-End Integration

## Condition
An integration test wires the full flow on a `gorilla/mux` router: guest-session middleware on the page route, issuance endpoint, and a protected API route behind the verification middleware. Asserts: (1) protected request with no token → 401; (2) full flow (load page → get cookie → issue token with stub-passing bot verifier → call protected route with Bearer) → 200; (3) request with an expired token → 401; (4) request with a token bound to a different session/IP/UA → 403.

## Evidence Required
- [ ] Integration test exercising the real mux router and real middleware chain → `ephemeralauth/integration_test.go`

## Verification Method
Test Engineer runs `go test -race ./ephemeralauth/...` and confirms the integration test builds a real `mux.Router`, mounts the real middlewares (not mocks of them), and asserts all four outcomes. Only the BotVerifier may be stubbed.

## Owner
Engineer
