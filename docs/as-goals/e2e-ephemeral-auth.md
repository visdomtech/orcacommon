# E2E Tests for Ephemeral Auth & Cloudflare Turnstile

## Goal
A complete e2e test suite for the ephemeralauth package (standalone and litespaserver integration) that validates guest session management, token issuance with Cloudflare Turnstile bot verification, and stateless API protection — covering CRUD operations, full workflows, and security edge cases — using go-task and Docker-based infrastructure with a dedicated e2e harness and server.

## Context
The `ephemeralauth` package provides short-lived JWT gating for public API endpoints with Cloudflare Turnstile bot verification. It has thorough unit tests and a small integration test, but no end-to-end tests that exercise the full HTTP stack through a real server. The `litespaserver` package can auto-wire ephemeralauth via `PublicAuth` config. The orcaagents repository has an established e2e pattern (go-task, testcontainers, harness + server) that should be adapted for this library.

## Success Criteria
- E2e test infrastructure exists: `cmd/e2eserver`, `tests/e2e/harness/`, `Taskfile.yml` with `task e2e` entry point
- CRUD e2e tests in `tests/e2e/crud/` cover: guest session cookie lifecycle, token issuance endpoint (all status codes), Protect middleware (all rejection paths + happy path)
- Workflow e2e tests in `tests/e2e/workflow/` cover: full page-to-API flow, token refresh flow, litespaserver PublicAuth integration flow
- Edge case tests cover: tampered cookies, expired tokens, wrong IP/UA binding, scope escalation, oversized bodies, method not allowed, algorithm confusion attacks
- All tests use live Cloudflare Turnstile API with dummy test keys (always-pass, always-fail, token-expired)
- `task e2e` runs the full suite green with `-race` detector
- Tests are organized under `tests/e2e/[user-facing-feature]/` with `crud_test.go` and `workflow_test.go` grouping

## Constraints
- Follow orcaagents e2e patterns (manifest-based client-server, harness.RunMain, go-task lifecycle)
- Use `//go:build e2e` build tag for all e2e test files
- Use `-race -count=1` flags for test execution
- Live Cloudflare Turnstile API with dummy keys (network dependency)
- No new external dependencies beyond what's needed for the e2e server (gorilla/mux is already available)

## Out of Scope
- Changes to the ephemeralauth package source code (tests only, bugs found are reported not fixed)
- Performance/load testing
- Browser-based testing (no headless browser, pure HTTP client tests)
- CI/CD pipeline configuration
- Testing other orcacommon packages (postgres, email, utils)

## Created
2026-09-14
