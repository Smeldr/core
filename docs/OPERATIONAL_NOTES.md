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
