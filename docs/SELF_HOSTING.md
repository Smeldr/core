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
From `v1.180.0` the release also carries `smeldr-server-linux-arm64`,
`smeldr-server-darwin-amd64`, `smeldr-server-darwin-arm64` and
`smeldr-server-windows-amd64.exe`, and a `SHA256SUMS` file over all five.
This guide runs the Linux binary as a service.
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
ENABLE_SCHEMA_TOOLS   wire the schema store and its discovery MCP tools without the
                      rest of the block tools (ENABLE_BLOCKS includes them)
ENABLE_GOVERNANCE_RECORDS  wire core governance records (Signal, Decision, Amendment,
                      their flows and relation kinds) without the Orchestration add-on;
                      records, not access: roles and grants are ENABLE_GOVERNANCE
ENABLE_ORCHESTRATION  wire governance records and the Orchestration add-on (Task, Goal,
                      Run); implies ENABLE_GOVERNANCE_RECORDS
ORGANIZATION_NAME     the name of the Organization created at first boot (D142;
                      default: INSTANCE_NAME, else smeldr)
ORGANIZATION_FOUNDER  the user ID of the person who created the organization; at boot
                      it gets the admin and approver roles, once (D142; needs
                      ENABLE_GOVERNANCE; empty grants nothing)
ENABLE_AUTHORITY      wire the Authority mechanism (Rule, AuthorityStub, rule type ranks)
ENABLE_CHECK          wire the Check precondition on Decision ratification (requires
                      ENABLE_AUTHORITY and governance records)
INSTANCE_NAME         source name embedded in Context Packet responses (default: smeldr)
ENABLE_REDIRECTS      wire database-backed redirect management
ENABLE_PAGE_META      wire per-path SEO override store
ENABLE_MEDIA          wire local media upload and management
MEDIA_STORE_BACKEND   media backend (default: local; only "local" supported)
ENABLE_SOCIAL         wire Mastodon social publishing
MASTODON_CLIENT_ID    Mastodon OAuth client ID (required when ENABLE_SOCIAL)
MASTODON_CLIENT_SECRET Mastodon OAuth client secret (required when ENABLE_SOCIAL)
MASTODON_INSTANCE_URL  Mastodon instance base URL (required when ENABLE_SOCIAL)
ENABLE_WEBHOOKS       wire outbound webhook delivery (creates its tables at boot)
ENABLE_EVENT_STREAM   wire GET /_events/stream (the agent event push, T269)
ENABLE_EVENT_LOG      keep a durable event log behind the stream; ?after=<event id>
                      resumes (D117; requires ENABLE_EVENT_STREAM)
EVENT_LOG_RETENTION_DAYS  days the event log keeps an event (default: 30; a value that
                      is not a whole number falls back to 30 without an error)
ENABLE_SUBSCRIPTIONS  subscription records on the event stream (D116): a connection
                      without ?channel= delivers its token's subscriptions
ENABLE_STRUCTURAL_SWEEP  scheduled relation-staleness sweep, recorded as sweep runs
                      (requires ENABLE_RELATIONS)
STRUCTURAL_SWEEP_SCHEDULE  cron for the sweep (default: "0 * * * *", hourly)
ENABLE_EVAL_QUEUE_DRAIN  scheduled drain of the eval queue, recorded as sweep runs
                      (requires governance records)
EVAL_QUEUE_DRAIN_SCHEDULE  cron for the drain (default: "*/5 * * * *")
ENABLE_STANDING_DRIFT_CHECK  scheduled standing drift check, report-only: records a
                      Finding per item whose stored standing differs from its
                      state's (requires governance records)
STANDING_DRIFT_CHECK_SCHEDULE  cron for the check (default: "30 3 * * *", daily)
ENABLE_SIGNAL_EXPIRY_SWEEP  retired (D115): nothing expires; when set, boot logs that
                      it does nothing
ENABLE_CONTEXT_PACKET wire GET /packet/{type}/{slug} (Editor role; only with
                      ENABLE_RELATIONS and governance records)
ENABLE_REACHABILITY   wire GET /reachability/{type}/{id} (Author role; only with
                      ENABLE_RELATIONS)
TITLE_CUTOVER, SIGNAL_MESSAGE_CUTOVER, SIGNAL_EVENT_CUTOVER, PLAN_PATHS_CUTOVER,
REV_CUTOVER           RFC3339 times when a rule turns from a warning into a
                      refusal (see Cutovers below; default: warnings only)
(findings)            recorded in smeldr_findings by the sweeps and checks whenever
                      governance records or the structural sweep are on
