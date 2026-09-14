# Gate: LiteSPA Server Integration

## Condition
`litespaserver` exposes a config option (e.g. `PublicAuth *ephemeralauth.Config`) that, when set, auto-wires the guest-session middleware onto its public routes and mounts the `POST /api/auth/ephemeral-token` issuance endpoint. When unset, litespaserver behaves exactly as before (no behavior change, no new deps forced on consumers who don't use it). Signing key is redacted in logs via `slog.LogValuer`.

## Evidence Required
- [ ] Config field + wiring in litespaserver → `litespaserver/` (config + server files)
- [ ] Test: with option set, SPA route sets guest cookie and issuance endpoint is reachable; with option unset, no guest cookie and no issuance route → `litespaserver/` tests
- [ ] Signing key redacted in `slog` output (LogValuer test) → `ephemeralauth/` or `litespaserver/` tests

## Verification Method
Test Engineer runs `go test -race ./litespaserver/...`, confirms both the wired and unwired paths are tested, and confirms the redaction test asserts the key never appears in log output.

## Owner
Engineer
