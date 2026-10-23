# Smeldr — developer skill

Smeldr is a Go content framework. This skill covers what you need to work
with Smeldr as a developer or agent.

Current versions: smeldr.dev/core v1.171.0 · smeldr.dev/mcp v1.67.0 · smeldr.dev/oauth v0.6.0 · smeldr.dev/media v1.7.0 · smeldr.dev/cli v0.30.0 · smeldr.dev/social v0.11.0 · smeldr.dev/agent v0.9.2 · smeldr.dev/core/pgx v0.3.0

---

## Intellectual foundation

Smeldr has a constitution and vocabulary layer in `smeldr/architect/constitution/`.
Read `constitution.md`, `vocabulary.md`, and `cloud-strategy.md` when making
architecture decisions or designing new MCP or developer-facing surfaces.

**Two vocabularies — keep them on separate surfaces.**

| Internal (constitutional) | External (market / developer-facing) |
|---------------------------|---------------------------------------|
| Managed Item, Authority, Lineage, Evidence, Participant, Operational platform | content, permissions, history, AI-native content backend |

- Constitutional vocabulary belongs in `constitution/`, design docs, DECISIONS.md, and NEXT.md.
- Market vocabulary belongs in README, smeldr.dev copy, MCP tool descriptions, and public docs.
- The skill and public docs are a bridge surface — convergence is deliberate, not accidental.

**Publication gate:** no external surface adopts constitutional vocabulary until a second
domain ships. Until then, lead externally with "AI-native content backend".

**Concept-to-code mapping:** `vocabulary.md` is the authoritative binding
(e.g. Managed Item → `Node` / `DynamicNode`). The code keeps its earned names.

---

## Core concepts

```
Node      — base struct every content type embeds (ID, Slug, Status, timestamps)
Module    — one content type, fully wired (routes, repo, MCP, signals)
Signal    — hook that fires on lifecycle changes (AfterPublish, AfterArchive, ...)
Head      — all page metadata (SEO, OG, JSON-LD, canonical)
Role      — Guest < Author < Editor < Admin
Config    — smeldr.config file + Go struct; Go-code values win
```

Content lifecycle: `Draft → Published/Scheduled → Archived`
Archived items are permanently invisible — cannot be reverted to Draft.

---

## Defining a content type

```go
type Story struct {
    smeldr.Node
    Title   string `smeldr:"required" json:"title"`
    Body    string `smeldr:"required,min=50" json:"body"`
    Image   string `smeldr:"" smeldr_description:"Hero image path." db:"image" json:"image"`
    OGImage string `smeldr:"" smeldr_description:"OG image URL." db:"og_image" json:"og_image"`
}
```

**json tag is required on every custom field** — all fields beyond `smeldr.Node`.
Without an explicit `json:"snake_case"` tag, Go serialises the field as PascalCase,
which breaks MCP read and write operations (MCP uses snake_case keys).
`smeldr.Node` fields are exempt — they are handled internally.

Wrong (MCP returns empty/missing values):
```go
type MyPage struct {
    smeldr.Node
    Title string `db:"title"`   // ← missing json tag — MCP cannot map "title" → Title
    Body  string `db:"body"`
}
```

Correct:
```go
type MyPage struct {
    smeldr.Node
    Title string `db:"title" json:"title"`
    Body  string `db:"body"  json:"body"`
}
```

---

## Wiring a module

```go
app.Content(smeldr.NewModule((*Story)(nil),
    smeldr.At("/solved"),
    smeldr.Table("stories"),       // override incorrect pluralisation
    smeldr.Repo(smeldr.NewSQLRepo[*Story](db)),
    smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
))
```

---

## Routing variants (v1.23.0+)

### SingleInstance — singleton page at module prefix

Use when a module holds exactly one canonical item (About, Contact, Terms):

```go
smeldr.NewModule((*AboutPage)(nil),
    smeldr.At("/about"),
    smeldr.Repo(repo),
    smeldr.SingleInstance(),
    smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
)
// GET /about → serves first Published item
// GET /about/{slug} → 404 (not registered)
```

MCP behaviour:
- `list_{type}s` tool suppressed (`MCPMeta.SingleInstance = true`)
- `get_{type}`, `update_{type}`, `publish_{type}`, `archive_{type}`, `delete_{type}` present
- `create_preview_url` returns `/{prefix}?preview=<token>` — no slug in path (forge-mcp ≥ v1.10.2)

**Pattern: SingleInstance + custom public handler**
When the public URL differs from the module prefix (e.g. homepage at `/`, module at `/homepage`):

```go
// Module at /homepage — admin + MCP surface only
app.Content(smeldr.NewModule((*HomePage)(nil),
    smeldr.Repo(homePageRepo),
    smeldr.At("/homepage"),
    smeldr.SingleInstance(),
    smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
))

// Public route — custom handler reads the published record
app.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    hps, _ := homePageRepo.FindAll(r.Context(), smeldr.ListOptions{
        Status: []smeldr.Status{smeldr.Published},
    })
    hp := homePageDefaults()
    for _, p := range hps {
        if p.Slug == "home" { hp = p; break }
    }
    // render hp ...
}))
```

When to use: exactly one record; no list page; managed via MCP/CLI; public URL may differ.
When NOT to use: multiple records; module prefix IS the public URL; per-item slug URLs needed.

### Standalone — top-level /{slug} routing

Use when items should appear at `/{slug}` rather than `/{prefix}/{slug}`:

```go
smeldr.NewModule((*Post)(nil),
    smeldr.At("/posts"),
    smeldr.Repo(repo),
    smeldr.Standalone(),
)
// GET /my-post     → serves Published Post with slug "my-post"
// GET /posts       → list of Published posts (unchanged)
// GET /posts/my-post → 404 (not registered)
```

Multiple Standalone modules coexist — slug dispatch is first-match.
AIDoc served at `GET /{slug}/aidoc` when `smeldr.AIIndex(smeldr.AIDoc)` is also set.

### APIOnly — no public HTML surface (v1.24.0+)

Use when a content type is managed exclusively via MCP or CLI with no public web presence:

```go
smeldr.NewModule((*HomePage)(nil),
    smeldr.At("/home-pages"),
    smeldr.Repo(repo),
    smeldr.MCP(smeldr.MCPWrite),
    smeldr.APIOnly(),
)
// GET /home-pages Accept:application/json → 200 JSON
// GET /home-pages Accept:text/html        → 404
// MCP tools: full set (list, get, create, update, publish, etc.)
```

`APIOnly()` + `SingleInstance()` panics at startup — incompatible combination.

### SiteConfig — built-in singleton (v1.29.0+)

`SiteConfig` is a built-in singleton content type for site-wide defaults configurable
via MCP after first deploy. Fields: `site_name`, `title_separator`, `og_image`,
`x_handle`, `head_script`. Registration:

```go
smeldr.CreateSiteConfigTable(db)          // once at startup
app.Content(smeldr.NewSiteConfigModule(db)) // registers MCP tools
```

Configure via `create_site_config` MCP tool (Admin role). No code redeployment needed.

### HeadAssets.RawHead — verbatim head HTML (v1.30.0+)

`HeadAssets` accepts a `RawHead template.HTML` field injected verbatim into `<head>`
after all other HeadAssets output (preconnect → stylesheets → links → scripts → RawHead).
Use for analytics snippets, preload hints, or custom head HTML that does not fit the
structured fields. Zero value is a no-op.

```go
app.SEO(&smeldr.HeadAssets{
    Scripts: []smeldr.ScriptTag{{Src: "/static/app.js", Defer: true}},
    RawHead: template.HTML(`<link rel="preload" href="/fonts/inter.woff2" as="font" crossorigin>`),
})
```

---

## Signal bus (v1.20.0+)

`app.OnSignal` registers a subscriber for a lifecycle signal. Handler contract:
enqueue work and return immediately — never block. Errors are logged and never
propagated to the publish caller.

```go
app.OnSignal(smeldr.AfterPublish, func(ctx context.Context, ev smeldr.SignalEvent) error {
    // ctx is detached from the request (WithoutCancel) — safe to enqueue async work
    return myQueue.Enqueue(ctx, ev)
})
```

Signal constants: `AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete`

`SignalEvent` fields: `Type`, `Slug`, `Title`, `URL`, `Timestamp`, `PreviousState`, `ActorRole`, `ActorID`

`PreviousState` and `ActorRole` are transient — not reconstructable via MCP after the fact.

**Built-in audit trail (v1.22.0+):** `app.Audit(smeldr.NewAuditStore(db))` subscribes to
`AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete` and persists each transition.
`GET /_audit` (Editor+) returns JSON records. `smeldr-cli audit list` prints a table.

---

## Log capture (v1.36.0+)

Opt-in, in-memory capture of recent log records for live debugging — works over
plain HTTP even when MCP is down. `app.CaptureLogs(...)` installs a teeing
`slog.Handler`: records still reach the existing handler (stderr) AND records
at/above the capture level are stored in a bounded ring.

```go
app.CaptureLogs() // ring of 500, WARN+ (defaults)
// app.CaptureLogs(smeldr.WithLogCapacity(1000), smeldr.WithLogLevel(slog.LevelInfo))
```

`GET /_logs` (Admin, bearer) returns `{capacity, count, dropped, entries}` with
entries newest-first. Query params: `level` (min level, inclusive), `limit` (most
recent N), `since` (RFC3339). Route is absent (404) unless `CaptureLogs` was called.

