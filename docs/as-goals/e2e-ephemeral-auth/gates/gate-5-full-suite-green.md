# Gate: Full Suite Green

## Condition
The entire e2e test suite runs green via `task e2e` with race detector enabled. The working tree is clean, all work is committed.

## Evidence Required
- [ ] `task e2e` completes successfully (exit code 0)
- [ ] All test packages pass: `tests/e2e/crud/`, `tests/e2e/workflow/`
- [ ] Race detector enabled (`-race` flag)
- [ ] Test caching disabled (`-count=1` flag)
- [ ] Working tree clean after all commits
- [ ] Existing unit tests still pass: `go test -race ./...` (no regressions)

## Verification Method
Run `task e2e` and verify exit code 0. Run `go test -race ./...` to verify no regressions in existing tests. Check `git status` for clean tree.

## Owner
Engineer
