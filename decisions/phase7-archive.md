# Smeldr — Decisions Archive: Phase 7 (A136–A138)

Archived 2026-06-10 from `recent.md`.

---

## A136 — `list_storys` → `list_stories`: consonant-y pluralization in MCP list tool names

**Date:** 2026-06-08
**Status:** Agreed
**Level:** 2 (patch; no exported-symbol change, no behaviour change for existing tool names)

### Decision

MCP list tool names for content types whose snake_case name ends in consonant+y
(e.g. `Story` → `story`) were generated as `list_storys`, which is grammatically
wrong. Fix: new `pluralSnake()` helper applies the standard English consonant-y →
ies rule when forming the list tool name.

### Changes

**`mcp/mcp.go`** — line 343:
`"list_" + typeSnake + "s"` → `"list_" + pluralSnake(typeSnake)`

New helpers added after `snakeCase`:

```go
func pluralSnake(s string) string {
    if len(s) >= 2 && s[len(s)-1] == 'y' && !isVowel(s[len(s)-2]) {
        return s[:len(s)-1] + "ies"
    }
    return s + "s"
}
func isVowel(b byte) bool {
    return b == 'a' || b == 'e' || b == 'i' || b == 'o' || b == 'u'
}
```

**`mcp/tool.go`** — `moduleForAdminList()` reverse lookup updated to resolve
the "ies" suffix back to the base type (stories→story) in addition to the
existing plain-s suffix stripping (posts→post).

**`mcp/mcp_test.go`** — three new tests:
- `TestPluralSnake`: story→stories, category→categories, post→posts, key→keys,
  essay→essays, day→days
- `TestMCPConsonantYPlural_toolName`: registers `testStory` module, asserts
  `defs[0].Name == "list_test_stories"`
- `TestMCPConsonantYPlural_dispatch`: asserts `list_test_stories` dispatches
  correctly (returns `items` field)

`go test ./...` → ok `smeldr.dev/mcp` 0.195s.

**`mcp/CHANGELOG.md`** — [1.17.2] section prepended.

**`common/agent/skills/smeldr.md`** and **`core/skills/smeldr.md`** — version
line updated: `mcp v1.17.1 → v1.17.2`.

### Version

mcp v1.17.2 (patch; `list_stories` now generated where previously `list_storys`
was generated — operators using consonant-y types must update their MCP client
tool references).

---

## A137 — Scheduler save-error resilience: continue publishing remaining items

**Date:** 2026-06-08
**Status:** Agreed
**Level:** 2 (bug fix; behaviour change in error path of `processScheduled`)

### Decision

When `processScheduled` called `repo.Save` on a scheduled item and the save
failed, the function returned immediately with the error. This stopped all
remaining scheduled items in the same tick from being published. The fix
replaces the `return` with `slog.Warn + continue` so the failing item is
skipped and the loop proceeds to the next item.

Also fixed: `scheduler.tick()` was discarding the error return from
`processScheduled` (comment said "logged by caller" — untrue). The error is
now captured and logged.

### Files changed

- **`module.go`**: `processScheduled` — `return published, next, err` on
  `repo.Save` failure replaced with:
  ```go
  slog.Warn("smeldr: scheduler failed to publish item; skipping",
      "id", nodeIDOf(item), "err", err)
  continue
  ```
- **`scheduler.go`**: `tick()` — `_, n, _ := m.processScheduled(...)` →
  captures `err` and calls `slog.Warn("smeldr: scheduler tick error", "err", err)`
  when non-nil. Import `"log/slog"` added.
- **`scheduler_test.go`**: `TestProcessScheduled_continuesAfterSaveError` — seeds
  3 scheduled items, wraps repo with `failOnSaveRepo` that injects an error for
  item #2, asserts `published == 2` and no error returned.
  New helper: `failOnSaveRepo[T any]` wraps `Repository[T]` and returns
  `errors.New("injected save failure")` when `Save` is called for a specific ID.

### Version

core v1.36.1 (patch; no exported-symbol or interface change).

Branch: `claude/scheduled-posts-conflict-m6s3kn` merged to main via `--no-ff`.

---

## A138 — X post body length: t.co URL weighting

**Date:** 2026-06-08
**Status:** Agreed
**Level:** 2 (bug fix; behaviour change in X post validation path)

### Decision

`publish()` in `social/twitter.go` counted URL characters at face value when
validating against the 280-character X limit. The X API wraps every URL with
t.co and counts it as exactly 23 characters regardless of actual length — a
long UTM-tagged URL occupies one t.co slot, not 80+ characters. Our guard was
rejecting posts the X API would accept.

Fix: new `xWeightedBodyLen(body string) int` helper that replaces each URL's
rune count with `xTcoURLLen` (23). Both uses of `len([]rune(p.Body))` in
`publish()` are replaced with `xWeightedBodyLen(p.Body)`.

### Files changed

**`social/twitter.go`**:
- New constant `xTcoURLLen = 23` alongside `xMaxBodyLength`
- New package-level var `xURLRegexp = regexp.MustCompile("https?://\\S+")`
- New helper `xWeightedBodyLen(body string) int`:
  ```go
  func xWeightedBodyLen(body string) int {
      total := len([]rune(body))
      for _, m := range xURLRegexp.FindAllString(body, -1) {
          total += xTcoURLLen - len([]rune(m))
      }
      return total
  }
  ```
- `publish()`: both `len([]rune(p.Body))` → `xWeightedBodyLen(p.Body)`
- New import: `"regexp"`

**`social/twitter_test.go`**:
- `TestXWeightedBodyLen`: 5 table-driven cases (no URL, short URL expands,
  long URL shrinks, two URLs, body at exactly 280 weighted)
- `TestPublishBodyLen_tcoWeighting`: verifies a 310-raw / 273-weighted body is
  accepted by `publish()`; verifies a 319-raw / 282-weighted body is rejected
- `xAPIRedirectTransport`: test helper redirecting `xAPIBase` requests to a
  local httptest.Server

**`social/CHANGELOG.md`**: [0.8.2] section prepended.

### Version

social v0.8.2 (patch; no exported-symbol or interface change).
