# Operational Notes

Durable tooling and framework gotchas — reference facts, not a diary. See
`CLAUDE.md` for standing process rules; this file is only for things that
are true about the tools regardless of what task is in flight.

## CI

`gh run watch --exit-status` returning exit 0 does **not** mean the
underlying workflow run succeeded. Check `gh run list`'s own
`status`/`conclusion` field explicitly before trusting a CI result — the
watch command's own exit code has been observed to report success while
the run it was watching was still `failure`.

## PowerShell working directory

`cd`/`Set-Location` updates PowerShell's own logical location (what
`Get-Content`, `Get-ChildItem`, and other PowerShell cmdlets resolve
relative paths against) but does **not** reliably update .NET's own
`Environment.CurrentDirectory` in this environment. A script that opens
with `cd <path>` and then calls a raw .NET API with a relative path —
`[System.IO.File]::ReadAllText("CHANGELOG.md", ...)`,
`[System.IO.File]::WriteAllText("release-notes.tmp", ...)` — can silently
resolve against a stale working directory left over from an earlier
PowerShell tool call, not the one just set. No error is raised; the wrong
file is read or written.

Confirmed live, 2026-09-20: extracting `smeldr.dev/mcp`'s own `[1.37.0]`
CHANGELOG section for a GitHub Release used `cd
...\Smeldr\mcp` followed by `[System.IO.File]::ReadAllText("CHANGELOG.md",
...)` — the read landed on `smeldr/core`'s own `CHANGELOG.md` instead
(cwd left over from an earlier release in the same session), returning
core's own *unrelated, historical* `[1.37.0]` entry from 2026-06-10 rather
than mcp's real one from today. `release-notes.tmp` was also written to
`smeldr/core`'s own directory, not `smeldr/mcp`'s. Caught before
`gh release create` was run, not after.

Use absolute paths for every `[System.IO.File]` (or other raw .NET I/O)
call in a PowerShell script that follows a `cd` — never rely on `cd`
having taken effect for anything beyond PowerShell's own cmdlets.

## gofmt / Windows

Windows checkouts with `core.autocrlf` enabled can make a local
`gofmt -l .` report files as dirty that are byte-identical to their
git-stored blob (CI runs on Linux against the real stored content, not
the local line-ending-translated working tree). Before treating a
gofmt-clean CI failure as a real regression, verify the actual stored
content directly (`git show HEAD:<file>`), not the local working-tree
file.

`gofmt -w` silently collapses two adjacent single-quotes (`''`) inside a
`//` comment into one curly closing-quote character (U+201D). Grep for
`//.*''` before running `gofmt -w` on a file with hand-written comments
using that pattern.

Windows PowerShell 5.1's `-Encoding utf8` writes **UTF-8 with a BOM**, not
plain UTF-8 (`utf8NoBOM` is a separate encoding name, only available in
PowerShell 7+). `Set-Content`/`Add-Content`/`Out-File -Encoding utf8` on a
`.go` file therefore prepends `EF BB BF` before the package declaration.
`gofmt` treats the BOM as real content and flags the file as dirty — this
reproduces even against the actual git-stored blob (unlike the CRLF
false-positive above), so it fails CI, not just the local check.
Confirmed root cause of a real CI break (2026-09-19, `smeldr.dev/oauth`
commit e07be4f): a `Bash`-tool call wrapping a PowerShell one-liner with
`Set-Content -Encoding utf8` partially executed before a quoting error,
leaving the BOM behind. Avoid `Set-Content`/`Out-File -Encoding utf8` on
`.go` files entirely — use the `Edit`/`Write` tools instead, or if a raw
PowerShell rewrite is unavoidable, use `[System.IO.File]::WriteAllText`
with an explicit `New-Object System.Text.UTF8Encoding($false)`.
