# Operational Notes

Durable tooling and framework gotchas — reference facts, not a diary. See
`CLAUDE.md` for standing process rules; this file is only for things that
are true about the tools regardless of what task is in flight.

## Standalone-module integration testing before a core push

For a standalone module (mcp, cli, oauth, media, social, agent) that depends on an
in-flight `smeldr/core` change: set up a local `go.work` pointing at the live feature
branch and test against it (e.g. `example/server` locally) before squashing, pushing, or
tagging the module — not after. `go.work` is gitignored, never committed, in every repo
it's used.

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

## SQLite missing-column error text depends on statement kind

`modernc.org/sqlite` reports a missing column with **two different message
shapes** depending on which statement referenced it — confirmed directly,
2026-09-28 (`token-record-user-id`), by provoking all three from the same
test: an `UPDATE ... SET <col> = ...` or a `SELECT <col> FROM ...` produces
`no such column: <col>`, but an `INSERT INTO t (<col>) VALUES (...)`
naming the column in its own column list produces `table t has no column
named <col>` instead — a different sentence, not just a different table
name substituted in.

`isNoSuchColumn` (`dbprobe.go`) checks for both shapes now, but every caller
of it before this date only ever used it against an `UPDATE` fallback
(`App.TransitionItemWithReason`, `App.DrainEvalQueue`) — the `INSERT` shape
was never exercised until `TokenStore.createToken`'s own fallback needed
it, and it silently failed to match (`isNoSuchColumn` returned `false` for
a genuine missing-column `INSERT` error) until caught by three pre-existing
tests failing against a legacy `smeldr_tokens` fixture with no `user_id`
column. Before writing a new `isNoSuchColumn`-based fallback for an
`INSERT` (not `UPDATE`/`SELECT`), verify empirically which message shape
your own statement actually produces — don't assume the existing check
already covers it.

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

## Probing for a column or table on SQLite and Postgres (found 2026-10-06, D103)

`SELECT "x" FROM t WHERE 1=0` is **not** a column probe on SQLite: a double-quoted
name that matches no column is read as a string literal, so the statement
succeeds for a column that is not there. `columnExists` (`dbprobe.go`)
qualifies the column with its table (`SELECT "t"."x" ...`), which is an error on
SQLite and on Postgres alike. The qualified SQLite error is `no such column:
t.x`, not `no such column: x`, so `isNoSuchColumn` reads the name after the last
dot.

Postgres words the same failure three ways, verified against postgres:16:
`column "x" does not exist`, `column "x" of relation "t" does not exist` (an
INSERT) and, for a qualified reference, `column t.x does not exist` with **no
quotes around the name**. Matching on `column "x"` alone misses the last one;
SQLSTATE 42703 does not say which column. A failed statement also aborts the
whole transaction on Postgres, so a probe that is meant to fail (a missing table
or column) must never run inside one: `refuseInTx` turns that into an error.

`resolveItemTable` finds an item's table by probing `smeldr_<x>s` and then `<x>s`, so a
runtime-defined type pays two probes that fail by design on every call. On Postgres each
failed probe is an `ERROR: relation ... does not exist` line in the server log. That is
noise, not a defect, and there is deliberately no cache of the result: table existence can
change at runtime (architect, 2026-10-06, D103).
