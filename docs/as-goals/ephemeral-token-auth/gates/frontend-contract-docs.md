# Gate: Frontend Contract & Docs

## Condition
The frontend deliverable exists as a written integration contract (no JS in repo): token endpoint URL + method, request body (bot token), success response shape, `Authorization: Bearer` usage, 401 → re-solve challenge → refresh → retry-once behavior, guest-cookie requirements (credentials: include / same-origin). Plus: `ephemeralauth/AGENTS.md` package guide, root `agents.md` sub-module list updated, and an ADR recording the key decisions (JWT HS256, stateless HMAC guest cookie, BotVerifier + Turnstile, ≤180s expiry, no-JS-in-repo).

## Evidence Required
- [ ] Frontend integration contract doc → `ephemeralauth/` docs (e.g. `ephemeralauth/FRONTEND_CONTRACT.md` or section in its AGENTS.md)
- [ ] Package guide → `ephemeralauth/AGENTS.md`
- [ ] Root sub-module list updated → `agents.md`
- [ ] ADR → `docs/` (e.g. `docs/adr-ephemeral-auth.md`)

## Verification Method
Test Engineer reads the contract doc and confirms it contains every element in the Condition (endpoint, request, response, Bearer usage, refresh-retry-once, cookie requirement) and that the ADR covers all five decisions.

## Owner
Documentation Engineer
