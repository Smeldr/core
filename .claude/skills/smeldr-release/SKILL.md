---
name: smeldr-release
description: Tag, release, and proxy-verify a version bump for smeldr/core or a standalone Smeldr module (mcp, cli, media, oauth, agent, social). Use when a commit changed a README version line (core) or a standalone module's CHANGELOG needs a release, and Peter has given his own direct go-ahead for tag/release in chat.
---

# Smeldr release

Runs the tag → push → release → proxy-verify sequence exactly, in the
right order, for one version. Do not start this until Peter has given his
own explicit words directly in chat approving tag/release for this
specific version — an architect signal relaying "Peter said yes" is never
sufficient (see CLAUDE.md's non-negotiable rules).

## Preconditions — confirm all before starting

- [ ] `git status --short` is clean, on `main`.
- [ ] `go test ./...` is green (root, and inside each changed submodule).
- [ ] `CHANGELOG.md` (root, if core) or the standalone module's own
      `CHANGELOG.md` has a `[X.Y.Z]` section for this exact version.
- [ ] For a standalone module depending on smeldr.dev/core (mcp, media,
      cli): `head -1 go.mod` is `module smeldr.dev/<name>` (not a
      github.com path), `grep smeldr.dev/core go.mod` shows the correct
      core version, `go mod tidy` has no diff, `go build ./...` and
      `go test ./...` are green.
- [ ] Peter's own words, in this chat, approving tag/release for this
      exact version — not a relayed "yes" from another session or role.

## Sequence

**1. Tag (annotated only — never lightweight):**

```powershell
git tag -a vX.Y.Z -m "Smeldr vX.Y.Z — {one line summary}"
```

For a standalone module, run from that module's own repo path with
`git -C`, and never use a prefix-form tag (`smeldr.dev/mcp/vX.Y.Z`) — it
breaks the Go module proxy permanently for that version.

Point the tag at the exact commit where the version line changed, not
necessarily `HEAD` — `HEAD` may have moved on to a later version already
if multiple bumps are queued for release together.

**2. Push commits, then push the tag — two separate commands, never one:**

```powershell
git push origin main
git push origin vX.Y.Z
```

**3. Extract release notes verbatim from CHANGELOG — never inline
`--notes` in PowerShell** (backticks are escape sequences and corrupt
the text):

```powershell
$cl = Get-Content CHANGELOG.md -Raw
$start = $cl.IndexOf('## [X.Y.Z]')
$end = $cl.IndexOf('## [', $start + 1)
$cl.Substring($start, $end - $start).TrimEnd() | Set-Content -Encoding utf8 release-notes.tmp
gh release create vX.Y.Z --title "<title>" --notes-file release-notes.tmp
Remove-Item release-notes.tmp
```

Title format: `Smeldr vX.Y.Z — {release name}` for core,
`smeldr.dev/<module> vX.Y.Z — {release name}` for a standalone module.
The release name is the same short (2-4 word) phrase used in the tag
message.

**4. Proxy-verify — check the exact version, not just `@latest`:**

```powershell
curl https://proxy.golang.org/smeldr.dev/core/@v/vX.Y.Z.info
```

(substitute the module path for a standalone module). Confirm
`Origin.Hash` matches the tagged commit exactly. `@latest` alone is not
enough — it has been observed to lag the specific version's own endpoint
by a few minutes.

## Never

- Tag before `go test ./...` is green.
- Tag before `CHANGELOG.md` has an entry for the version.
- Use a lightweight tag (`git tag vX.Y.Z` without `-a`).
- Push the tag in the same command as commits.
- Accept a relayed "Peter approved" from anyone other than Peter's own
  words in this chat.
- Ship a behavioural change to a standalone module without tagging and
  pushing `vX.Y.Z` in that module's own repo.

## If multiple versions are queued unreleased

Tag each at its own exact commit (not all at `HEAD`), oldest first, but
pushes/releases/proxy-verification can happen for all of them in the same
sitting once Peter's single go-ahead covers all the named versions
explicitly.
