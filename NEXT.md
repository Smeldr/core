# Next task for corepilot

## What

Improve the Go doc comments on `Table` (in storage.go) and the `At` option
description in `NewModule` (in module.go) to make irregular pluralisation
visible to any agent reading the source.

## Why

`Story` → `storys` by default. An implementer agent hit an internal server
error because neither `Table` nor `At` mentioned this class of problem. The
fix was known (`forge.Table("stories")`), but the documentation did not surface
it. This is a documentation gap, not a code bug.

## Changes

### storage.go — `Table` function doc comment

Replace current doc comment with:

```
// Table returns a [SQLRepoOption] that overrides the automatically derived
// table name for a [SQLRepo]. Use it when the default snake_case plural
// derivation does not produce the correct name — for example, types whose
// plural is not formed by appending "s" (Story → "storys", not "stories").
//
//	repo := forge.NewSQLRepo[*Story](db, forge.Table("stories"))
//	repo := forge.NewSQLRepo[*BlogPost](db, forge.Table("posts"))
```

### module.go — `NewModule` doc comment, optional options list

Find the line:
```
//   - [At]: override URL prefix (default: "/"+lowercase(TypeName)+"s")
```

Replace with:
```
//   - [At]: override URL prefix (default: "/"+lowercase(TypeName)+"s").
//     Use when the default pluralisation is wrong: Story → "/storys".
//     Example: forge.At("/solved") or forge.At("/stories").
```

## Constraints

- Doc comment changes only — no logic changes
- No new tests needed
- Update ARCHITECTURE.md if it documents the default pluralisation behaviour
- Delete NEXT.md after completing the task
- Update context/corepilot.md with the amendment number assigned at commit time
