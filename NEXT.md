# A60: MustConfig enforcement in forge.New()

## Background
`forge.New()` currently accepts a `Config` without validation. Invalid
configuration (empty BaseURL, Secret too short) is only discovered at runtime,
not at startup. `MustConfig()` already exists and performs the correct validation.

## Task
Call `MustConfig(cfg)` as the first line of `forge.New()` in `forge.go`.

## Why
Configuration errors must be caught at startup, not at first request.
This aligns with the existing godoc on `New()` which already recommends
`MustConfig` — now it is enforced automatically.

## Consequences
- Breaking change: apps with invalid config that previously started will now
  panic at startup. This is intentional and correct behaviour.
- Update godoc on `New()` to reflect the new behaviour.
- Add entry in CHANGELOG.md.

## Files affected
- `forge.go` — `New()` function only
- `CHANGELOG.md`

## Amendment
Register as A60 at commit time.
