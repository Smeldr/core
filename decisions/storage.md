# Forge Decisions — Storage

Archived from decisions/recent.md on 2026-05-17.
Read on demand. See DECISIONS.md for the full index.

---

## Amendment A68 � storage.go/module.go: irregular pluralisation doc comments

**Date:** 2026-04-09
**Status:** Agreed
**Level:** 1 (micro-amendment � doc-only, no exported symbol change)

### Problem

Story ? "storys" by default. An implementer agent hit an internal server
error because neither Table nor At mentioned this class of problem. The
correction (orge.Table("stories")) was trivially available, but neither
doc comment surfaced it. This is a documentation gap, not a code bug.

### Changes

**storage.go � Table function doc comment:**

Extended to name the problem class explicitly and add a *Story example:

`go
// Table returns a [SQLRepoOption] that overrides the automatically derived
// table name for a [SQLRepo]. Use it when the default snake_case plural
// derivation does not produce the correct name � for example, types whose
// plural is not formed by appending "s" (Story ? "storys", not "stories").
//
//repo := forge.NewSQLRepo[*Story](db, forge.Table("stories"))
//repo := forge.NewSQLRepo[*BlogPost](db, forge.Table("posts"))
`

**module.go � NewModule doc comment, At option line:**

Extended to name the pitfall inline:

`go
//   - [At]: override URL prefix (default: "/"+lowercase(TypeName)+"s").
//     Use when the default pluralisation is wrong: Story ? "/storys".
//     Example: forge.At("/solved") or forge.At("/stories").
`

### Consequences

- No logic changes
- No new tests required
- No exported symbols added, removed, or renamed
- ARCHITECTURE.md unchanged (existing entries are historical record)
- NEXT.md deleted in the same commit

---

## Amendment A78 — node.go: ValidateStruct unexported; RunValidation is sole public entry point

**Date:** 2026-05-04
**Status:** Agreed
**Files:** `node.go`, `node_test.go`

### Problem

`ValidateStruct` was exported but was never intended to be part of the public
API — it was an implementation detail of `RunValidation`. Having it exported
created two entry points for the same logic, and surfaced internal panic messages
that referred to the internal function name. Test helpers called `validateStruct`
directly rather than going through the documented public function.

### Decision

Unexport `ValidateStruct` to `validateStruct`. `RunValidation` is the only
public entry point for struct-tag-based validation.

Changes:
- `ValidateStruct` → `validateStruct` in `node.go`
- `RunValidation` godoc updated: removed the now-broken `[validateStruct]`
  cross-reference; "Struct-tag constraints" listed instead
- Panic message updated from `"forge: ValidateStruct requires..."` to
  `"forge: RunValidation requires a struct or pointer to struct"` — the public
  function name is now used in the error
- `node_test.go`: section comment and all 9 test functions renamed from
  `TestValidateStruct*` → `TestRunValidation*`; all `validateStruct()` call
  sites replaced with `RunValidation()`

### Consequences

1. **Breaking change** — any caller that imported `forge.ValidateStruct` will
   fail to compile. `RunValidation` was always the documented entry point;
   `ValidateStruct` was never mentioned in README or REFERENCE.md.
2. Single public entry point reduces confusion and prevents callers from
   bypassing the pointer-normalisation in `RunValidation`.
3. No behaviour change — `RunValidation` calls `validateStruct` as before.

---

## Amendment A80 — storage.go: SeqRepository[T] lazy iterator interface

**Date:** 2026-05-04
**Status:** Agreed
**Level:** 1 (additive — new exported interface and methods, no breaking change)
**Files:** `storage.go`, `storage_test.go`

### Problem

`Repository[T].FindAll` loads the entire result set into memory before returning
it. For large collections (feed generation, search indexing, CSV export) this
wastes memory proportional to the table size. There was no way to stream items
one at a time.

### Decision

