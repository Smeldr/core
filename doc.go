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
//	    log.Printf("published %s/%s", ev.ContentType, ev.Slug)
//	    return nil
//	})
//
// Available signal constants: [AfterCreate], [AfterUpdate], [AfterPublish],
// [AfterUnpublish], [AfterSchedule], [AfterArchive], [AfterDelete].
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
//   - Relation graph ([RelationStore], [RelationEdge], [RelationKindDef]) —
//     typed, directional edges between items (asserted, inferred, or
//     observed). Create a store with [NewRelationStore].
//   - Authority and rules ([Rule], [AuthorityStub]) — role/rank-scoped
//     authorization scaffolding, ordered with [SetRuleTypeOrder] and queried
//     with [RuleTypeRank].
//   - Checks ([CheckStore], [CheckRecord]) — recorded precondition
//     enforcement against a subject, run with [RunAuthorityCheck].
//   - Structural sweeps ([SweepRunStore], [SweepRunRecord]) — recorded runs
//     of a structural-deviation detector, created with [NewSweepRunStore].
//   - Findings ([Finding], [FindingStore]) — thin, detector-owned records of
//     a structural or governance condition, created with [NewFindingStore]
//     and wired into a sweep via [App.Findings]; written only by detectors,
//     never by a human-driven flow.
//   - Lineage ([LineageTrace], [LineageNode]) — traversal of the relation
//     graph for provenance and impact queries.
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
