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
// actor ([RelationStore.Withdraw], [EdgeEnd]). References between records
// (a Task's Implements, an Amendment's TaskRef, a Signal's subject, a
// [SchemaField] with References) are checked on write and become edges the
// system writes in the record's own transaction (D113, [MigrateReferences]).
//
// # Markdown
//
// [RenderMarkdown] renders a markdown body as HTML that is safe for text from
// any author: everything is escaped, raw HTML included, and its parser is
// bounded against hostile input. [StripMarkdown] returns the same body as
// plain text for previews. The "markdown" template func is for trusted site
// authors and passes raw HTML lines through; never use it for untrusted text.
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
//     the backing tables with [CreateOrchestrationTables]. Core governance
//     ([Signal], [Decision], [Amendment]) has its own calls,
//     [CreateGovernanceTables] and [RegisterGovernanceTypes]; the
//     Orchestration add-on ([Task], [Goal], [Run]) has
//     [CreateOrchestrationAddOnTables] and [RegisterOrchestrationAddOn].
//   - Places (D142: [Organization], [Domain], [Area], [Set]): the organization's
//     structure, registered with core governance. [App.EnsureOrganization] creates
//     the one Organization and a default Domain; any record may belong to a Domain
//     or an Area, and place readers read through the hierarchy.
//     [App.MigratePlaces] moves an instance's runtime place types to them.
//     A Decision has exactly one place (Organization, a Domain or an Area) and is
//     ratified only once it has one.
//   - Approval (D142): the operations [OperationRatify] and
//     [OperationApproveChange], held by the roles [RoleApprover] (the whole
//     organization), [RoleDomainApprover] (one Domain) and [RoleAreaApprover]
//     (one Area, approve-change only). An agent or job token never ratifies,
//     and holds approve-change only on an Area grant from a human.
//     [App.GrantFounder] gives the organization's founder admin and approver.
//     [App.ApproversOf] tells anyone who reads a place who approves there
//     (D147), without administer.
//   - Coverage (D142): an Area's [CoverageRule] records name repository path
//     patterns; [App.DecisionsForPaths] answers which Decisions in force
//     apply to a list of paths, and [App.CheckCoverage] keeps the rules true
//     against a repository's file list.
//   - Pinned sources (D148): [Decision].Sources lists the external files a
//     Decision rests on, each at a full commit SHA with a role
//     ([DecisionSource]); set while proposed, frozen once ratified.
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
//   - Open and closed ([State].AwaitsAction, D109): a flow state may declare that
//     an item in it waits for someone to act (open); every other state is closed.
//     Readers ask the flow with [StatesAwaitingAction] or
//     [TransitionOption].ToAwaitsAction instead of keeping their own list.
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
//   - Signal kinds ([SignalTypes], [SignalTypeOf]): a Signal is a
//     condition core opens and resolves itself, or a request one actor
//     answers ([App.AnswerSignal]) or withdraws ([App.WithdrawSignal]).
//     Nothing expires: [App.ExpireSignals] is retired (D115), and
//     [App.OpenSignalsOlderThan] surfaces old open Signals instead.
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
//   - Per-item read rules ([ReadScope], [App.ReadScope], [LoadReadScope]): under
//     governance a caller reads only the items its grants name, and an item it
//     may not read answers exactly like a missing one, on the item surfaces
//     (get, list, writes, transitions) and the graph surfaces (context packet,
//     goal context, relations, reachability, provenance, audit), and dynamic
//     types ([DynamicTypeRepo.ListPage]). A Signal about a subject is visible
//     only when its subject is. [App.ResolveReadableID] turns a caller's name
//     for an item into its ID under the same rules. Published web
//     content stays public; a record type never is. A request loads its scope
//     once and attaches it with [WithReadScope]; trusted in-process code reads
//     past the rules only with [TrustedRead].
//   - The capability registry ([Capability], [App.Capabilities],
//     [App.CapabilityFor]): every act the MCP, REST and CLI surfaces project, as
//     a resource, a verb and the one governance operation word that gates it.
//     Content types are an argument of the item resource, and lifecycle is the
//     one transition verb. [App.AuthorizeCapability] checks a caller against a
//     capability under the read rules and grants, and [App.VisibleCapabilities]
//     lists what a caller may use.
//   - Search ([App.Search], [SearchQuery], [SearchResult]): a plain,
//     case-insensitive text search across every record type the caller may
//     read, filtered by type, state and place (unplaced included), with the
//     read rules applied before anything is returned or counted. A compiled
//     type marks searchable fields with smeldr_search:"text", a runtime type
//     with [SchemaField.Search].
//   - Record shape (D112): Decision, Task, Goal and Amendment carry a title of
//     at most [TitleMaxLength] characters, warned about first and refused after
//     [Config.TitleCutover]; amendment_type is the closed list [AmendmentTypes];
//     [MigrateRecordShape] brings existing records to that shape.
//   - Webhooks ([WebhookStore], [WebhookEndpoint], [WebhookEventPayload]) —
//     outbound delivery on lifecycle events, created with [NewWebhookStore].
//   - The event stream ([App.EventStream]) and its durable log
//     ([App.EventLog], [App.EventsAfter], [LoggedEvent]): every event is
//     logged in one order before it is delivered and kept 30 days, a
//     connection resumes with ?after=<event id>, and delivery is checked
//     against the reader's scope (D117).
//   - Subscriptions (D116, [App.Subscriptions], [Subscription],
//     [SubscriptionFilter]): an owner's standing request for events, filtered
//     at delivery and on replay, delivered on the stream (?subscription=) or
//     to a webhook endpoint. Every token has three defaults, and
//     [App.SeedRoleSubscriptions] adds a role's ([App.SeedRoleSubscriptionsReport]
//     also names the ones left out). A delivery is bounded by the
//     owner's read scope, reloaded when grants or anchored relations change;
//     a revoked token's connections close. [App.CanRead] answers the same
//     question for one item, and [MigrateWebhookSubscriptions] gives existing
//     webhook endpoints their subscriptions.
//   - Dynamic content types ([DynamicTypeRepo]) — a schema-driven repository
//     for content types registered at runtime (e.g. via MCP) rather than
//     compiled in, created with [NewDynamicTypeRepo].
package smeldr