Add `SeqRepository[T]` as an optional interface extending `Repository[T]`.
Both `MemoryRepo[T]` and `SQLRepo[T]` implement it. Callers type-assert their
`Repository` to `SeqRepository` to use lazy iteration.

```go
type SeqRepository[T any] interface {
    Seq(ctx context.Context, opts ListOptions) iter.Seq2[T, error]
}
```

Callers:

```go
if sr, ok := repo.(forge.SeqRepository[*Post]); ok {
    for item, err := range sr.Seq(ctx, opts) {
        if err != nil { ... }
    }
}
```

### Implementation

**`MemoryRepo[T].Seq`:** acquires an RLock, snapshots IDs and items map,
releases the lock, then yields items in insertion order applying the status
filter. Returns early if `yield` returns false (caller break).

**`SQLRepo[T].Seq`:** issues `SELECT *` with optional `WHERE "status" IN ($1,...)`
(no LIMIT), calls `QueryContext`, scans rows one at a time via cached reflection
(same path as `Query[T]`), yields each row. Propagates `rows.Err()` at end.
Does not apply sorting or pagination — use `FindAll` when those are required.

### Consequences

1. New exported symbol: `SeqRepository[T any]` interface.
2. `MemoryRepo[T]` and `SQLRepo[T]` each gain an unexported-method-equivalent `Seq`
   that satisfies the interface — no change to their existing method sets.
3. `Repository[T]` interface is unchanged — no breaking change for existing
   implementations.
4. Requires Go 1.23 `iter.Seq2` (forge minimum is 1.26.2 via A76).
5. Five new tests: `TestMemoryRepo_Seq_basic`, `TestMemoryRepo_Seq_statusFilter`,
   `TestMemoryRepo_Seq_yieldStop`, `TestSQLRepo_Seq_basic`,
   `TestSQLRepo_Seq_statusFilter_query`.

---

## Amendment A81 — go.mod: modernc.org/sqlite as test-only dependency

**Date:** 2026-05-04
**Status:** Agreed
**Level:** 1 (micro-amendment — test infrastructure only, no exported symbol change)
**Files:** `go.mod`, `go.sum`, `storage_sqlite_test.go` (new)

### Problem

All `SQLRepo[T]` tests used a fake `database/sql` driver that captures SQL
strings but never executes them against a real SQL engine. Behavioural
correctness — that `Save`, `FindByID`, `FindBySlug`, `FindAll`, and `Delete`
actually work against SQL — was untested. A parity suite existed for `MemoryRepo`
but had no counterpart for `SQLRepo`.

### Decision

Add `modernc.org/sqlite` as a **test-only** direct dependency in the core
module's `go.mod`. Import it only in `storage_sqlite_test.go`. The `forge`
package itself remains free of third-party dependencies at build time.

Exception to the zero-dependency rule: test-only imports (`_test.go` files)
may use third-party packages when:
1. The package is pure Go with no CGO (modernc.org/sqlite qualifies).
2. No alternative exists within the stdlib.
3. The import is isolated to a single `_test.go` file.
4. The decision is documented here so future maintainers understand the
   precedent.

### What this enables

`TestRepoParity_SQLRepo` runs the full `runRepoParity` 11-sub-test suite
against an in-memory SQLite database via `NewSQLRepo[parityItem]`. This
verifies that `$N` positional placeholders, `INSERT ... ON CONFLICT ("id") DO
UPDATE SET`, `RowsAffected()` on `DELETE`, and pagination all behave correctly
against a real SQL engine.

### Consequences

1. `go.mod` gains `require modernc.org/sqlite vX.Y.Z` and its transitive deps.
2. `go get forge-cms.dev/forge` users who never run tests are unaffected at
   runtime — the SQLite library is not linked into the produced binary.
3. CI `go test ./...` now requires a network-connected build cache on first run
   (same as any test dependency).
4. No new exported symbols. No changes to production code paths.
5. Sets the precedent: any future test-only CGO-free dependency follows the
   same three criteria and must be documented as an amendment.

---
