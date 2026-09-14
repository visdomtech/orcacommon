# Gate: Dummy Key Constants

## Condition
The `ephemeralauth` package exports named constants for all six Cloudflare official dummy test keys (sitekey + secret pairs for always-pass, always-fail, and token-expired/spent). Constants are clearly named, documented, and discoverable by consumers.

## Evidence Required
- [ ] Exported constants with godoc comments → `ephemeralauth/turnstile.go` (or dedicated file)

## Verification Method
Test Engineer reads the source and confirms all six keys are present as exported `const` or `var` with matching values:
- Always Pass Sitekey: `1x00000000000000000000AA`
- Always Pass Secret: `1x0000000000000000000000000000000AA`
- Always Fail Sitekey: `2x00000000000000000000AB`
- Always Fail Secret: `2x0000000000000000000000000000000AA`
- Forces Challenge Sitekey: `3x00000000000000000000FF`
- Token Expired/Spent Secret: `3x0000000000000000000000000000000AA`

## Owner
Engineer
