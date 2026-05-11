# Forge — Milestone {N} Backlog (v{semver})

One-line description of the milestone goal.

---

## Progress

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1    | foo.go | 🔲 Not started | — |

---

## Layer {N} — {Layer name} ({dependency note})

### Step {N} — {filename}

**Depends on:** {nothing | list of files}
**Decisions:** {Decision numbers and Amendment IDs}
**Files:** `{impl file}`, `{test file}`

#### {N}.{M} — {Sub-section name}

- [ ] Specific, atomic implementation task
- [ ] Another task

#### Verification

- [ ] `go build ./...` — no errors
- [ ] `go vet ./...` — clean
- [ ] `gofmt -l .` — returns nothing
- [ ] `go test -v -run Test{Name} ./...` — all green
- [ ] `README.md` — no examples broken by this step
- [ ] `README.md` — section status badges updated if this step ships a documented feature
- [ ] `integration_full_test.go` — new cross-milestone groups added (final step of each milestone only)
- [ ] Review `ARCHITECTURE.md` and `DECISIONS.md` — no new decisions required,
      or new Decision/Amendment drafted and agreed upon

---

## Completion criteria for Milestone {N}

- [ ] Criterion 1
- [ ] `integration_full_test.go` — new cross-milestone groups (G{N}+) added and all passing
- [ ] `README.md` — all section badges updated to reflect features shipped in this milestone
