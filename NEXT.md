# Next — Amendment A67: `forge_html` template function

## What

Add a `forge_html` template function to `templatehelpers.go` that wraps a `string`
value as `template.HTML`, enabling trusted raw HTML to be rendered unescaped in
module templates.

## Why

Go's `html/template` escapes all string output by default. There is no way for a
module template to render pre-rendered HTML (e.g. a video embed iframe) without a
trusted passthrough function. This gap was discovered during planning of the
forge-cms.dev demo page (S45), which needs to embed an iframe alongside markdown
content. Both `forge_html` and `forge_markdown` will be used on the same page —
they are independent functions in the same TemplateFuncMap.

## Constraints

- Add the function to the existing `TemplateFuncMap` in `templatehelpers.go`
- Function name in templates: `forge_html`
- Input: `string`. Output: `template.HTML`
- No new files. No changes to public API or interfaces
- Add tests in `templatehelpers_test.go` (or equivalent)
- Document as Amendment A67 in `DECISIONS.md` and `decisions/phase2.md`
- Version bump to v1.7.0
- Update `ARCHITECTURE.md` and `CHANGELOG.md`
- Delete this file after committing
