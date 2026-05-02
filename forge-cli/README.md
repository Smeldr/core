# forge-cli

Command-line interface for Forge CMS instances. Manage content and tokens
from a terminal or CI/CD pipeline.

Zero third-party dependencies — requires only Go 1.22 or later.

---

## Installation

```bash
go install forge-cms.dev/forge-cli@latest
```

Or build from source:

```bash
git clone https://github.com/forge-cms/forge
cd forge/forge-cli
go build -o forge-cli .
```

---

## Configuration

Set environment variables or create a `.forge-cli.env` file in your working
directory (values already set in the environment take precedence):

```env
FORGE_URL=https://mysite.com
FORGE_TOKEN=my-bearer-token
FORGE_MCP_URL=https://mysite.com/mcp/message
```

`FORGE_MCP_URL` defaults to `{FORGE_URL}/mcp/message` if not set. It is only
required for `token` commands.

---

## Content commands

All content commands take the URL path prefix of the content type as the first
argument (e.g. `posts`, `pages`).

### Create a draft

```bash
forge-cli posts create --from post.md
```

`--from` reads a YAML-subset frontmatter file. Omit `--from` to read from stdin.

Frontmatter format:

```
---
Title: My Post
Body: Hello world
Tags: [go, forge]
---
Optional body text appended to Body if Body is blank in the header.
```

### Update (field overlay)

```bash
forge-cli posts update my-post --from updated.md
```

GETs the existing item and overlays only the fields present in the file.
Fields absent from the file are preserved unchanged.

### Lifecycle transitions

```bash
forge-cli posts publish my-post
forge-cli posts unpublish my-post
forge-cli posts archive my-post
```

### Delete

```bash
forge-cli posts delete my-post
```

### List

```bash
forge-cli posts list
forge-cli posts list --status draft
forge-cli posts list --status published
```

### Get a single item

```bash
forge-cli posts get my-post
```

---

## Token commands

Token commands require `FORGE_MCP_URL` and an Admin-role token in `FORGE_TOKEN`.

### Create a token

```bash
forge-cli token create ci-deploy author 30
```

Arguments: `<name> <role> <ttl-days>`. Roles: `guest`, `author`, `editor`,
`admin`. TTL is an integer number of days (e.g. `30` for 30 days). Prints
the plaintext token once — copy it immediately.

### List tokens

```bash
forge-cli token list
```

### Revoke a token

```bash
forge-cli token revoke <id>
```

Revocation is permanent and takes effect immediately.

---

## Status check

```bash
forge-cli status
```

Calls `GET /_health` and prints the JSON response. Exits non-zero if the
server is unreachable.

---

## Changelog

See [CHANGELOG.md](CHANGELOG.md).
