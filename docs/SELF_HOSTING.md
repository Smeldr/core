# Self-hosting Smeldr

This covers running a Smeldr instance outside this repo's own
dogfooding deploy — downloading a release binary, the environment
variables it reads, and how to restore your own data from a backup.
It documents `example/server`, the generic Smeldr server this repo
ships and dogfoods against — not a separate product.

## Get the binary

Every tagged release from `v1.67.0` onward attaches a Linux amd64
binary, `smeldr-server-linux-amd64`, built via
[`.github/workflows/release.yml`](../.github/workflows/release.yml).
Download it from the release's own page:

```bash
curl -LO https://github.com/Smeldr/core/releases/download/vX.Y.Z/smeldr-server-linux-amd64
chmod +x smeldr-server-linux-amd64
```

Only Linux amd64 is built today — the platform this project's own
instance runs on, and the one every manual build this project has
ever done has targeted. If you need another platform, build from
source instead (below); say so if you need it attached to releases
going forward.

### Or build from source

```bash
git clone https://github.com/Smeldr/core.git
cd core/example/server
CGO_ENABLED=0 go build -o smeldr-server .
```

## Configuration

Every setting is an environment variable — there is no config file.
`SECRET` is the only one that's required; the binary starts and runs
with nothing else set, every other subsystem is opt-in.

```
SECRET   HMAC signing secret (min 32 bytes in production) — required
```

```
BASE_URL              canonical origin (e.g. "https://cms.example.com")
DATABASE_PATH         path to the SQLite database (default: smeldr.db)
PORT                  HTTP listen port (default: 8080)
ADDR                  full listen address (default: 127.0.0.1:PORT)

ENABLE_TOKENS         wire database-backed named token management
ENABLE_GOVERNANCE     wire role-based access control (requires ENABLE_TOKENS for OAuth)
ENABLE_RELATIONS      wire the relation graph store
ENABLE_DYNAMIC_CONTENT wire the runtime content type system and schema store
ENABLE_BLOCKS         wire the block/composition system MCP tools
ENABLE_ORCHESTRATION  wire orchestration types (Signal, Task, Decision, Amendment, Goal)
INSTANCE_NAME         source name embedded in Context Packet responses (default: smeldr-dogfood)
ENABLE_REDIRECTS      wire database-backed redirect management
ENABLE_PAGE_META      wire per-path SEO override store
ENABLE_MEDIA          wire local media upload and management
MEDIA_STORE_BACKEND   media backend (default: local; only "local" supported)
ENABLE_SOCIAL         wire Mastodon social publishing
MASTODON_CLIENT_ID    Mastodon OAuth client ID (required when ENABLE_SOCIAL)
MASTODON_CLIENT_SECRET Mastodon OAuth client secret (required when ENABLE_SOCIAL)
MASTODON_INSTANCE_URL  Mastodon instance base URL (required when ENABLE_SOCIAL)
ENABLE_WEBHOOKS       wire outbound webhook delivery
ENABLE_PROVENANCE     wire transition-provenance recording (App.Provenance)
ENABLE_AGENTS         wire the agent job system (connects to this server's own /mcp endpoint)
AGENT_MCP_URL         agent MCP endpoint (default: http://127.0.0.1:PORT/mcp/message)
AGENT_MCP_TOKEN       bearer token for agent MCP calls
OAUTH_ISSUER          enable OAuth 2.1; set to canonical issuer URL (requires ENABLE_TOKENS)
OAUTH_DB_PATH         path to the OAuth SQLite database (default: ./oauth.db)
```

This list is `example/server/main.go`'s own doc comment, kept in sync
by hand — if the two ever disagree, the source is correct and this
file is stale; read the doc comment directly rather than assume this
copy is current.

## Running it

```bash
SECRET="$(openssl rand -hex 32)" \
BASE_URL="https://your-domain.example" \
DATABASE_PATH="/var/lib/smeldr/smeldr.db" \
./smeldr-server-linux-amd64
```

### systemd

A minimal unit, enough to keep the process running and restart it on
failure — not a hardened production configuration:

```ini
[Unit]
Description=Smeldr server
After=network.target

[Service]
ExecStart=/opt/smeldr/smeldr-server-linux-amd64
WorkingDirectory=/opt/smeldr
EnvironmentFile=/opt/smeldr/smeldr.env
Restart=on-failure
RestartSec=5
User=smeldr

[Install]
WantedBy=multi-user.target
```

`/opt/smeldr/smeldr.env` holds the environment variables above, one
`KEY=value` per line, outside version control (it carries `SECRET`).

## Your data, and restoring it

Everything lives in the single SQLite file at `DATABASE_PATH`
(`smeldr.db` by default), opened in WAL mode — alongside it you'll
find `smeldr.db-wal` and `smeldr.db-shm`, both part of the same live
database, not separate data. A consistent backup needs all three
files together, or a proper SQLite-aware backup (e.g. the `.backup`
command, or stopping the process before copying).

**Restoring** is copying that file (plus its `-wal`/`-shm` siblings, if
present) back to `DATABASE_PATH` and starting the binary — there is no
separate import step or migration to run. If you're also running
`OAUTH_DB_PATH`'s own separate SQLite file (only when `OAUTH_ISSUER`
is set), restore it the same way, independently — it's a second,
unrelated database, not part of the main one.

If you've never actually restored from one of your own backups,
that's worth doing once before you need it for real — the first
restore is always the one that finds the gap in a backup process that
looked fine on paper.
