# Changelog

All notable changes to `smeldr.dev/core/pgx` are documented in this file.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html) (pre-1.0 — breaking changes bump the minor version).

Tags for this module are path-prefixed (`pgx/vX.Y.Z`) since it is a submodule
within the `smeldr/core` repository, not a standalone repo.

---

## [0.2.0] — 2026-07-28

### Changed
- **Breaking:** Go package declaration renamed `forgepgx` → `pgx`, matching the
  module's own import path (`smeldr.dev/core/pgx`) and every other renamed
  companion package's convention (package name equals its import path's last
  segment). Callers update `forgepgx.Wrap(pool)` → `pgx.Wrap(pool)`. No
  behavioural or API-shape change — `Wrap` and every exported symbol are
  otherwise unchanged. This module's own Forge→Smeldr rename campaign
  (T61, v0.1.0) renamed the directory and module path but missed the package
  declaration itself; this release closes that gap.

---

## [0.1.2] — 2026-06-12

### Changed
- Bumped `smeldr.dev/core` dependency v1.29.0 → v1.38.0.

---

## [0.1.1] — 2026-06-11

### Fixed
- Bumped `github.com/jackc/pgx/v5` to v5.9.2 (security: one Critical, one Low CVE).

---

## [0.1.0] — 2026-05-28

### Changed
- Renamed `forge-pgx/` → `pgx/`; module path `smeldr.dev/pgx` → `smeldr.dev/core/pgx` (T61).