- **Not log storage** — in-memory, lost on restart; stderr stays the durable path.
- **Ordering** — call `CaptureLogs` AFTER any app-side `slog.SetDefault`.
- **Zero-config** — when no custom handler is set, capture forwards to a stderr text
  handler (wrapping slog's built-in handler would deadlock via the log package).
- **No MCP tool** by design — use `smeldr-cli logs` (calls `/_logs` over HTTP).

---

## Event stream (v1.73.1+)

Opt-in, in-memory push of live content-lifecycle and state-flow-transition
events over a held-open NDJSON connection — built for an agent/listener
process that cannot receive an inbound webhook (behind NAT, no public IP)
and wants live events without polling `list_signals`/`get_valid_transitions`
on a timer.

```go
app.EventStream() // mounts GET /_events/stream
```

`GET /_events/stream` (Author role, bearer auth — same contract as `/_logs`/
`/_audit`) holds the connection open and writes one NDJSON line per event,
flushed immediately. Same payload envelope and event names as an outbound
webhook delivery (`"{type}.created"` … `"{type}.transitioned"`,
`"signal.created"`) — `{"id","event","timestamp","data"}` per line. Exceptions that are
webhook-only and never published to the stream: `signal.transitioned` (v1.106.0, a Signal's own
transition could only echo back to the session that made it) and every `amendment.*`
event (v1.107.0, no routing field, so each one woke every listener). Also since v1.107.0 an
event is not delivered to the connection whose own token caused it (`?include_own=true` opts
back in; system-originated events are never suppressed). A
`{"type":"ping"}` heartbeat arrives every 25s while idle, to survive
reverse-proxy idle timeouts.

- **Independent of `app.Webhooks(...)`** — works whether or not outbound
  webhook delivery is also configured; the two are separate consumers of
  the same event data, either or both.
- **At-most-once without the event log**: a dropped connection misses
  whatever fired in the gap.
- **Durable event log** (core v1.160.0, D117): `app.EventLog(smeldr.EventLogOptions{})`
  (example/server `ENABLE_EVENT_LOG`) keeps every event for 30 days. To resume:
  - reconnect with `?after=<last event id>`;
  - the missed events arrive first, in order, then `{"type":"caught_up"}`, then live events;
  - `{"type":"skipped"}` means the cursor was older than the retention;
  - delivery is then at least once: deduplicate by `id`.

  From Go, `app.EventsAfter(ctx, after, limit)` pages with `Next` until
  `More` is false. `app.UnloggedEvents()` counts events that could not be
  logged. A connection whose buffer (256) fills is closed: reconnect with
  `?after=`. Under governance, events about items the token cannot read
  are not delivered.
- **Channels:** `?channel=<name>` receives the events routed to that channel
  plus true broadcasts; `all` (or no parameter) receives everything. Since
  core v1.138.0 (A453) `?channel=` takes a comma-separated list, e.g.
  `?channel=architect,type:task_plan`, each event delivered once. A
  runtime-defined type routes its events on the string field with schema role
  `channel` (one per schema): to the field's value and to the topic
  `type:<type name>`, the topic alone for an empty value; a type without that
  role is a broadcast. Further topics: `relations`, `amendments`, `signals`,
  `types` (`content_type.redefined`, core v1.140.0). An older core reads a
  list as one channel name.
- **Subscriptions** (core v1.162.0, D116; `app.Subscriptions()`, example/server
  `ENABLE_SUBSCRIPTIONS`): a `Subscription` record (address `N<n>`) is an
  owner's filter (types, events, event names, from/to states, items,
  `related_to`, routing fields such as band or priority, Signal kinds,
  `addressed_to_me`, `sent_by_me`), matched at delivery and on replay.
  - `?subscription=N<n>` delivers one and resumes from its saved cursor.
  - **A connection with no `?channel=` now gets its token's subscriptions,
    not everything.** Pass `?channel=all` for everything you may read.
  - Every token has three defaults (requests to me, conditions I can
    resolve, outcomes of my requests); `seed-role-subscriptions` adds a
    role's. Since core v1.163.0 it prints `{"created": [...], "left_out":
    [{"name", "type"}]}` (both always lists) and names each left-out
    subscription on stderr. Before v1.163.0 every `smeldr-server`
    subcommand missed the runtime-defined types, so `task_plan`
    subscriptions were silently left out (T859).
    Since v1.164.0 "Tasks in band X" also delivers `updated` (a priority
    set later) and `--architect` adds "Tasks done, blocked or resolved". A
    re-seed never changes an existing subscription of the same name; use
    `update_subscription`.
  - Since v1.164.0 every `signal.created`/`signal.transitioned` carries the
    Signal's `address`, and relation events carry `source_address` and
    `target_address` (omitted when an end has none).
  - Since core v1.165.0 an edge a reference field writes at create (a
    Signal's `about`, Task `implements`, Amendment `ships_as`, a runtime
    type's `refers_to`) sends `relation.asserted` (new_row true). Before, a
    raw Signal insert sent none, and on SQLite none of them did.
  - Since core v1.166.0 core governance (Signal, Decision, Amendment:
    `CreateGovernanceTables`, `RegisterGovernanceTypes`,
    `RegisterGovernanceRelationKinds`) and the Orchestration add-on (Task,
    Goal, Run: `...OrchestrationAddOn...`) register separately; the old
    combined calls and `ENABLE_ORCHESTRATION` still mean both, and
    `ENABLE_GOVERNANCE_RECORDS` wires core governance alone. Without the
    add-on a `task_ref` is refused naming it, and mcp v1.64.2 does not list
    `get_goal_context`.
  - Since mcp v1.64.1 the relation tools (`get_relations`,
    `get_reachability`, `assert_relation`, `propose_relation`,
    `observe_relation`, `preview_impact`) take an address, slug or ID. An
    unknown or hidden item is not found, never an empty list.
  - The read scope reloads on open connections when grants or anchored
    relations change (and every 60 s); a revoked token's connections close.
  - Webhook endpoints deliver through their own subscription;
    `migrate-webhook-subscriptions` backfills existing ones.
- Route is absent (404) unless `EventStream` was called. No MCP tool by
  design, same reasoning as `/_logs` — this exists for exactly the
  situation where an agent has only this one HTTP connection to lean on.
- **Per-token connection cap (v1.73.1+)**: max 4 concurrent connections per
  token — a 5th attempt gets 429 before any header is written, not a hang.
  A normal listener holding its one connection never approaches this;
  reconnect immediately rather than retrying in a tight loop if you ever
  hit it.

Example listener (any language, illustrating the shape — `curl -N` for a
quick manual check):

```bash
curl -N -H "Authorization: Bearer <token>" https://example.com/_events/stream
```

---

## Custom state flows (v1.44.0+)

Call `app.RegisterFlow(smeldr.StateFlow{...})` at startup (after `smeldr.New`) to
register a data-driven state machine for a content type. Idempotent — safe to call
on every restart. State flows are enforced on every supported database (v1.118.0, D103): SQLite and Postgres alike. Before v1.118.0 a flow was stored and never consulted on Postgres through core/pgx (a transition's role gate was not checked, Locked locked nothing); from v1.118.0 it is, with no opt-out, so an upgrade can refuse transitions that passed unchecked. Since v1.119.0 ConflictReject and ConflictSupersede are also exclusive across several processes on one Postgres database through core/pgx (an advisory lock the adapter provides through AcquireLock); a handle without that method, SQLite and a wrapper that hides it keep the in-process lock only. EnsureColumn and the boot migrations work on Postgres. Core opens a transaction for the conflict policy, audited governance mutations and relation diffs only when the handle has BeginTx; core/pgx adds it in adapter v0.3.0 (before, those ran statement by statement on Postgres), and since core v1.119.1 the last_actor fail-open of an older table runs under a savepoint inside that transaction. Runtime-defined (dynamic) content types work on Postgres since core v1.119.2 (a json.RawMessage field now scans from a string; before, every read of a dynamic item failed there); PageMetaStore (SEO overrides) works on Postgres since core v1.119.3. smeldr.dev/media (v1.7.0) and smeldr.dev/social (v0.11.0) run on Postgres too, with a legacy `forge_*` table renamed through `smeldr.RenameLegacyTables(ctx, db, pairs)` (core v1.136.0); in social, deleting a credential that posts still use is refused (409) and deleting a post also deletes its delivery log. smeldr.dev/oauth stays SQLite-only by D106 (its own file; implement `oauth.Store` to keep OAuth state elsewhere). Since core v1.134.0 a dynamic type cannot hold two items under one slug: a unique index on (type_name, slug), skipped with a boot warning while existing duplicates remain.

```go
err := app.RegisterFlow(smeldr.StateFlow{
    Name:     "agent-job",
    TypeName: "AgentJob",  // Go type name — maps to "agent_jobs" table
    States: []smeldr.State{
        {Name: "draft",     IsInitial: true},
        {Name: "published"},
        {Name: "paused",    SuppressesSignals: true},
        {Name: "archived",  IsTerminal: true},
    },
    Transitions: []smeldr.Transition{
        {From: "draft",     To: "published"},
        {From: "published", To: "paused"},
        {From: "paused",    To: "published"},
        {From: "published", To: "archived", RequiredRole: "Editor"},
    },
})
```

- `IsInitial` — entry point for new items (exactly one per flow)
- `IsTerminal` — no outbound transitions (e.g. "archived")
- `SuppressesSignals` — After* hooks do not fire for items in this state
- `RequiredRole` — stored in `smeldr_transitions` for future per-transition role enforcement (not yet enforced; planned for a later T23 step)
- Requires `Config.DB`. On unknown existing item states, returns error — treat like a failed migration.

**ConflictPolicy (v1.46.0+, A186):** Opt-in uniqueness enforcement at a designated state.

```go
err := app.RegisterFlow(smeldr.StateFlow{
    Name:           "article-flow",
    TypeName:       "Article",
    ActiveState:    "published",
    ConflictPolicy: smeldr.ConflictReject, // or smeldr.ConflictSupersede
    States: []smeldr.State{
        {Name: "draft", IsInitial: true},
        {Name: "published"},
        {Name: "archived", IsTerminal: true},
    },
    Transitions: []smeldr.Transition{
        {From: "draft", To: "published"},
        {From: "published", To: "archived"},
    },
})
```

- `ConflictReject` — `MCPPublish`/`SetStatus` returns `ErrConflict` (409) when another item is already in `ActiveState`
- `ConflictSupersede` — transitions the other items in `ActiveState` to `"superseded"` when an item enters it (since v1.112.1 the entering item's own write comes first, then the others, in one transaction for `TransitionItemVia` and dynamic types; the Module lifecycle methods are not atomic and can leave two active items, never none). Since v1.111.0 it also asserts a `supersedes` relation edge winner -> loser (by ID, triggering actor as `created_by`), only when `App.Relations` is wired and a `supersedes` kind permitting `Type -> Type` is registered (the orchestration kind permits Decision -> Decision only; a customer type needs its own `UpsertKind`, keeping the Decision pair). Since v1.115.1 transitions into the active state are serialised per type within the process. Since v1.116.0 the policy also applies on HTTP PUT status changes, the scheduler publish, DrainEvalQueue and ScheduleContent (a rejected PUT is 409; a rejected scheduled item stays scheduled and is retried every tick).
- Zero value = no enforcement. Both policies fail-open: DB errors never block a transition.

**Standing (v1.112.0, D100):** a flow `State` may declare `Standing: smeldr.StandingHolds` ("in force"; other values rejected by `RegisterFlow`). Each item of such a type stores `holds` / `ceased` / `none` / `not recorded` (no row = `none`) in `smeldr_standing`, written only by the code that changes state: entering a holding state stores `holds`, leaving one for an untagged state `ceased`, untagged to untagged changes nothing. Events `standing-began` / `standing-ended` go to provenance (when wired) with the transition's actor, so ratifying a Decision reads as two entries. Smeldr's tags: Decision `ratified`/`pending-re-evaluation`, Amendment `merged`; Signal, Task, Goal have none. Read with `ItemStanding` / `CountStanding` or the context packet's `standing`; separate from the governed state name. `MigrateStanding` (boot, once per type, from the flow graph as registered then, no guessing) gives pre-existing items a standing; retagging never recomputes; `App.CheckStandingDrift` only reports. mcp `define_state_flow` does not pass `standing` yet. For a page of items use `TypeHasStanding` then `ItemStandings` (v1.114.0): one query per 400 ids, `none` for an item with no row, empty map for a type without standing.

**Decision freshness (v1.47.0+, A187):** Wire scheduled state transitions via `TransitionTrigger`.

```go
err := app.RegisterFlow(smeldr.StateFlow{
    Name:     "governance-decision",
    TypeName: "Decision",
    States: []smeldr.State{
        {Name: "proposed", IsInitial: true},
        {Name: "ratified"},
        {Name: "pending-re-evaluation"},
    },
    Transitions: []smeldr.Transition{
        {From: "proposed", To: "ratified"},
        {From: "ratified", To: "pending-re-evaluation"},
        {From: "pending-re-evaluation", To: "ratified"},
    },
    Triggers: []smeldr.TransitionTrigger{
        {
            FromState:    "proposed",
            ToState:      "ratified",
            TriggerClass: "async",
            TriggerType:  "schedule-eval",
            Config:       `{"eval_field":"next_eval_at","to_state":"pending-re-evaluation"}`,
        },
    },
})
```

- `schedule-eval` reads `eval_field` from the item row and inserts a timed entry in `smeldr_eval_queue`
- `App.DrainEvalQueue(ctx)` transitions due items; call via `agent.NewEvalQueueScheduler("", "UTC", app)` for automatic drain
- Fail-open: nil DB, missing table, and empty `eval_field` are all silently skipped

---

### Task flow (agent-task)

`backlog -> active -> waiting-plan -> plan-reviewing -> implementing -> commit-reviewing -> done`, plus side doors:
`backlog`/`active` -> `deferred`; `backlog`/`active`/`waiting-plan`/`plan-reviewing` -> `resolved` (reason required); `active` -> `done` (reason required, work that ends without a plan/commit cycle).

Return and block doors (v1.108.0, A391), all additive:

- `commit-reviewing` -> `implementing` (reason required): a reviewer returns the Task to the implementer after a failed review.
- `implementing` -> `blocked` (reason required): the implementer stops mid-build to ask instead of guessing. The reason states what is being asked.
- `blocked` -> `implementing`: the question is answered and the build resumes. `blocked` -> `active` still exists.

`blocked` does not remember where it was entered from. A block raised mid-build resumes to `implementing`, not `active` (which would re-enter planning). None of these doors needs an operation; who may return a Task is governed by the process, not the flow. Use `transition_item` (or `smeldr-cli transition Task <slug> --to <state> --reason "<text>"`).

### Transition provenance (v1.109.0, A392)

With `App.Provenance` wired, every successful state change through `transition_item` (`App.TransitionItem`/`TransitionItemWithReason`, and `App.TransitionItemVia(ctx, surface, ...)` which they delegate to), `DynamicTypeRepo.SetStatus`/`ScheduleContent` and `POST /_content/{type}/{id}/status` writes a `ProvenanceRecord` (verb `transition`, from/to, actor, actor kind, surface, reason). A rejected transition records nothing; a failed write never fails the transition.

- **Not backfilled.** Transitions made before the upgrade have no record.
- **Nothing is recorded unless provenance is enabled.** The example server wires `App.Provenance` only when `ENABLE_PROVENANCE` is set. Check that before expecting history.
- The older methods record an empty surface; `TransitionItemVia` names it (`mcp`, `http`, `cli`, `trigger`). The status endpoint records `http` and, since v1.109.1, the authenticated actor. v1.109.1 is a security fix (A393): that endpoint used to pass no actor to the transition gate, so any Editor could perform a non-Strict `RequiredOperation` transition without the operation; it now returns 403 without it (400 when a reason is required). Since v1.110.0 the `ConflictSupersede` side effect (the superseded item: triggering actor, that transition's surface, reason `superseded by <Type> <id>`, no event fires for it) and each Signal expired by `App.ExpireSignals` (job `signal-expiry-sweep`, surface `trigger`) are recorded too; since v1.159.0 the sweep is retired and core's Signal resolver records its resolutions as `signal-resolver`.
- Readers see the actor only for transitions that required an operation under `Strict` (`SubjectProvenance`), so a Task state change reads as verb, states and date; a Decision ratification also shows who did it.
- **Pull request check (core v1.171.0, mcp v1.67.0, cli v0.30.0, T918):** `ChangeCheck` (`CHK<n>`) per checked commit of a pull request; read with `get_change_check`/`list_change_checks` (by repository/number, area or decision; cli `smeldr-cli check show|list`); state is the verdict (commented, awaiting-approval, approved, not-approved, superseded). Made only by the host adapter via check_change. A third Area mode audit-after-merge never blocks; record_merge opens a merge-unapproved condition, resolved with answer_change_check. Approve per Area with answer_change_check (never answer_signal); an agent only for an Area it is area-approver on, or a Check delegated to it as item-change-approver.
- **Pinned sources (core v1.171.0, mcp v1.67.0, cli v0.30.0, T914):** `sources` on a Decision: [{repository, commit: full 40-char SHA (never a branch), path: one file, anchor?, role: source|evidence|implementation, hash?: sha256:<64 hex>}], max 20. Set while proposed; ratified is locked, so re-pin by superseding.
- **Whom to ask (core v1.170.0, mcp v1.67.0 `approvers_of`, cli v0.30.0 `smeldr-cli approvers <place>`, T911):** lists who approves a place you can read: token name, role, class, anchor address. No administer needed.
- **Gotcha (core v1.169.0, T910):** changing an Area's `approval_mode`, archiving or deleting an Area, and changing, archiving or deleting a coverage rule need `ratify` over the Area (approver, or the Area's domain-approver); adding a rule needs `approve-change` over the Area (an agent area-approver may). Agent tokens get `ErrForbidden` for the ratify acts.
- **Gotcha (core v1.169.0, T906):** placing or moving a Decision that is no longer proposed needs `ratify` over the old and the new place (approver anywhere, domain-approver within its Domain and its Areas); an Area that already had a Domain moves only the same way. Agent tokens get `ErrForbidden`: a human approver places or moves them. Proposed Decisions move at the Author tier.
- **Coverage (core v1.168.0, mcp v1.66.0, cli v0.29.0):** ask `decisions_for_paths` {repository: "owner/name", paths: [...]} before writing code: it returns the Areas whose coverage rules match, the Decisions in force placed in them, their Domains and the Organization, and the uncovered paths (Author, at most 3000 paths, read rules). Rules are `CoverageRule` records (`create_coverage_rule` with `area`, `repository`, `pattern`; `*`, `?`, `**`; Area write-once; changing pattern, repository or kind clears `match_count`/`checked_at` until the next check, core v1.168.1, T901). `check_coverage` (Editor) with the full file list opens `coverage-matches-nothing` for a rule that matches nothing. CLI: `decisions-for-paths --repo owner/name --stdin`, `coverage check --repo owner/name --stdin`.
- **Gotcha (core v1.168.0):** the organization place is the type `Organization` (tools `*_organization`, `list_organizations`, prefix `ORG`, relation kind `belongs_to_organization`); its Go struct is `OrganizationRecord` because `smeldr.Organization` is the schema.org constant. The default Domain is `General`. A module's type name can be set apart from its struct with `smeldr.TypeName(...)`.
- **Gotcha (core v1.168.0):** approval is `ratify` (Decisions) and `approve-change` (changes against Decisions in force); `approve` is retired. Roles: `approver` (was decision-steward), `domain-approver` (was decision-domain-admin, one Domain through its Areas), `area-approver` (approve-change in one Area). An agent or job token never ratifies (refused at grant and at transition), and holds approve-change only on an `area-approver` grant from a human with approve-change over that Area. `delegate_item` of `item-approver` names `ratify` or `approve-change`.
- **Gotcha (core v1.168.0):** a Decision is ratified only with a place. `transition_item` to `ratified` (from `proposed` or `pending-re-evaluation`) is refused with a conflict until the Decision has a live `belongs_to_organization`, `belongs_to_domain` or `belongs_to_area` edge (`assert_relation`), and a Decision holds exactly one place: withdraw the old edge before giving another. Unplaced: `search` with `place: "none"`.
- `CreateProvenanceTable` also indexes `(subject_type, subject_id)`; add that index by hand if you created the table from your own DDL.

## MCP tool catalog

Tools are named from the type in lower_snake_case.
`BlogPost` → `blog_post`, `MCPDoc` → `mcp_doc`.

| Tool | Role | Description |
|------|------|-------------|
| `create_{type}` | Author+ | Create Draft |
| `update_{type}` | Author+ | Partial field update. Name the item with `slug` (an ID, slug or human ID); `id` is accepted as its alias (mcp v1.55.2), and `id` and `slug` with different values are refused with -32602 on every identifier tool. Since mcp v1.55.1 / core v1.139.1 (A457) the schema offers no `status`, a `status` is refused with -32602 naming `transition_item`, and a differing slug or ID is refused (422 over HTTP PATCH); an equal value is accepted. Deploy mcp >= v1.55.1 with or before core v1.139.1. |
| `publish_{type}` | Author+ | Draft → Published |
| `schedule_{type}` | Author+ | Draft → Scheduled (requires scheduled_at RFC 3339) |
| `archive_{type}` | Author+ | Any → Archived |
| `delete_{type}` | Editor+ | Permanent delete |
| `list_{type}s` | Editor+ | All items, optional status filter |
| `get_{type}` | Editor+ | Single item at any status |
| `create_upload_token` | Author+ | forge-media: generate upload token |
| `create_preview_url` | Editor+ | Draft preview URL (prefix + slug) |
| `create_token` | Admin | Mint bearer token. Optional `actor_class` (`agent`, `job` or `human`, mcp v1.48.0, A418): provenance then records that `actor_kind` for everything done with the token; it never changes permissions, a token may be `human` only if every use of it is the direct result of one authenticated request by that person (D105), and an issued token cannot be classified afterwards (issue a new one and revoke the old). **An untagged actor records `unclassified` since core v1.121.0** (before, `human`; older rows keep `human` and mean unclassified, not a verified person). `list_tokens` returns `ActorClass` per token. cli: `token create ... --class agent|job|human` (v0.19.0). Call `smeldr.EnsureTokenActorClassColumn` at boot: a classified mint on a table without it is refused. Optional `reason` (string, at most 1000 characters, mcp v1.53.0 / core v1.135.0, A444): stored on the mint's provenance record (every mint now writes one, subject the token fingerprint). `expires_in_days` is capped at 36500 (larger values are -32602; before mcp v1.53.0 they overflowed into an already-expired token). Never put a token value or other secret in a reason. |
| `list_tokens` / `revoke_token` | Admin | Token management. `revoke_token` takes an optional `reason` (mcp v1.53.0); `list_tokens` returns `Reason` and `RevokeReason` per token (rows from before core v1.135.0 have none). cli: `token create ... --reason <text>`, `token revoke <id> --reason <text>` (v0.23.0). |
| `grant_role` / `revoke_grant` / `list_grants` | Admin | Governance role grants. `grant_role` and `revoke_grant` take an optional `reason` (mcp v1.53.0, A444), returned as `Reason` by `list_grants`. `grant_role` takes an optional `expires_in_days` (positive, at most 36500, fractions allowed, mcp v1.53.0, A447) for a time-boxed grant that stops authorizing on its own; the response carries `expires_at`, `list_grants` shows `ExpiresAt`. cli: `grant <token-id> <role> [--scope ...] [--anchor <id>] [--reason <text>] [--expires-in-days N]`, `grant revoke <grant-id> [--reason <text>]`, `grant list [<token-id>]` (v0.23.0, A448). |
| `create_webhook` / `list_webhooks` / `delete_webhook` | Admin | Webhook endpoints. Event suffixes: created, updated (a content edit only since core v1.137.0), published, unpublished, scheduled, archived, deleted, transitioned. Since core v1.137.0 (D107, A452) every status change on every path gives one `<type>.transitioned` (from_state, to_state, reason, actor) plus the status events once each, and one provenance record; a status-only PUT no longer sends `<type>.updated`, so key status reactions on `transitioned` or the status event. |
| `list_webhook_deliveries` / `retry_webhook` | Admin | Delivery introspection and retry |
| `create_redirect` / `list_redirects` / `delete_redirect` | Editor+ | Redirect rule management (requires `app.Redirects(db)`) |
| `set_page_meta` / `get_page_meta` / `delete_page_meta` / `list_page_meta` | Admin | Per-path SEO overrides (requires `mcp.WithPageMeta(db)`) |
| `assert_relation` / `propose_relation` / `observe_relation` / `get_relations` | Author+ | Relation graph: assert/propose/observe edges (edge_class=asserted/inferred/observed), query by source/target/both. Response includes `created_by` (the human/agent actor's ID, when set — v1.42.1) alongside the existing `created_by_job` (the system/job that wrote the edge, when applicable). Gate: `app.Relations(store)` called. Since v1.117.0 reachability, the cascade, goal context and the context packet read live edges only (GetLiveBySource/GetLiveByTarget); get_relations still returns ended edges. Since core v1.128.0 / mcp v1.51.0 (A434, relation history) one row is one life of a relation: re-asserting a live relation updates it, re-asserting an ended one starts a new row; get_relations returns every row in creation order and each ended row carries `ended: {cause, at, reason, actor_kind, actor_id}`, cause one of withdrawn, swept, recomputed, amendment-rejected, purged, or not-recorded (ended before causes were recorded). |
| `withdraw_relation` | Author | End a live relation on purpose: params `id` (from get_relations), `reason` (required). The row stays as history, ended, with you and the reason on record (cause withdrawn); an already ended relation is refused. Gated on the `archive` operation. There is no delete tool for relations. mcp v1.51.0 (A434); cli: `relation withdraw <id> --reason <text>` (v0.22.0). |
| `list_relations` | Author | Every relation on the instance, paged, for a whole-graph snapshot (core v1.142.0, mcp v1.58.0, A464). Params `kind`, `source_type`, `target_type`, `include_ended`, `as_of` (RFC3339), `limit` (default 50, max 500), `offset`. Returns `{edges, total, count, limit, offset, as_of}` in the get_relations edge shape, ordered created_at then id. Omit `as_of` on page 1, pass the returned one back on every next page so pages describe one moment; then follow the relations topic from as_of (add on relation.asserted, drop on relation.ended). `source_id`/`target_id` are the items' raw ID, as the list tools return it (not a context packet id). Live by default (invalid_at absent or after as_of). cli: `relation list [--all]`. |
| `search` | Author | Find records across every type you may read (core v1.167.0, mcp v1.65.0, A537/A538). Params `query` (2 to 200 characters, plain text, case-insensitive; optional when a filter is set), `types` (array), `states` (array), `place` (`none` = no Domain or Area, the import inbox; or a Domain/Area by address, slug or ID), `limit` (default 50, max 500), `offset`. Returns `{hits, count, total}`, newest first; each hit has type, id, slug, address, title, state, field, snippet, updated_at, awaits_action, standing (types with standing), state_since (Task, Goal). Hidden records never appear and never change `total`. Searchable: compiled fields tagged `smeldr_search:"text"`; runtime fields with `search: true`, else title/description/body/summary roles and markdown fields. No semantic search. cli: `search`. |
| `decisions_for_paths` | Author | Which Decisions apply to these files (core v1.168.0, mcp v1.66.0, A548/A551). Params `repository` (owner/name), `paths` (at most 3000), `include_organization` (default true). Returns `{repository, areas, decisions, uncovered}`: Areas whose coverage rules match (address, name, domain, approval_mode, paths, rules), Decisions in force (standing holds) placed in those Areas, their Domains and the Organization (`place`, `place_address`, `areas`), uncovered paths. Read rules apply. cli: `decisions-for-paths`. |
| `check_coverage` | Editor | Count each coverage rule's matches in a repository's full file list (at most 100000 paths); stores `match_count`/`checked_at`, opens `coverage-matches-nothing` for a rule that matches nothing, resolves it when it matches again. Returns `{repository, paths, rules, matching_nothing}`. cli: `coverage check`. |
| `preview_impact` | Editor+ | Return dependents of a target item without firing signals. Gate: `app.Relations(store)` called. |
| `upsert_relation_kind` | Admin | Register or update a relation kind. Gate: `app.Relations(store)` called. |
| `list_relation_kinds` | Author+ | List all registered relation kinds. Includes `reverse_label` when the kind has one set (01a0c43f/v1.40.0). Gate: `app.Relations(store)` called. |
| `define_state_flow` | Admin | Register or update a state flow for a dynamic content type. Calls `App.RegisterFlow`; idempotent. `type_name` required. States carry `locked`, `standing` (`holds` or omitted) and `awaits_action` (true = an item there waits for someone to act, "open"; omitted = closed; mcp v1.57.0, D109); the flow carries `active_state` and `conflict_policy`. Anything left out is reset, so send the full definition. A Go-defined `type_name` is refused (its flow is set in code). Returns `{name, type_name, state_count, transition_count}`. Gate: `App.Config().DB != nil`. |
| `redefine_content_type` | Admin (`define-type`) | Change a runtime-defined type's schema at once, without a restart (core v1.140.0, mcp v1.56.0, A459). Params `type_name`, `fields` (the full list), optional `label`, `reason`, `address_prefix` (mcp v1.60.0: empty keeps the current one; define_content_type takes it too, empty proposes one; both return `address_prefix`, while `prefix` stays the tool prefix). Allowed: the label, a field's role, format, description and relation, required to optional, new optional fields. Refused with -32602 naming the field, nothing saved: removing a field, changing its type, making it required, a new required field, any `url_prefix` change. Later writes use the new schema; existing items are not revalidated; another process on the same database sees it after a restart. Writes a provenance record (ContentType, update) and sends `content_type.redefined` on the `types` topic. cli: `content-type redefine` (v0.24.0). |
| Runtime-defined resources | Author | Since mcp v1.55.0 (A455) a runtime-defined type with a URL prefix is an MCP resource: `resources/list` (Published items), `resources/templates/list`, `resources/read` of `smeldr://{prefix}/{slug}`, and subscribers are notified on its status changes and, with core v1.139.0, content edits. A type without a prefix (e.g. `task_plan`) is not a resource. |
| `transition_item` | Editor | Move a dynamic content item to a new state; validates against registered flow (ErrConflict → -32001, ErrNotFound → -32000 as of v1.40.0, previously also -32001). Optional `reason` param (v1.31.0+) — required if the target transition has `RequiredReason` set, else returns -32602. Gate: `App.Config().DB != nil`. Since core v1.137.0 / mcp v1.54.0 (D107) a move reaches `App.OnSignal` subscribers (Audit, agent jobs) as the status events, and resource subscribers of a compiled type are notified (`smeldr.AfterTransition`, listener-only). |
| `get_valid_transitions` | Author | List legal target states for the item's current state; falls back to default flow. Since mcp v1.57.0 it returns `awaits_action` for the current state and `to_awaits_action` per option (D109). Gate: `App.Config().DB != nil`. |
| `list_items_by_state` | Author | List items of a dynamic content type in a given state. Gate: `App.Config().DB != nil`. |
| `get_item_standing` | Editor | Read one item's standing (D100): holds, ceased or none; no `standing` key when the type has none. Params: `type_name`, `slug`. A failed read is an error. Since mcp v1.46.0 the get/list tools, `get_content`, `list_content`, `list_items_by_state` and `transition_item` also carry a separate `standing` key (absent = no standing for the type or a failed lookup). Since mcp v1.57.0 they also carry `awaits_action` (D109: true = open, waits for someone to act; false = closed; absent = unknown, never closed). Gate: `App.Config().DB != nil`. |
| `get_item_provenance` | Editor | Read one item's history, newest first, paged (limit default 50, max 500, offset): time (UTC, second resolution), verb (create, update, transition, standing-began, standing-ended), from_state, to_state, gated, and the actor. Default view `members` (D101): actor_kind (job, agent, human or unclassified), actor_id, surface and reason on every entry; view `gated` only on a gated transition (a caller can only narrow). Rows from before core v1.121.0 say human for an untagged actor and mean unclassified. An error says when provenance is not enabled. Relation events are not included. mcp v1.49.0 (A428); cli: `history <type> <slug> [--limit] [--offset] [--view]` (v0.20.0). Since mcp v1.53.0 (A446) `type_name` `Token` (id = the fingerprint from `list_tokens`) and `RoleGrant` (id = the grant id) return the mint, grant and revoke entries with actor and reason, also after a grant's row is deleted; they need the same access as `list_tokens`/`list_grants` and take precedence over a content type of the same name. |
| `get_reachability` | Author | Walk the live relation graph outward from one item, up to 10 hops, bounded. Params: type_name, id, optional kind, direction (incoming, outgoing or both, default both; not get_relations source/target), depth (1 to 10), max_items (default 500, max 2000), limit (default 100, max 500), offset. Returns items (depth, type, id, edge_class, confidence), ring_sizes, total, count and cut {depth, dropped} when the cap stopped the walk: the ring at the cut is partial even when empty and deeper rings are absent, never reported as empty. Live edges only; graph structure, no item content. mcp v1.50.0 (A430); cli: reachability <type> <id> [flags] (v0.21.0). |
| `amends` (on create_amendment, update_amendment) | Editor | Optional Decision number the Amendment changes (D104; 104 is read too). Must name exactly one Decision or the write is refused; write-once (set at creation or later only while empty, a different value is refused, an empty one never clears it). Core asserts the amends edge to that Decision with your actor; read it with get_relations kind amends. Existing Amendments have none until an administrator maps them. Since core v1.128.0 a rejected Amendment's edge ends (cause amendment-rejected, the rejecting caller as actor). core v1.125.0 (A431). |
| `state_since`, `state_reason` (keys) | Author | Since mcp v1.47.0 (A415) every Task and Goal item returned by the typed `get_<type>`/`list_<type>` tools and `list_items_by_state` also carries `state_since` (RFC3339 UTC, second resolution) and, when the transition carried one, `state_reason`: when the item entered the state it is in now and why, from provenance (core `App.ItemsStateSince`, v1.120.0). An absent `state_since` means unknown (provenance off, the item moved before it was enabled, or a failed lookup), never "has not moved". The actor of the transition is deliberately not on this surface. |
| `create_signal` | Author | Insert a signal into smeldr_signals (status `open` for a catalog type, `pending` for a history type, since core v1.159.0; a condition type is refused). Args: `sender`, `signal_type` (required); `receiver` (optional — omit or leave empty for a broadcast to every subscriber), `task_ref`, `message`, `sequence`, `subject_type`, `subject_id` (optional — D86, the general way any signal pattern points at a real item; threaded straight into the INSERT, v1.40.1). Returns `{id, slug, status, address}`: since mcp v1.60.0 (A491) it inserts through core's `InsertSignal`, so the Signal gets its address (`S<n>`, D110) in the same transaction. Requires `CreateOrchestrationTables`. Gate: `App.Config().DB != nil`. smeldr.dev/mcp v1.40.1. |
| `list_signals` | Author | List signals filtered by `receiver`/`sender` (at least one required) and `state` (default "pending"). `limit` returns the most recent N (`created_at` descending); omitted or explicit 0 defaults to 50, capped at 500 (01a0d76a/v1.39.0 — previously omitted meant unbounded, oldest-first, a real disclosed behaviour change). Response includes both `count` (post-`limit`, i.e. `len(signals)`) and `total` (the real pre-`limit` match count, so a client can detect truncation). `offset` (default 0, negative is `-32602`) pages past 500, mcp v1.52.0; ties order by id (`created_at DESC, id DESC`), so pages neither repeat nor skip. Fail-open on missing table. Gate: `App.Config().DB != nil`. smeldr.dev/mcp v1.39.0. Both signal tools work on Postgres since mcp v1.46.1 (numbered placeholders); `created_at`/`updated_at` come back as RFC3339 with nanoseconds there and as the stored text on SQLite, so compare them as times. |
| `list_tasks` / `list_amendments` / `list_decisions` / `list_goals` / `list_runs` | Editor/Admin | Generic admin-list family (`mcpAdminReadToolDefs`) for the six orchestration types. `status` filters by lifecycle status. `limit`/`offset` (01a0d76a/v1.39.0) page the result — omitted or explicit 0 defaults to 50, capped at 500; previously unbounded (confirmed live: `list_tasks` returned 338 items, ~710KB). Response gains `"total"` (the real unfiltered count) alongside `"items"`. |
| `get_goal_context` | Author | Retrieve a goal and all linked items (Decisions, Tasks, Goals) via the relation graph. Args: `goal_id` (required): the Goal's address (`G12`), slug or ID; until the D110 cut also an old goal number (mcp v1.60.0). Returns `{goal, linked_decisions, linked_tasks, linked_goals}`. -32000 on not found (was -32001 before v1.40.0). Gate: `App.Config().DB != nil`. smeldr.dev/mcp v1.27.0. |
| `migrate_addresses` | Admin (`administer`) | Give every record without an address its address (D110), in one transaction: core's `MigrateAddresses` over MCP, for an instance reached only by URL and token (a Cloud org). Arg `apply` (boolean): omitted or false is a dry run (same code, rolled back, nothing written). Returns the report: per type prefix, counts before and after, every renumbered record (old, new), rewritten references, references left alone, `duration_ms` (writer held). Keep the report with the deploy record; the instance stores none. A second apply changes nothing. Applying is a governance act: dry run first, back up, Peter's yes. Policy row seeded by core v1.145.0 (A490). smeldr.dev/mcp v1.61.0 (A491). cli: `migrate-addresses [--apply] [--report <path>]` (v0.26.0, A492): report JSON on stdout, summary on stderr. |
| `list_type_tools` | Author | Discovery meta-tool. Takes `type_name` (snake_case, e.g. `"essay"`) and returns all MCP tool names registered for that compiled content type. Use when you find one verb for a type and need to enumerate its siblings. Always present in tools/list. smeldr.dev/mcp v1.29.0. |
| `get_sweep_run` | Author | Reads the most recent structural sweep run recorded for a detector, via `smeldr.SweepRunStore.Last`. Args: `detector` (required, e.g. `"structural"`). Returns `{detector, wired: false}` when no run has been recorded, or `{detector, wired: true, ran_at, walked, flagged}` when one exists. Gate: `App.Config().DB != nil`. smeldr.dev/mcp v1.33.0, requires smeldr.dev/core v1.78.2+. |
| `list_findings` | — | **Not actually implemented in `smeldr.dev/mcp` despite earlier documentation claiming v1.37.0+ ships it.** `smeldr.FindingStore`/`smeldr.Finding` exist in core (D51) and a `smeldr_tool_policies` row is seeded for this tool name, but no MCP tool definition, dispatch, or test for `list_findings` exists anywhere in `smeldr.dev/mcp`'s own source (confirmed by direct repo search, 2026-09-29) — traced to a 2026-09-20 WIP commit that started this tool alongside `get_check_status`, of which only the latter was ever finished. Tracked as its own fix/implement Task; do not rely on this tool existing until that lands. |
| `delegate_item` | Author+ | Delegate a role, scoped to one item, from the caller's own authority to another token — time-boxed (default 14 days, capped 90), not standing. Args: `token_id`, `role`, `operation`, `type`, `id`, optional `expires_in_days`. The caller must itself be authorized for every operation the named role carries against that item, and the named role must itself be `ScopeStatic` (e.g. the built-in `item-approver`/`item-reviewer`) — a global- or dynamic-scope role is refused (-32602), since its grants would authorize the recipient beyond the one named item. Response reports the actually-stored grant (read back via `GetGrant`), not the request — a repeat delegation of the same item resolves to the same grant row. Since core v1.151.0 `item-approver` holds `approve`+`read` and `item-reviewer` `review`+`read` (A501), so the delegator also needs `read` on the item, and the delegate reads its item; since smeldr.dev/mcp v1.61.1 the delegate can `transition_item` through the transition whose `RequiredOperation` it holds. Gate: `app.Governance(store)` called. smeldr.dev/mcp v1.42.0, requires smeldr.dev/core v1.101.0+ (`RoleStore.GetGrant`, `ListRoles`, `item-approver`/`item-reviewer`). |
| reader roles `scoped-reader`, `domain-reader` | Admin (via `grant_role`) | Built-in roles with ops exactly `["read"]` (core v1.146.0, A494; registered when `ENABLE_GOVERNANCE` is set). `scoped-reader` is static: `scope_static` like `["Decision:D12", "Task:*"]`, where the item may be named by address, slug or ID and is stored as its ID; a pattern naming no record is refused. `domain-reader` is dynamic over `belongs_to_domain` (incoming): `scope_anchor_id` = a Domain ID reads the items in that Domain, not the Domain itself. Since core v1.147.0 (A495) the item surfaces filter by them: get, list, writes, transitions and `/_content` show a scoped reader only what it may read, anything else answers exactly like a missing record, totals count readable items, and record types are never public. Since core v1.148.0 (A496) the graph surfaces do too: packet, goal context, relations, reachability, provenance and audit leave out what the reader may not read, and a relation shows only when both ends are readable. Since mcp v1.61.0 (A500) every MCP tool applies them: a read tool that names a type works for a scoped reader and is filtered, hidden and missing both answer -32000 `not found`, and `list_relations` needs a type filter for a scoped reader. |
| `withdraw_delegation` | Author+ | Withdraw a delegation before it expires. Args: `grant_id`. Only the member who created the delegation may withdraw it (checked via `GetGrant`'s `Grantor`), and only a time-boxed grant qualifies — a standing grant (no `ExpiresAt`) must be revoked by an Admin via `revoke_grant` instead. Gate: `app.Governance(store)` called. smeldr.dev/mcp v1.42.0. |
| `list_roles` | Author+ | List every role defined on the instance: name, operations, scope shape. Read-only, no parameters. Gate: `app.Governance(store)` called. smeldr.dev/mcp v1.42.0, requires smeldr.dev/core v1.101.0+ (`RoleStore.ListRoles`). |
| `lookup_token_names` | Author | Batch-resolve JWT `User.ID` actor values (the kind seen in `last_actor`, `RoleGrant.Grantor`, or a relation edge's `created_by`) back to the human-readable token `Name` each was minted for. Args: `user_ids` (array of strings, up to 500 per call). Returns `{names: {<user_id>: <token_name>}}` — an ID with no matching token is simply absent from the result, never guessed. Reveals only the token `Name`, not role/expiry/revoked status (those stay behind the Admin-only `list_tokens`). Gate: `TokenStore` configured. smeldr.dev/mcp v1.43.0, requires smeldr.dev/core v1.104.0+ (`TokenStore.NamesForUserIDs`) and v1.104.1+ for the seeded tool policy (governance-enabled instances). |

**Signal kinds (smeldr.dev/core v1.159.0, D115).**
- A Signal is either a **condition** or a **request**:
  - Conditions are `ratification-required`, `authorization-required` and `conflict-detected`. Core opens and resolves them itself, from `open` to `resolved`. Never create, answer or withdraw one; `create_signal` refuses a condition type.
  - Requests are `review-requested` and `question`. They go from `open` to `answered` (`answer_signal`, `App.AnswerSignal`, or `transition_item` with the answer as `reason`) or to `withdrawn` (`withdraw_signal`, `App.WithdrawSignal`).
- Every other type is history (plan-ready, notice, implementation-question and so on):
  - It is accepted with a `deprecated_signal_type` warning until the instance's `Config.SignalEventCutover`.
  - It starts `pending` and still moves `pending` → `read` → `acknowledged`.
  - Ask with `question`, not `implementation-question`.
- A message longer than 500 characters is a `message_too_long` warning, and is refused after `Config.SignalMessageCutover`.
- Nothing expires: `App.ExpireSignals` is a no-op. Use `App.OpenSignalsOlderThan` to find old open Signals.
- Existing rows move with `smeldr-server migrate-signal-kinds` (dry run unless `--apply`).
- **mcp v1.64.0 (A524):**
  - `answer_signal` (`signal`, `answer`, optional `answer_ref` as `Type:ident`) and `withdraw_signal` (`signal`, `reason`) close an open request. Sender and receiver are routing names: any caller who may and can read the request may withdraw it.
  - `list_signals` with no `state` lists what awaits action, `open` and `pending` together; pass `state` for one state.
  - `create_signal` returns the real `status` and a `warnings` array.
  - Subscriptions (D116): `list_subscriptions`, `get_subscription`, `create_subscription` (`name`, `filter`, `include_own`, `paused`), `update_subscription` (`paused` true/false pauses and resumes), `delete_subscription` (not the defaults). Listed when the instance keeps subscriptions.
  - A resource-subscription session belongs to the token that first subscribes it, and is notified only while that token is not revoked and may read the item.
  - cli v0.27.0 (A525) has the same as `subscription ...` and `signal answer|withdraw`, plus `watch --subscription <address>`. Gotcha: `watch` replaces the PowerShell watch scripts only at the protocol switch (`architect-protocol-switch-to-subscriptions`), after deploy 2. Until then keep `scripts/watch-events.ps1`.

Block system (T32, enabled with `mcp.WithBlocks()`; blocks addressed by ID, not slug):

| Tool | Role | Description |
|------|------|-------------|
| `create_node` | Author+ | Create a Draft block. Args `type_name`, `fields` (JSON object) |
| `update_node` | Author+ | Merge `fields` onto a block by `id` (absent keys preserved) |
| `get_node` / `list_nodes` | Author+ | Read block(s); `list_nodes` filters by `type_name` / `status` |
| `publish_node` / `archive_node` | Author+ | Lifecycle by `id` (publish idempotent) |
| `add_section` / `reorder_sections` / `remove_section` | Editor+ | Compose page sections |
| `add_item` / `reorder_items` / `remove_item` | Editor+ | Compose collection items |

`add_section` / `add_item` take only `parent_id` + `child_id` (types derived).
Reorder tools take `parent_id` + `ordered_child_ids`.

**Block rendering (developer API):** `app.ServeBlocks("templates/blocks")` returns a
`*BlockRenderer`; `Render(ctx, "page", pageID)` assembles a page's Published blocks
into HTML via one `templates/blocks/<type_name>.html` per type (batched, cycle-safe,
degrades gracefully). **Block `Fields` keys are PascalCase** (`{"Title":...,"Body":...}`,
matching the block-system type tables) — templates access `.Title`/`.Body`, so
snake_case keys render blank. Use PascalCase when creating blocks via `create_node`.
**Reference fields:** a `{Name}ID` field resolves to a `.{Name}` sub-object — `ImageID`
→ `.Image` (`.Image.MediaURL`/`.AltText`/`.Caption`) on content_block/contact_card/hero;
templates guard with `{{ with .Image }}…{{ .MediaURL }}…{{ end }}`. Published-only.

---

## forge-oauth — OAuth 2.1 for remote MCP (v0.1.0+)

ChatGPT Plus and Claude.ai require OAuth 2.1 to connect to remote MCP servers.
`forge-oauth` is a standalone MIT library (`smeldr.dev/oauth`).

```go
import oauth "smeldr.dev/oauth"

store, _ := oauth.NewSQLiteStore("./oauth.db")
oauthSrv := oauth.New(oauth.Config{
    Issuer:   "https://cms.example.com",
    Resource: "https://cms.example.com/mcp", // must match mcp's own resource identifier
    VerifyBearer: func(token string) bool {
        _, ok := smeldr.VerifyTokenString(token, app.Secret(), app.TokenStore())
        return ok
    },
}, store)
mcpSrv := mcp.New(app, mcp.WithOAuth(oauthSrv))
```

- `smeldr.VerifyTokenString(token, secret, store)` — validates a raw bearer token without `*http.Request` (v1.25.0+)
- `oauth.Config.Resource` (oauth v0.4.0+) — required; RFC 8707 resource indicator this
  authorization server issues audience-bound tokens for. `oauth.New` panics if empty.
- `mcp.WithOAuth(*oauth.Server)` — enables OAuth; all HTTP endpoints require Bearer (v1.11.0+)
- `mcp.WithForgeFallback()` — accepts forge bearer tokens as fallback when OAuth enabled; use alongside `WithOAuth` to keep Claude Desktop/smeldr-cli working (v1.11.1+). `ErrTokenNotFound` → try forge bearer · `ErrTokenExpired` → always 401
- `GET /.well-known/oauth-protected-resource` — RFC 9728; triggers OAuth flow in AI clients on 401
- Scope mapping: `mcp` → Author role · `mcp:admin` → Admin role
- `offline_access` scope → refresh token issued (required for ChatGPT)
- CIMD: stateless client validation — no client registration database

---

## forge-media — upload token flow

**Never use `create_file` (base64) for real images** — 85 KB WebP ≈ 113 KB
base64 — too many tokens. Use the upload token flow instead.

```
Step 1: call create_upload_token MCP tool
        → returns { token, upload_url, expires_in }

Step 2: POST file directly to upload_url
        Authorization: UploadToken <token>
        Content-Type: multipart/form-data
        Field name: "file"

        curl -X POST https://example.com/media \
          -H "Authorization: UploadToken <token>" \
          -F "file=@hero.webp"

        → 201 { "url": "/media/abc123-hero.webp", ... }

Step 3: update content with returned URL
        update_story slug="my-story" image="/media/abc123-hero.webp"
```

Token expires in 15 min (default). MIME whitelist: jpeg, png, webp, gif, avif.
Filename gets a hex prefix — prevents overwrite of existing files.

---

## forge-social (separate module)

`smeldr.dev/social` — social post scheduling and agent routing.

```go
socialSrv := social.New(db, social.Config{
    Secret: cfg.Secret,
    // Mastodon/LinkedIn env-var config still accepted but deprecated.
    // Preferred: use create_platform_config MCP tool (Admin) to store
    // credentials in the DB; no env vars required.
})
socialSrv.Register(app)
defer socialSrv.Stop()

mcpSrv := mcp.New(app,
    mcp.WithModule(socialSrv.PostModule()),
    mcp.WithModule(socialSrv.CredentialModule()),
    mcp.WithModule(socialSrv.ConfigModule()),     // create_platform_config
    mcp.WithModule(socialSrv.ScheduleModule()),   // slot-queue
)
```

**Platforms:** `mastodon` | `linkedin` | `x` (Twitter, v0.5.0+)

**Two scheduling models:**
- Model 1: explicit `scheduled_at` timestamp per post
- Model 2: slot-queue — `PublicationSchedule` defines recurring weekly slots; posts with `status: queued` (no `scheduled_at`) are published FIFO when a slot fires

**Catch-up:** if the server was offline when a slot fired, one post per missed slot is published on the next tick, capped at `len(slots)` per tick.

**Schedule status:** `active` (slots fire) or `paused` (queue preserved, slots skipped).

**Slot format (MCP):** `slots` is a JSON array string — use `"timezone"`, not `"tz"`:
```json
[{"weekday": 1, "time": "09:00", "timezone": "Europe/Copenhagen"},
 {"weekday": 4, "time": "09:00", "timezone": "Europe/Copenhagen"}]
```
weekday: 0=Sunday, 1=Monday … 6=Saturday. Empty timezone silently defaults to UTC.

**Platform config (v0.5.0+):** call `create_platform_config` (Admin) with `platform`, `client_id`, `client_secret`, `redirect_url`, and (for Mastodon) `instance_url`. Optional: `scope` (space-separated OAuth 2.0 scopes; X default: `"tweet.read users.read tweet.write offline.access media.write"`; Mastodon default: `"write:statuses write:media"`). Credentials are stored AES-256-GCM encrypted in the DB. X requires a registered app in the Twitter developer portal with OAuth 2.0 enabled.

**X OAuth 2.0 + PKCE:** call `create_social_credential` with `platform=x` → returns a `redirect_url` containing the PKCE challenge. The code verifier is stored server-side — agents never see it. Operator completes the flow in a browser; callback saves the token automatically.

MCP tools: `create_platform_config`, `create_scheduled_post`, `list_scheduled_posts`, `publish_scheduled_post`,
`archive_scheduled_post`, `delete_scheduled_post`, `create_social_credential`,
`list_social_credentials`, `get_social_credential`, `delete_social_credential`,
`create_publication_schedule`, `get_publication_schedule`, `update_publication_schedule`,
`list_publication_schedules`, `delete_publication_schedule`

---

## smeldr-cli key commands

```bash
# Content
smeldr-cli posts create --from post.md
smeldr-cli posts update my-slug --from updated.md
smeldr-cli posts publish my-slug
smeldr-cli posts archive my-slug
smeldr-cli posts list --status draft

# Preview (Admin)
smeldr-cli preview /posts my-draft-slug

# Media
smeldr-cli media upload hero.jpg --description "Hero image"
smeldr-cli media list --type image
smeldr-cli media delete <id>

# Tokens (Admin)
smeldr-cli token create ci-deploy author 30
smeldr-cli token create ci-deploy author 30 --reason "CI deploys for site"   # v0.23.0+, needs mcp v1.53.0+
smeldr-cli token list
smeldr-cli token revoke <id> --reason "<text>"                  # --reason optional, v0.23.0+
smeldr-cli history Token <fingerprint-id>                       # who minted/revoked it and why (mcp v1.53.0+)

# Grants (Admin, smeldr-cli v0.23.0+, needs mcp v1.53.0+ on the server)
smeldr-cli grant <token-id> <role> --reason "<text>" --expires-in-days 14   # time-boxed (at most 36500)
smeldr-cli grant revoke <grant-id> --reason "<text>"
smeldr-cli grant list [<token-id>]
smeldr-cli history RoleGrant <grant-id>

# Runtime-defined content types (smeldr-cli v0.24.0+; redefine needs core v1.140.0 and mcp v1.56.0 on the server)
smeldr-cli content-type define --type recipe --fields recipe-fields.json --url-prefix /recipes --label Recipe   # Admin
smeldr-cli content-type redefine --type recipe --fields recipe-fields.json --reason "<text>"                  # Admin; loosen and add only
smeldr-cli content-type get recipe                                                                             # Author

# Webhooks
smeldr-cli webhook create --url https://example.com/hook --events post.published
smeldr-cli webhook list
smeldr-cli webhook delete <endpoint-id>

# Redirect management (Editor role, v1.34.0+) — requires app.Redirects(db)
smeldr-cli redirect list                                      # aligned table
smeldr-cli redirect list --json                              # raw JSON
smeldr-cli redirect create --from /old --to /new             # 301
smeldr-cli redirect create --from /gone --code 410           # 410 Gone
smeldr-cli redirect create --from /posts --to /articles --prefix  # prefix rewrite
smeldr-cli redirect delete /old-path

# Navigation tree (Editor role, v0.13.0+) — requires app.Nav(...) with DB mode
smeldr-cli nav list                                          # aligned table (ID, LABEL, PATH, PARENT, HIDDEN, GHOST, SORT)
smeldr-cli nav list --json                                   # raw JSON
smeldr-cli nav create --label "Learn" --path /learn          # create top-level item
smeldr-cli nav create --label "Intro" --path /learn/intro --parent-id <id>  # nested
smeldr-cli nav update <id> --label "New Label" --sort-order 2
smeldr-cli nav delete <id>                                   # cascades to descendants

# Audit trail (Editor role, v1.22.0+)
smeldr-cli audit list
smeldr-cli audit list --type Post
smeldr-cli audit list --from 2026-01-01T00:00:00Z --to 2026-12-31T23:59:59Z
smeldr-cli audit list --actor <actor-id>

# Transition (role required by the target StateFlow's own gate, smeldr-cli v0.16.0+)
# — dynamic content (snake_case type_name) or a compiled type (e.g. Decision, Task, Signal)
smeldr-cli transition Decision <slug> --to ratified          # e.g. Decision ratification
smeldr-cli transition <type_name> <slug> --to <state> --reason "<text>"  # when the gate requires a reason

# Relation (smeldr-cli v0.22.0+, needs mcp v1.51.0+ on the server): end a relation on purpose, keeping its row
smeldr-cli relation withdraw <edge-id> --reason "<text>"       # Author
smeldr-cli relation list [--kind K] [--source-type T] [--target-type T] [--include-ended] [--as-of RFC3339] [--limit N] [--offset N] [--all] [--json]   # Author; --all pages the whole graph at one as_of (cli v0.25.0)
smeldr-cli subscription list|get|create|update|pause|resume|delete [args]   # Author; your event subscriptions (D116, cli v0.27.0, mcp v1.64.0)
smeldr-cli signal answer <address> --text <answer> [--ref Type:ident]       # Author; close an open request (D115)
smeldr-cli signal withdraw <address> --reason <text>                        # Author
smeldr-cli watch --subscription <address> [--after <id>]                    # Author; one JSON line per event; reconnects with ?after= after a drop or a clean close, dedupes by id; exit 1 on 400/401/403/404

# Standing (D100, smeldr-cli v0.18.0+, needs mcp v1.46.0+ on the server): holds, ceased, none, or "no standing for this type"
# (rarely also a failed flow lookup on the server). "<type> get/list" read REST and do not show standing.
smeldr-cli standing Decision <slug> [--json]                   # Editor

# Search (T885, smeldr-cli v0.28.0+, needs mcp v1.65.0+ on the server): every type you may read
smeldr-cli search "release plan" [--type Decision] [--state proposed] [--place none|<DOM/ARE>] [--limit n] [--offset n] [--json]   # Author

# Coverage (T886, smeldr-cli v0.29.0+, needs mcp v1.66.0+ on the server)
git diff --name-only main | smeldr-cli decisions-for-paths --repo owner/name --stdin [--no-organization] [--json]   # Author
git ls-files | smeldr-cli coverage check --repo owner/name --stdin [--json]   # Editor
# Block system (T32, smeldr-cli v0.10.0+) — Fields keys are case-sensitive PascalCase
smeldr-cli block node create --type hero --field Headline="Welcome"   # Author
smeldr-cli block node list --type hero --status published             # aligned table; --json for raw
smeldr-cli block node publish <id>
smeldr-cli block section add <page_id> <block_id>                     # Editor
smeldr-cli block section reorder <page_id> <id1,id2,id3>
smeldr-cli block item add <collection_id> <block_id>

# Social (smeldr-cli v0.8.0+)
smeldr-cli social credential create --platform mastodon|linkedin|x [--instance-url <url>]
smeldr-cli social credential list
smeldr-cli social credential get <id>
smeldr-cli social credential delete <id>
smeldr-cli social post create --platform mastodon|linkedin|x --credential <id> --body "..."
smeldr-cli social post queue --credential <id> --body "..."
smeldr-cli social post list --status queued
smeldr-cli social post publish <slug>
smeldr-cli social post archive <slug>
smeldr-cli social platform configure --platform mastodon|linkedin|x --client-id <id> --client-secret <secret> --redirect-url <url> [--instance-url <url>] [--success-url <url>]
smeldr-cli social schedule create --credential <id> --slot "monday 09:00 Europe/Copenhagen"
smeldr-cli social schedule show --credential <id>
smeldr-cli social schedule pause --credential <id>
smeldr-cli social schedule resume --credential <id>
smeldr-cli social schedule delete --credential <id>
```

Config: `SMELDR_URL`, `SMELDR_TOKEN`, `SMELDR_MCP_URL` (or `.smeldr-cli.env`; legacy `FORGE_*` env vars still read as fallbacks — T86/T87)

---

## Common gotchas

- **go.mod line 1** must be `module smeldr.dev/mcp` — not a github.com path
- **Verify go.mod deps before tagging** — `grep smeldr.dev go.mod`; run `go mod tidy`
- **Module proxy caches permanently** — bad tag requires a new patch tag, no fix
- **smeldr.Table()** — use when type name pluralises incorrectly (Story → storys)
- **OAuth Dynamic Client Registration** (mcp v1.59.1, oauth v0.6.0, A470/A471/A473): `mcpSrv.Register(app)` mounts each oauth route with its method and full path, `POST /oauth/register` included; it is served exactly when discovery advertises `registration_endpoint` (an `oauth.SQLiteStore` supports it). Never use mcp v1.59.0 (retracted): its `/oauth/` prefix panics at startup next to an App `GET /`. The endpoint bounds each request (64 KiB body, 10 redirect URIs of up to 2048 bytes, a 256-byte client_name) and accepts only https, loopback http or reverse-domain private-use redirect URIs. Rate limiting stays the App's job (`app.Use`).
- **References must name real, readable records** (core v1.161.0, mcp v1.63.1, A521/A522, D113). Task `implements` (a Decision or Goal), Amendment `task_ref` (a Task), a Signal's `subject_type`/`subject_id` and `task_ref`, `answer_ref`, and runtime-defined fields with `"references": "<Type>"` are resolved by address, slug or ID under your read scope; a missing or hidden one is refused (-32602) with how to name it. They are stored canonically (address; a Signal subject as its ID), write-once, and core writes the edge (`implements`, `derives_from`, `ships_as`, `about`, `refers_to`) with the record. A Signal `task_ref` like `A185` is refused: put a Task there and any other record in `subject_type`/`subject_id`. Old records: `smeldr-server migrate-references` (dry run first).
- **Markdown for untrusted text: `smeldr.RenderMarkdown(body)`, never the `markdown`/`smeldr_markdown` template funcs** (core v1.143.0, A465). The template funcs pass a line starting with `<` through as raw HTML, for trusted site authors. RenderMarkdown escapes everything, renders headings as one `<h3 class="md-h">` step, and is bounded against hostile input (whole text as escaped `<pre class="md-raw">` on a bound). `smeldr.StripMarkdown(body)` gives plain text for previews. `smeldr_excerpt` now returns escaped, markup-free text on one line (A466).
- **Windows MIME** — add `mime.AddExtensionType(".webp", "image/webp")` in main()
- **Docker volume** — forge_media volume at /app/media; COPY in Dockerfile seeds it on first run
- **Archived ≠ Draft** — preview tokens bypass Draft/Scheduled only, never Archived
- **Rule and AuthorityStub have addresses** (core v1.156.0, A510) — `U<n>` and `B<n>` (D110), accepted by get/update/transition next to slug and ID; rows from before the upgrade get theirs from `migrate-addresses`
- **A draft reserves no URL** (core v1.154.0, A507) — drafts of web content may share a slug; publish/schedule refuses a URL another item already published (409 naming a free one, e.g. `/posts/about-2`); an unpublished item may change its `slug` (over MCP: `new_slug` on `update_<type>`/`update_content`, mcp v1.62.0, A511); a slug naming several readable items is 409 "use its ID". Bind a preview to one item with `App.GeneratePreviewTokenForID`. A compiled table should use `slug TEXT NOT NULL` plus `EnsurePublishedSlugIndex`, not a column `UNIQUE`
- **Timezone database** — `time.LoadLocation` fails on servers without OS tzdata (Alpine, scratch). forge-social embeds `time/tzdata` since v0.4.1 — ensure you are on v0.4.1 or later
- **X body limit** — X posts are capped at 280 characters. Exceeding the limit returns a terminal `publishError` — the post will never be retried. Truncate before calling `create_scheduled_post` with `platform=x`
- **Go stdlib CVEs turn CI red on an unrelated commit** — the vuln DB updates independent of any push; `govulncheck` starts failing on whatever commit happens to run next, time-triggered not commit-triggered. Fix is a `toolchain goX.Y.Z` line in `go.mod` (not a `go` line bump — a stdlib patch changes nothing about what the language or a caller needs, only which binary compiles the code; see A259), applied to every repo on the affected Go version, not just the ones with a `govulncheck` CI step — a repo without the step has the same exposure, only less visibility
- **Assigning an Amendment number with parallel branches in flight** — read `main`'s index AND every unmerged sibling branch that carries an Amendment. The next free number is one past the highest claimed *anywhere*, not one past `main` — reading only `main` is what breaks with more than one Amendment-carrying branch open at once (first hit 2026-08-14, three concurrent branches)
- **A version number written into CHANGELOG.md or README.md before its tag exists is an intention, not a fact.** Until the tag is pushed and proxy-verified, mark the section as unreleased. A superseded intention gets annotated, never backfilled — the proxy keeps tags forever, and a release nobody used is worse than a gap that is explained (v1.65.1, T211/T250)
- **Local green does not mean CI green.** CI runs `-race` and `govulncheck` as separate steps. A pre-commit-ready check that omits them cannot predict the gate. Run `go test -race ./...` before signalling commit-ready, not just `-cover` — an async dispatch that passes without the detector will fail with it (T251)
- **X media** — `media_url` is ignored for platform `x` in v0.5.0. Attach images only for Mastodon and LinkedIn
- **Every `create_*`/`update_*` MCP tool now rejects an unrecognized parameter** (v1.40.0, 01a0da88-2) — a call with a key not in that tool's own declared `InputSchema.properties` gets `-32602 "unknown parameter: <key>"` instead of silently succeeding with the field left empty. Found live from a real incident: `create_signal` called with `body` instead of `message` succeeded four times with an empty field before this shipped. Double-check field names against the tool's own schema, not a similarly-named field on a different content type.
- **`tools/list` is now filtered by the caller's own role** (v1.40.0, 01a0ca79) — an Author-level caller no longer sees Admin-only tools like `create_token` in `tools/list`, even though they never could have called them. This is a curation UX feature, not a new authorization rule: `tools/call` enforces exactly the same thing it always did.
- **`errorFor`'s `ErrNotFound` moved to its own code, `-32000`, separate from `-32001`** (v1.40.0, 01a0c487) — previously `ErrNotFound`/`ErrForbidden`/`ErrConflict` all shared `-32001`. Any code matching on `-32001` specifically to detect "not found" needs to match `-32000` instead. `ErrForbidden`/`ErrConflict` are unaffected.
- **`mcp.WithSchemaTools(db)`** (v1.40.0) wires `get_content_type_schema`/`list_content_type_schemas` on their own, without the rest of `WithBlocks`'s tool surface — use this when a deployment only needs schema discovery. A server already using `WithBlocks` doesn't need it too.

---

## smeldr.config keys

```
base_url                      string
secret                        string  (panics if set here — use env instead)
dev                           bool    (disk-based static serving)
media_path                    string  (default ./media/)
media_max_size                int     (bytes, default 5242880)
preview_token_expiry          duration
media_upload_token_expiry     duration
```

---

## smeldr-agent (separate module)

`smeldr.dev/agent` v0.3.0 — minimal Go agent runtime with native MCP support.
MIT license. Three dependencies: `anthropic-sdk-go` + `modelcontextprotocol/go-sdk` + `gocron/v2`.

```go
cfg := agent.Config{
    MCPURL:        "http://localhost:8080/mcp",
    MCPToken:      "bearer-token",
    SystemPrompt:  "You are a helpful assistant.",
    Model:         "claude-sonnet-4-6", // default
    MaxTurns:      10,                  // default
    StreamableHTTP: false,              // false = SSE (smeldr-mcp), true = Streamable HTTP (GitHub MCP)
}
a := agent.New(cfg)
result, err := a.Run(ctx, "List all published posts and summarize the site.")
```

**Transport selection:**
- `StreamableHTTP: false` — SSE transport (`/mcp` + `/mcp/message`). Use for smeldr-mcp.
- `StreamableHTTP: true` — Streamable HTTP (MCP 2025-11-25 spec). Use for GitHub MCP
  (`https://api.githubcopilot.com/mcp/`) and other modern MCP servers.

**Built-in tools** (always available alongside MCP tools):

| Tool | Input | Behaviour |
|------|-------|-----------|
| `http_get` | `url` | GET request, returns body (32 KB cap) |
| `http_post` | `url`, `body`, `content_type` | POST body; `content_type` defaults to `text/plain` |

`http_post` examples: ntfy.sh notifications (`text/plain`), Discord webhooks (`application/json`).

**Scheduler (v0.2.0+):** `agent.NewScheduler([]agent.Job{...})` — cron-driven agent jobs.
Each `Job` has `Schedule` (5-field cron), `Timezone` (IANA), `Task` (prompt), `Config`.
Timezone validated at startup. Overlapping runs skipped. Missed jobs not caught up.
Add `import _ "time/tzdata"` to binaries on Alpine/scratch containers.

**Example binaries in repo:**
- `cmd/agent-smeldr` — `SMELDR_MCP_URL` + `SMELDR_TOKEN` (SSE)
- `cmd/agent-github` — `GITHUB_MCP_URL` + `GITHUB_TOKEN` + `GITHUB_REPO` (Streamable HTTP)
- `example/electricity-advisor/` — `ANTHROPIC_API_KEY` + `DISCORD_WEBHOOK_URL` (UC2: electricity prices → Discord)

### smeldr-agent/flow — Smeldr integration (v0.3.0)

`smeldr.dev/agent/flow` — AGPL sub-package. Wires `AgentJob` as a Smeldr
content type. Requires `smeldr.dev/core` as a dependency.

```go
import agentflow "smeldr.dev/agent/flow"

agentflow.CreateTable(db) // run once at startup
agentMod := agentflow.New(db, agentflow.Config{
    MCPURL:   "http://localhost:8080/mcp",
    MCPToken: os.Getenv("SMELDR_TOKEN"),
})
agentMod.Register(app) // registers MCP tools + signal bus
defer agentMod.Stop()
```

**AgentJob fields:**

| Field | Description |
|-------|-------------|
| `Name` | Human-readable identifier. Slug source. Required. |
| `Trigger` | Cron expression (`"45 13 * * *"`) or smeldr signal (`"after_publish"`). Required. |
| `ContentTypeFilter` | Restrict signal trigger to content type (e.g. `"Post"`). Empty = all. |
| `SystemPrompt` | System instruction for every run. Required. |
| `Model` | Anthropic model ID. Defaults to `"claude-sonnet-4-6"`. |
| `MaxTurns` | Max tool-use loops. Defaults to 10. |
| `WebhookURL` | If set, agent task includes instruction to POST output here via `http_post`. |

**Status lifecycle:** Draft (does not run) → Published (active) → Archived (stopped).

**Auto-generated MCP tools (Admin role):**
`create_agent_job`, `get_agent_job`, `list_agent_jobs`, `update_agent_job`,
`publish_agent_job`, `archive_agent_job`, `delete_agent_job`

**Signal triggers:** set `Trigger` to any smeldr signal string value:
`after_publish`, `after_create`, `after_update`, `after_unpublish`,
`after_archive`, `after_schedule`, `after_delete`.

**Guard:** AgentJob lifecycle events never trigger other jobs — prevents self-activation loops.

---

## Skill update process

The canonical source for this file is:
`C:\Users\peter\Documents\Code\Smeldr\common\agent\skills\smeldr.md`

When updating: edit here first, then sync to `core/skills/smeldr.md` via the
doc-gate Copy-Item step before any M-number commit.
Agents read this file directly — no copies to distribute.

---

## Haiku delegation — plan marking

Every core-implementer plan for a task that contains mechanical deliverables should include
a Haiku-scan step. For each deliverable in the plan, mark it `[Haiku]` if it is
template-eligible, or leave it unmarked (implicitly Sonnet).

Eligible categories are defined in `context/corepilot.md` under
"Haiku delegation — mekaniske leverancer".

This makes delegation decisions explicit and reviewable by the architect.

**Example plan entry:**

| Deliverable | Owner |
|-------------|-------|
| coverage_test.go — 41 test stubs from plan table | [Haiku] |
| codecov.yml — new file with exact content specified | [Haiku] |
| decisions/recent.md — A150 entry (fixed format) | [Haiku] |
| context/core-implementer.md — coverage gate addition | Sonnet |
| Plan writing | Sonnet |
