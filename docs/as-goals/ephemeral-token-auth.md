# Ephemeral Token Auth

## Goal
Provide a reusable `ephemeralauth` package in orcacommon that secures unauthenticated public API endpoints via short-lived (≤180s) stateless JWTs, gated by a stateless signed guest session and server-side bot verification, exposed as gorilla/mux-compatible middleware and auto-wireable through litespaserver config.

## Context
orcacommon is a shared Go library (postgres, litespaserver, email, utils). Downstream Visdom/Orca services expose unauthenticated public endpoints (form submissions, public reads) that are open to automated scraping and spam. No shared mechanism exists to gate them. litespaserver already serves the public SPA and is the natural integration point for guest-session issuance.

## Success Criteria
- Request to a protected public endpoint without a token → HTTP 401.
- Request with a valid token (correct signature, unexpired, matching session + IP/UA binding, sufficient scope) → passes through.
- Request with an expired token → HTTP 401.
- Request with a token whose session/IP/UA binding does not match the current request → HTTP 403.
- Token issuance (`POST /api/auth/ephemeral-token`) without a valid guest session cookie → rejected.
- Token issuance with a failing/low-score bot verification → rejected.
- Token issuance with valid guest session + passing bot verification → 200 JSON `{ "token": "<JWT>", "expires_in": <seconds> }`.
- Token `exp` is always ≤180s from issuance; verification is fully stateless (no DB/Redis).
- Guest cookie is `SameSite=Strict; HttpOnly; Secure` and HMAC-signed (stateless).
- litespaserver exposes a config option that auto-wires guest-session middleware + the issuance endpoint when set.
- Unit tests cover token sign/verify, expiry, and binding mismatch; an integration test covers the no-token/valid-token/expired-token matrix.
- `go build ./...` clean; `go test -race ./...` green.

## Constraints
- JWT HS256 (symmetric HMAC-SHA256), single secret configured via env (caarlos0/env convention), never exposed to the frontend.
- Bot verification behind a `BotVerifier` interface; ship a Cloudflare Turnstile HTTP implementation as the reference. No reCAPTCHA impl in this pass.
- Guest session is a stateless HMAC-signed cookie (crypto-random ID + HMAC using the same signing key). No server-side session store.
- Token payload: `exp` (≤180s), `sub`/context claims binding guest session ID + client IP + User-Agent hash, `scope` list (e.g. `public:read`, `form:submit`).
- Verification path must be stateless: signature + exp + context binding only, no DB/Redis lookups.
- Lean dependency tree per AGENTS.md — one JWT library at most; justify any other new dep.
- `slog` structured logging; config structs implement `slog.LogValuer` with the signing key redacted.
- Middleware must be usable standalone with `gorilla/mux` (`mux.MiddlewareFunc`-compatible), independent of litespaserver.

## Out of Scope
- No JavaScript/frontend code in this repo. The frontend deliverable is a written integration contract doc (endpoint, request/response shape, `Authorization: Bearer` usage, 401-refresh-retry-once behavior) that consumer frontends implement against.
- No reCAPTCHA implementation (interface allows consumers to add one).
- No PASETO.
- No server-side session store / token revocation list.
- No per-endpoint rate limiting (separate concern, may be layered later).
- No changes to postgres, email, or utils packages.

## Created
2026-09-14
