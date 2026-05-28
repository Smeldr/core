# Contributing to Forge

Thank you for your interest in contributing. Forge is built in the open
and every contribution — code, documentation, bug reports, or ideas — matters.

---

## Before you start

**Read DECISIONS.md first.**
Every architectural decision is documented with rationale. Before opening a PR
that changes an interface or adds a feature, check whether a relevant decision
exists. If you disagree with a decision, open an issue for discussion — don't
work around it in a PR.

---

## Contributor License Agreement

Before your first PR is merged, you will be asked to sign a CLA via
cla-assistant.io. This is a one-time step. See COMMERCIAL.md for why
this is necessary.

---

## How to contribute

### Bug reports
Open an issue with the `bug` label. Include:
- Go version (`go version`)
- Forge version
- Minimal reproduction case
- Expected vs actual behaviour

### Feature requests
Open an issue with the `enhancement` label before writing code.
Describe the problem you are solving, not just the solution.
Large features should reference or propose an addition to DECISIONS.md.

### Pull requests
1. Fork the repository
2. Create a branch: `feat/my-feature`, `fix/my-bug`, `docs/my-update`
3. Write tests for your change
4. Run `go test ./...` and `go vet ./...`
5. Format with `gofmt -w .`
6. Open a PR with a clear description of what and why

---

## Code style

- `gofmt` — always
- `godoc` comments on all exported symbols — always
- No third-party dependencies in the `forge` core package
- Error types implement `smeldr.Error` — not raw `errors.New`
- Context is always `smeldr.Context`, not `context.Context`, in user-facing APIs

---

## Commit messages

```
feat: add smeldr.RateLimit middleware
fix: correct slug collision suffix generation
docs: update DECISIONS.md with amendment S1
test: add TestModule_ScheduledPublishing
```

---

## Decision process

If your contribution requires an architectural decision:
1. Open an issue tagged `architecture`
2. Describe the problem, options, and your recommendation
3. Wait for maintainer consensus before writing code
4. The decision is documented in DECISIONS.md before the PR is merged

This process exists to keep DECISIONS.md as the single source of truth
for why Forge is the way it is.
