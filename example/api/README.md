# Smeldr API Example

A headless JSON API built with Smeldr. Demonstrates authentication, role-based
authorisation, validation hooks, and legacy URL redirects — no database required.

## Run

```powershell
cd example/api
go run .
```

The server starts on **http://localhost:8082** and seeds 10 example resources.

## Modes

### JSON API (default)

```powershell
go run .
```

All `GET` endpoints are public. Write operations require an Editor token — use
the CLI commands below (the token is signed automatically).

### HTML site

```powershell
go run . html
```

Same server, same data — list and detail routes render HTML pages when a browser
(or any client sending `Accept: text/html`) requests them. The JSON API continues
to work alongside.

## CLI commands

The binary doubles as a CLI client. Run these from a **second** terminal while
the server is running:

```powershell
.\api.exe list
.\api.exe get go-language-spec
.\api.exe create "My Resource" "https://example.com" "A description."
.\api.exe update my-resource "New Title" "https://example.com" "Updated description."
.\api.exe delete my-resource
```

Write operations use a pre-signed Editor token automatically — no copy-pasting
from the server log required.

## Endpoints

| Method | Path | Auth | Notes |
|--------|------|------|-------|
| `GET` | `/resources` | public | list published resources |
| `GET` | `/resources/{slug}` | public | single resource |
| `GET` | `/resources/go-spec` | public | 301 → `/resources/go-language-spec` |
| `POST` | `/resources` | Editor | create |
| `PUT` | `/resources/{slug}` | Editor | update |
| `DELETE` | `/resources/{slug}` | Editor | delete |
| `GET` | `/llms.txt` | public | compact AI index |
| `GET` | `/llms-full.txt` | public | full AI corpus |
| `GET` | `/resources/sitemap.xml` | public | sitemap fragment |
| `GET` | `/resources/feed.xml` | public | RSS feed |
| `GET` | `/.well-known/redirects.json` | public | redirect manifest |
| `GET` | `/robots.txt` | public | robots directives |

## Smoke test

```powershell
go build -o api.exe .
.\test-api.ps1        # 18 checks — all should pass
```
