# Code Review — Iteration 1

## Summary

Full implementation of the `ephemeralauth` package across 8 commits. All code is clean, well-structured, and follows project conventions (slog logging, caarlos0/env tags, `slog.LogValuer` redaction, plain `testing` + `httptest`).

## Findings

### Critical: 0
### Warning: 0
### Suggestion: 2

#### S-1: `clientIP` could return empty string on edge cases
- **File:** `ephemeralauth/issuance.go:73`
- When `RemoteAddr` has no port and `net.SplitHostPort` fails, the fallback returns `r.RemoteAddr` which is fine. But if `RemoteAddr` is empty, `clientIP` returns `""`. This is an edge case that only affects tests without explicit `RemoteAddr`.
- **Impact:** Negligible — all tests set `RemoteAddr` explicitly.

#### S-2: Consider adding `MaxBytesReader` to issuance handler
- **File:** `ephemeralauth/issuance.go:47`
- The JSON body is decoded without a size limit. A malicious client could send a very large body.
- **Impact:** Low — in practice, the request body is a small JSON object. Could add `http.MaxBytesReader` in a future iteration.
- **Not blocking:** This is defense-in-depth, not a correctness issue.

## Code Quality Assessment

- **Architecture:** Clean separation of concerns — token, guest, bot verifier, issuance, middleware, config. Each file has a single responsibility.
- **Security:** Algorithm pinning (HS256 only), fail-closed bot verification, constant-time comparison (`hmac.Equal`), stateless verification (no I/O in middleware).
- **Testing:** Comprehensive test coverage — every branch in the middleware has a distinct test, integration test uses real gorilla/mux with real middlewares.
- **Conventions:** Follows all project conventions (slog, env tags, LogValuer redaction, conventional commits).
- **Dependencies:** Only 2 new deps (golang-jwt/v5 and gorilla/mux), both justified.
