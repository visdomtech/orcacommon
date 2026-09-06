# Frontend Name Multi-Version Support

## Goal
Support multiple frontend versions in the same `litespa_settings` table by adding a `FrontendName` config field that namespaces the version key.

## Context
Currently `dao` uses a fixed key `"frontend.version"` for get/set operations. Multiple SPA frontends sharing the same database cannot each have their own version row. The user wants `FrontendName` on `Config` so that when set, the key becomes `"frontend.version.<FrontendName>"`.

## Success Criteria
- `Config.FrontendName` field exists with a clear doc comment
- When `FrontendName` is empty, behavior is identical to today (key = `"frontend.version"`)
- When `FrontendName` is non-empty, `getVersion`/`setVersion` use `"frontend.version.<FrontendName>"`
- Unit tests cover both paths (empty and non-empty `FrontendName`)
- Build and full test suite pass with race detector

## Constraints
- Backward-compatible: empty `FrontendName` = current behavior, no breaking changes
- No DB schema changes — same `litespa_settings` table
- Key format: `settingVersionKey + "." + FrontendName` (dot-separated)

## Out of Scope
- No changes to static provider, embedded mode, or CDN fetch logic
- No admin API changes
- No migration of existing keys

## Created
2026-09-06
