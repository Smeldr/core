# Next milestone — Decision 30: forge.config

Full decision spec: forge-cms/forge-architect drafts/decision-30-config.md

---

## What and why

Forge needs a simple file-based configuration mechanism so that a Forge Cloud
agent can provision an instance by writing a file — without compiling Go code.

The format is plain `key = value`. No third-party dependencies. No TOML, no
YAML, no JSON. A minimal line-by-line parser written in pure Go, under 50 lines.

This is intentionally small. The goal is the load mechanism and the explicit
key-to-field mapping — not a large feature.

---

## Constraints

- Zero new dependencies
- No reflection-based mapping — the key-to-field table is explicit in code
- `secret` as a key must panic with a clear message
- Unknown keys are silently ignored (forward compatibility)
- Go-code config always wins over file config (no breaking change)

---

## Files to change

- `config.go` — add `loadConfigFile(path string) (Config, error)` and extend
  `MustConfig()` with the load order below

---

## Load order in MustConfig()

1. Explicit Go `forge.Config{}` fields (existing behaviour, unchanged)
2. Env var `FORGE_CONFIG` if set — use as file path
3. `forge.config` in working directory
4. No file — Go code and env vars only

Fields set in Go code win over fields in file.

---

## Key-to-field mapping

Define this as an explicit table in code — one place, no duplication:

| Key | Field | Type | Valid values |
|---|---|---|---|
| `base_url` | `Config.BaseURL` | string | Full URL including scheme |
| `https` | `Config.HTTPS` | bool | `true`/`false` |
| `nav_mode` | `Config.NavMode` | string | `db`, `code` |
| `org_name` | AppSchema name | string | Free text |
| `org_type` | AppSchema type | string | schema.org type, e.g. `Organization` |
| `twitter_site` | `OGDefaults.TwitterSite` | string | `@handle` |
| `og_image` | `OGDefaults.Image` | string | Relative path, e.g. `/static/og.png` |

`url` in AppSchema is always derived from `base_url` — never a separate key.
`secret` as a key must panic immediately with a descriptive message.

`nav_mode` maps to `Config.NavMode` which does not exist yet — add the field
to `Config` as a string. Valid values are `"db"` and `"code"`. Default (empty
string) is treated as `"db"`. This field will be used by Decision 29 (NavTree)
in a later milestone.

---

## Parser rules

- Lines starting with `#` are comments — skip
- Empty lines — skip
- Trim whitespace from both key and value
- No quoting required or supported
- Split on first `=` only — values may contain `=`
- Unknown keys — ignore silently

---

## Error messages

Parse errors and invalid values must produce messages that explain what is
wrong and what is valid. These messages are read by both humans and AI agents
generating `forge.config` files. Write them accordingly.

Example: `forge.config line 4: invalid value "yes" for key "https" — expected "true" or "false"`

---

## forge-site boilerplate

Add `forge.config` to `.gitignore` in forge-site (forge-cms/forge-site repo).
This is a separate commit or note — do not block the core implementation on it.

---

## What this is not

- Not nav-items — those are handled by NavTree (Decision 29, next milestone)
- Not a replacement for env vars — `secret` always comes from env
- Not a breaking change — existing Go-code config works exactly as before

---

## Task breakdown

Present your breakdown before writing any code.
