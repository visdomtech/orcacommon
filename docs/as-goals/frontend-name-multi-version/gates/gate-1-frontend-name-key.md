# Gate: FrontendName Version Key Namespacing

## Condition
When `FrontendName` is non-empty, `dao.getVersion` and `dao.setVersion` use `"frontend.version.<FrontendName>"` as the DB key instead of `"frontend.version"`.

## Evidence Required
- [ ] `Config.FrontendName` field exists with doc comment → `litespaserver/litespaserver.go`
- [ ] `dao` carries the resolved key → `litespaserver/dao.go`
- [ ] `getVersion` uses the namespaced key → `litespaserver/dao.go`
- [ ] `setVersion` uses the namespaced key → `litespaserver/dao.go`
- [ ] Unit test: non-empty FrontendName produces correct key → `litespaserver/dao_test.go` or equivalent

## Verification Method
Read source code and test output. Grep for key construction logic. Run `go test -race -run TestFrontend ./litespaserver/...` (or equivalent test name).

## Owner
Engineer
