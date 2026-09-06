# Plan — Iteration 1

## Architecture Decision
Add a `key` field to `dao` struct, computed once at construction time. This avoids re-computing the key on every get/set call and keeps the SQL queries clean.

`NewManager` gains a `frontendName string` parameter (appended at the end for positional compatibility). It computes the key and passes it to the `dao`.

## Tasks (ordered)

### Task 1: Add `FrontendName` to Config
**File:** `litespaserver/litespaserver.go`
**Change:** Add `FrontendName string` field with doc comment to `Config` struct.
**Acceptance:** Field exists, documented, zero-value is empty string.

### Task 2: Add `key` field to dao, resolve from FrontendName
**File:** `litespaserver/dao.go`
**Changes:**
- Add `key string` field to `dao` struct
- Add a helper `versionKey(frontendName string) string` that returns `settingVersionKey` when empty, `settingVersionKey + "." + frontendName` when non-empty
- `getVersion` and `setVersion` use `d.key` instead of `settingVersionKey`

**Acceptance:** `dao.key` carries the resolved key; SQL queries unchanged except for parameter source.

### Task 3: Thread FrontendName through NewManager → dao
**File:** `litespaserver/version.go`
**Changes:**
- Add `frontendName string` parameter to `NewManager` (last positional arg)
- Compute key via `versionKey(frontendName)` and assign to `dao.key`

**File:** `litespaserver/serve.go`
**Changes:**
- `NewServer` passes `cfg.FrontendName` to `NewManager`

**Acceptance:** Config → Server → Manager → dao chain carries FrontendName.

### Task 4: Write unit tests for key resolution
**File:** `litespaserver/dao_test.go` (new file)
**Tests:**
- `TestVersionKey` — table-driven test for the `versionKey` helper: empty → `"frontend.version"`, `"admin"` → `"frontend.version.admin"`
- `TestDaoKey` — verify dao constructed with FrontendName uses the correct key (use a mock or verify field directly)

**Acceptance:** Tests cover both empty and non-empty paths. `go test -race ./litespaserver/...` passes.

### Task 5: Update existing callers
**File:** Check all existing `NewManager` call sites within the package and update.
**Acceptance:** `go build ./...` succeeds. `go test -race ./...` passes.

## Risks
- `NewManager` signature change could break downstream callers (out of this repo). Mitigated by appending the parameter at the end.
