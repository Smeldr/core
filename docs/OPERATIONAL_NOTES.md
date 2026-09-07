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
