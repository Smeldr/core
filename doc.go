// Package smeldr is a Go web framework for content-first applications: structured
// content types with lifecycle management (draft → scheduled → published → archived),
// AI indexing, RSS feeds, sitemaps, MCP tool support, and zero third-party runtime
// dependencies.
//
// # Minimal startup
//
// Embed [Node] in a content type, wire a repository and a module, then call [App.Run]:
//
//	type Post struct {
//	    smeldr.Node
//	    Title string `smeldr:"required,min=3" db:"title"`
//	    Body  string `smeldr:"required"       db:"body"`
//	}
//
//	repo := smeldr.NewMemoryRepo[*Post]()
//
//	m := smeldr.NewModule(&Post{},
//	    smeldr.At("/posts"),
//	    smeldr.Repo(repo),
//	    smeldr.Auth(
//	        smeldr.Read(smeldr.Guest),
//	        smeldr.Write(smeldr.Author),
//	    ),
//	)
//
//	app := smeldr.New(smeldr.MustConfig(smeldr.Config{
//	    BaseURL: "http://localhost:8080",
//	    Secret:  []byte("at-least-16-bytes-secret"),
//	}))
//	app.Content(m)
//	log.Fatal(app.Run(":8080"))
//
// # Key types
//
//   - [App] — the HTTP application. Create it with [New]; configure it with [MustConfig].
//     Register content modules with [App.Content].
//   - [Module] — a typed content module that owns a URL prefix, a repository, and a full
//     set of routes for CRUD, feeds, sitemap, and AI endpoints. Create with [NewModule].
//   - [Node] — the mandatory embedded base for every content type. Provides Slug, Status,
//     PublishedAt, ScheduledFor, and CreatedAt fields.
//   - [Config] — application configuration: BaseURL, Secret, DB, TokenStore, and optional
//     media and development settings. Validated by [MustConfig].
//   - [Repository] — the storage interface. Use [NewMemoryRepo] for in-process storage or
//     [NewSQLRepo] for SQLite/Postgres persistence.
//
// # Module options
//
// Pass functional options to [NewModule] to configure module behaviour:
//
//   - [At] — sets the URL prefix (required; e.g. smeldr.At("/posts")).
//   - [Auth] — configures role-based access. Nest [Read], [Write], and [Delete] calls
//     with role constants [Guest], [Author], [Editor], or [Admin].
//   - [Repo] — wires the persistence layer; required for any module that stores content.
//   - [On] — registers a typed module-level signal handler for lifecycle events.
//   - [Feed] — enables an Atom/RSS feed at the module's URL prefix.
//   - [Templates] — registers HTML templates for server-side rendering.
//   - [SitemapConfig] — controls whether this module contributes to /sitemap.xml.
//   - [AIIndex] — enables /llms.txt and /llms-full.txt AI content indexes.
//   - [MCP] — exposes the module via the Model Context Protocol.
//   - [Social] — connects the module to Smeldr's social posting schedule.
//
// # Authentication
//
// Smeldr uses token-based authentication. Two modes are available:
//
//   - Stateless HMAC tokens (default): create tokens with [SignToken] and verify
//     them with the [BearerHMAC] auth function. Tokens are self-contained and
//     cannot be revoked individually.
//   - Revocable tokens: wire a [*TokenStore] in [Config.TokenStore]. Tokens are
//     persisted in the database and can be listed and revoked at runtime via
//     [TokenStore.Create], [TokenStore.List], and [TokenStore.Revoke]. A bootstrap
//     admin token is generated automatically on first startup when the store is empty.
//
// Use the [Authenticate] middleware to enforce authentication on routes registered
// outside content modules.
//
// # Signals
//
// Smeldr fires a lifecycle [LifecycleEvent] at each content transition. Attach handlers to
// trigger side effects such as notifications, cache invalidation, or social posting.
//
// Module-level typed handlers receive the full typed content item:
//
//	smeldr.On[*Post](smeldr.AfterPublish, func(ctx smeldr.Context, p *Post) error {
//	    log.Printf("published: %s", p.Slug)
//	    return nil
//	})
//
// App-level bus handlers receive a [SignalEvent] and fire for all content types:
//
//	app.OnSignal(smeldr.AfterPublish, func(ctx context.Context, ev smeldr.SignalEvent) error {
//	    log.Printf("published %s/%s", ev.Type, ev.Slug)
//	    return nil
//	})
//
// Available signal constants: [AfterCreate], [AfterUpdate], [AfterPublish],
// [AfterUnpublish], [AfterSchedule], [AfterArchive], [AfterDelete].
//
// The synchronous [BeforeCreate], [BeforeUpdate] and [BeforeDelete] module
// hooks can refuse a write by returning an error. They run on HTTP and MCP
// writes alike, so a validation hook guards every surface. Likewise a module's
// After handlers see the same events for a status change on every path,
// including transition_item and the scheduler.
//
// A status change is recorded and announced once, the same way on every path
// (D107): one provenance record, one "<type>.transitioned" event on the event
// stream and to webhooks, the status events above once each on the App's bus,
// and one [AfterTransition] to [App.AddSignalListener] callbacks. On the App's
// bus [AfterUpdate] means a content edit only.
//
// Relations between items keep their history: one row is one life of a
// relation, an end is never a delete, and every end records its cause and
// actor ([RelationStore.Withdraw], [EdgeEnd]).
//
// # Orchestration and governance
//
// Beyond the base content-lifecycle framework above, Smeldr ships a set of
// compiled types and stores for running a project's own governance ledger —
// decisions, tasks, goals, relations between them, and enforcement around all
// of it:
//
//   - Orchestration types ([Decision], [Task], [Goal], [Signal], [Amendment],
//     [Run]) — a project's own ledger of decisions, work items, goals, and
//     inter-agent signals. Register with [RegisterOrchestrationTypes]; create
//     the backing tables with [CreateOrchestrationTables].
//   - Custom state flows ([StateFlow], [State], [Transition],
//     [TransitionTrigger]) — a data-driven state machine registered per
//     content type via [App.RegisterFlow], for lifecycles beyond the built-in
//     Draft → Published → Archived progression.
//   - Standing ([Standing], [State].Standing): a flow state may declare that an
//     item in it is in force ([StandingHolds]); each item then stores whether it
//     holds, has ceased, never held or was not recorded, written only by the code
//     that changes its state and recorded as "standing-began"/"standing-ended"
//     provenance events. Read it with [ItemStanding] and [CountStanding]; check it
//     with [App.CheckStandingDrift]; give pre-existing items a standing once with
//     [MigrateStanding].
//   - Item history ([App.ItemProvenance], [ProvenanceAudience]): one item's
//     provenance, newest first and paged, with the actor on every entry for the
//     organisation's members and only on gated transitions for a wider audience
//     (D101). [SubjectProvenance] is the older gated-only read.
//   - Relation graph ([RelationStore], [RelationEdge], [RelationKindDef]) —
//     typed, directional edges between items (asserted, inferred, or
//     observed). Create a store with [NewRelationStore]. Designate a dynamic
//     content type as reference/lookup data (never edited toward a terminal
//     "published" state, but never structurally invalid either) with
//     [RelationStore.RegisterReferenceType] — the default structural-sweep
//     target checker treats a registered reference type's row as alive
//     whenever it exists and isn't archived, rather than requiring
//     "published" the way editorial dynamic content does.
//   - Authority and rules ([Rule], [AuthorityStub]) — role/rank-scoped
//     authorization scaffolding, ordered with [SetRuleTypeOrder] and queried
//     with [RuleTypeRank].
//   - Checks ([CheckStore], [CheckRecord]) — recorded precondition
//     enforcement against a subject, run with [RunAuthorityCheck].
//   - Structural sweeps ([SweepRunStore], [SweepRunRecord]) — recorded runs
//     of a structural-deviation detector, created with [NewSweepRunStore].
//     [App.ExpireSignals] is one such detector: it moves aging, unanswered
//     Signals to an "expired" state on a schedule, exempting a
//     configurable set of standing-condition signal types via
//     [SignalExpiryConfig].
//   - Findings ([Finding], [FindingStore]) — thin, detector-owned records of
//     a structural or governance condition, created with [NewFindingStore]
//     and wired into a sweep via [App.Findings]; written only by detectors,
//     never by a human-driven flow.
//   - Lineage ([LineageTrace], [LineageNode]) — traversal of the relation
//     graph for provenance and impact queries.
//   - Severity ([Severity], [SeverityOf]) — a small ordinal blast-radius
//     score for an anchor item, the higher of its real relation-graph
//     fan-out and its own rule-type authority rank used as a floor.
//   - Governance roles and audit ([RoleStore], [RoleDefinition], [RoleGrant],
//     [GovernanceAuditStore], [GovernanceAuditRecord], [StewardshipInbox]) —
//     role grants and an audit trail over governance actions, created with
//     [NewRoleStore] and [NewGovernanceAuditStore].
//   - Webhooks ([WebhookStore], [WebhookEndpoint], [WebhookEventPayload]) —
//     outbound delivery on lifecycle events, created with [NewWebhookStore].
//   - Dynamic content types ([DynamicTypeRepo]) — a schema-driven repository
//     for content types registered at runtime (e.g. via MCP) rather than
//     compiled in, created with [NewDynamicTypeRepo].
package smeldr
