# Gate: E2E Infrastructure

## Condition
The e2e test infrastructure exists and is functional: a dedicated e2e server binary, a test harness package, and a Taskfile with `task e2e` as the main entry point. The server boots, serves HTTP, and the harness can connect to it.

## Evidence Required
- [ ] `cmd/e2eserver/main.go` — standalone e2e server that wires ephemeralauth middleware on gorilla/mux
- [ ] `tests/e2e/harness/harness.go` — Stack struct with Start/Close lifecycle, manifest reading, HTTP client
- [ ] `tests/e2e/harness/runmain.go` — RunMain boilerplate for TestMain
- [ ] `Taskfile.yml` — `task e2e` entry point with server lifecycle (start, wait, test, stop)
- [ ] Server writes a manifest file (base_url) that the harness reads
- [ ] `task e2e` successfully boots the server and connects (even if no tests exist yet)

## Verification Method
Run `task e2e` and verify the server starts, the manifest is written, and the harness can read it. The infrastructure must support both ephemeralauth standalone routes and litespaserver PublicAuth integration routes.

## Owner
Engineer