ENABLE_PROVENANCE     wire transition-provenance recording (App.Provenance)
ENABLE_AGENTS         wire the agent job system (connects to this server's own /mcp endpoint)
AGENT_MCP_URL         agent MCP endpoint (default: http://127.0.0.1:PORT/mcp/message)
AGENT_MCP_TOKEN       bearer token for agent MCP calls
OAUTH_ISSUER          enable OAuth 2.1; set to canonical issuer URL (requires ENABLE_TOKENS)
OAUTH_DB_PATH         path to the OAuth SQLite database (default: ./oauth.db)
ENABLE_GITHUB         mount the GitHub adapter of the pull request check (gated merges):
                      POST /github/webhook and POST /github/core-events; needs ENABLE_TOKENS,
                      ENABLE_GOVERNANCE, ENABLE_RELATIONS, ENABLE_WEBHOOKS and
                      ENABLE_GOVERNANCE_RECORDS (or ENABLE_ORCHESTRATION)
GITHUB_APP_ID         the GitHub App's ID
GITHUB_APP_PRIVATE_KEY or GITHUB_APP_PRIVATE_KEY_FILE  the App's private key (PEM)
GITHUB_WEBHOOK_SECRET the GitHub App's webhook secret
GITHUB_ADAPTER_TOKEN_ID the token_id of a class job token granted editor
GITHUB_CORE_WEBHOOK_SECRET  optional; by default the server registers its own core webhook
GITHUB_NAMES_SHOWN    optional comma list of owner/name whose comments show names
GITHUB_CHECK_URL, GITHUB_ITEM_URL  optional link formats
```

`BASE_URL` is required (an absolute URL), `SECRET` must be at least 16 bytes,
and `ENABLE_GOVERNANCE_RECORDS` or `ENABLE_ORCHESTRATION` needs `ENABLE_RELATIONS`.
The boot is also refused when:

- `ENABLE_CHECK` is set without `ENABLE_AUTHORITY` and governance records;
- `ENABLE_EVENT_LOG` is set without `ENABLE_EVENT_STREAM`;
- `ENABLE_STRUCTURAL_SWEEP` is set without `ENABLE_RELATIONS`;
- `ENABLE_EVAL_QUEUE_DRAIN` or `ENABLE_STANDING_DRIFT_CHECK` is set without
  governance records (`ENABLE_GOVERNANCE_RECORDS` or `ENABLE_ORCHESTRATION`).

### Cutovers

Five rules start as warnings and become refusals at a time you choose:

| Variable | After it, refused |
|---|---|
| `TITLE_CUTOVER` | a Decision, Task, Goal or Amendment without a title, or with one over 120 characters (D112) |
| `SIGNAL_MESSAGE_CUTOVER` | a Signal message over 500 characters (D114) |
| `SIGNAL_EVENT_CUTOVER` | a history `signal_type` (D115) |
| `PLAN_PATHS_CUTOVER` | a commit plan without `repository` and `paths` (T1015) |
| `REV_CUTOVER` | a governance write that names no `rev` (T1010) |

Each value is an RFC3339 time, for example `REV_CUTOVER=2026-11-10T00:00:00Z`.
Unset or empty means warnings only, with no end date. A value that is not an
RFC3339 time stops the boot, with an error that names the variable.

### Schedules

The three schedulers (`STRUCTURAL_SWEEP_SCHEDULE`, `EVAL_QUEUE_DRAIN_SCHEDULE`,
`STANDING_DRIFT_CHECK_SCHEDULE`) take a 5-field cron expression, run in UTC,
and have the defaults listed above. A schedule that does not parse stops the
boot, but only when its `ENABLE_*` flag is set; otherwise it is not read.

A boot refused for a flag or a variable (a missing dependency, a value that
does not parse, a GitHub variable) exits before it writes anything to the
database, so a refused deploy leaves nothing to roll back (T955).

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

## SQLite settings

smeldr-server opens its file with three settings (since core v1.183.3, T1013):

```
smeldr.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate
```

The pragmas run in this order on every new connection, so `busy_timeout` comes first: the WAL pragma then waits for a lock another process holds instead of failing at once (T1041).

- **`journal_mode(WAL)`**: readers never block the writer, so a backup (`.backup`, `VACUUM INTO`) can run against the live file.
- **`busy_timeout(5000)`**: a write that meets another process's write lock waits up to 5 seconds instead of failing at once with `database is locked`. The maintenance subcommands (`migrate-*`, `repair-swept-edges`, `seed-role-subscriptions`) open the same file from a second process while the server runs, and wait up to 10 seconds.
- **`_txlock=immediate`**: every transaction starts with `BEGIN IMMEDIATE`, taking the write lock at once. Without it, a transaction that reads and then writes fails at once if another process wrote in between (`database is locked (517)`, a stale snapshot), and `busy_timeout` does not help with that. Smeldr's transactions read before they write, so this setting matters whenever a second process writes the file.

If you build your own server on Smeldr with SQLite (modernc.org/sqlite), open the file the same way, above all when anything else writes it: a migration, a script, a second instance. Before these settings, a Story scheduled for the same second as a Post on smeldr.dev published a minute late, because its save met the Post's write lock (T1008).

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
