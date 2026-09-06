# Gate: Backward Compatibility

## Condition
When `FrontendName` is empty (default), behavior is identical to today: key = `"frontend.version"`. No existing tests break. No API signature changes that break callers.

## Evidence Required
- [ ] Empty FrontendName → key is exactly `"frontend.version"` → source code inspection
- [ ] All existing tests still pass → `go test -race ./...`
- [ ] `NewManager` and `NewServer` signatures are backward-compatible (new param optional or zero-value safe)

## Verification Method
Run full test suite. Inspect key resolution for empty-string case. Verify no required parameters added to existing constructors.

## Owner
Engineer
