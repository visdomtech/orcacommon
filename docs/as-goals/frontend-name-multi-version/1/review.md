# Review — Iteration 1

## Scope
Diff: `HEAD~1..HEAD` — 5 source files changed, 1 new test file, 10 docs artifacts.

## Correctness
- `versionKey()` correctly returns `settingVersionKey` when empty, `settingVersionKey + "." + frontendName` when non-empty
- `dao.getVersion` and `dao.setVersion` both use `d.key` — verified no remaining references to `settingVersionKey` constant in queries
- `NewManager` constructs dao with `versionKey(frontendName)` — key computed once at construction time
- `NewServer` passes `cfg.FrontendName` through to `NewManager`

## Backward Compatibility
- `Config.FrontendName` is a zero-value string — existing callers using `Config{}` literal or struct initialization without this field get empty string automatically
- `NewManager` gains a trailing `frontendName string` parameter — within-package callers (only `serve.go`) updated. Out-of-repo callers would need to add the argument, but Go positional params make this unavoidable for a struct-less constructor
- Empty FrontendName → `versionKey("")` returns `"frontend.version"` → identical to pre-change behavior

## Test Coverage
- `TestVersionKey`: 3 cases (empty, single name, multi-segment name) — covers the key resolution logic
- `TestDaoKeyField`: verifies dao struct carries the key correctly
- Missing: integration test with actual DB (deferred — requires Docker/TestContainers, not in scope for a pure key-namespacing change)

## Findings
- **Warning:** `NewManager` is an exported function with a breaking signature change (new positional parameter). Downstream callers outside this repo must update. No mitigation possible without Go options-pattern refactor, which is out of scope.
- **Nit:** No doc update for `litespaserver/AGENTS.md` to mention `FrontendName`.

## Verdict
No Critical findings. One Warning (breaking API signature) is inherent to the design choice and accepted. All gates pass.
