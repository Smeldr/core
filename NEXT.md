# A60: MustConfig enforcement in forge.New()

## Background
`forge.New()` currently accepts a `Config` without validation. Invalid
configuration (empty BaseURL, Secret too short) is only discovered at runtime,
not at startup. `MustConfig()` already exists and performs the correct validation.

## Why
Configuration errors must be caught at startup, not at first request.
This aligns with the existing godoc on `New()` which already recommends
`MustConfig` — now it is enforced automatically.

## Constraints
- Only `forge.go` and `CHANGELOG.md` are in scope.
- Breaking change: apps with invalid config that previously started will now
  panic at startup. This is intentional and correct behaviour.
- Godoc on `New()` must be updated to reflect the new behaviour.
- Register as A60 in `DECISIONS.md` at commit time (index row + body section).

## Your task
1. Plan the change and present it for review before writing any code.
2. Wait for approval before implementing.
