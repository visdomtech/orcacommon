# Gate: Quality (Build, Tests, Race Detector)

## Condition
`go build ./...` succeeds. `go test -race ./...` passes for all packages. No unresolved Critical or Required findings from code review.

## Evidence Required
- [ ] `go build ./...` exit code 0 → terminal output
- [ ] `go test -race ./...` all packages pass → terminal output
- [ ] Code review with no Critical findings → `review.md`

## Verification Method
Run commands. Review diff.

## Owner
Engineer + Test Engineer
