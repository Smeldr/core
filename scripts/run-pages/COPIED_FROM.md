# Copied from docs, not edited here

Every other file in this directory is a byte-for-byte copy of `scripts/run-pages` in the docs repository at commit `93709b1` (docs T1272: the `-drafts` flag, the `example-places` setup and the `decisions-before-an-edit` sequence). It was copied on 15 November 2026 for core T1275 (first copy: `408d59c`, core T1253 part 2).

- The docs role owns these files. A change to them comes from docs through a Task, and is copied over in full: never edit one here, and never copy one file alone.
- The docs repository is private, which is why core carries a copy. `.github/workflows/getting-started-macos.yml` runs it on macOS (the `docs-pages` job).

To prove that the copy still matches its commit, with the docs repository checked out beside core:

```bash
mkdir -p /tmp/rp && git -C ../docs archive 93709b1 scripts/run-pages | tar -x -C /tmp/rp && diff -r -x COPIED_FROM.md /tmp/rp/scripts/run-pages scripts/run-pages
```

It prints nothing when the copy matches. When docs sends a new commit, replace the files, update the commit in this file and in the command, and run it again.
