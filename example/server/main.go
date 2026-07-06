// Package main is a generic Smeldr server with no custom content types.
// All content types are defined at runtime via define_content_type MCP tool.
// Optional subsystems are gated by environment variables; the binary compiles
// and runs with only SECRET set — all other features are opt-in.
//
// Run with:
//
//	cd example/server && go run .
//
// Required environment variables:
//
//	SECRET   HMAC signing secret (min 32 bytes in production)
//
// Optional environment variables:
//
//	BASE_URL              canonical origin (e.g. "https://cms.example.com")
//	DATABASE_PATH         path to the SQLite database (default: smeldr.db)
//	PORT                  HTTP listen port (default: 8080)
//	ADDR                  full listen address (default: 127.0.0.1:PORT)
//
//	ENABLE_TOKENS         wire database-backed named token management
//	ENABLE_GOVERNANCE     wire role-based access control (requires ENABLE_TOKENS for OAuth)
//	ENABLE_RELATIONS      wire the relation graph store
//	ENABLE_DYNAMIC_CONTENT wire the runtime content type system and schema store
//	ENABLE_BLOCKS         wire the block/composition system MCP tools
//	ENABLE_ORCHESTRATION  wire orchestration types (Signal, Task, Decision, Amendment, Goal)
//	ENABLE_REDIRECTS      wire database-backed redirect management
//	ENABLE_PAGE_META      wire per-path SEO override store
//	ENABLE_MEDIA          wire local media upload and management
//	MEDIA_STORE_BACKEND   media backend (default: local; only "local" supported)
//	ENABLE_SOCIAL         wire Mastodon social publishing
//	MASTODON_CLIENT_ID    Mastodon OAuth client ID (required when ENABLE_SOCIAL)
//	MASTODON_CLIENT_SECRET Mastodon OAuth client secret (required when ENABLE_SOCIAL)
//	MASTODON_INSTANCE_URL  Mastodon instance base URL (required when ENABLE_SOCIAL)
//	ENABLE_WEBHOOKS       wire outbound webhook delivery
//	ENABLE_AGENTS         wire the agent job system (connects to this server's own /mcp endpoint)
//	AGENT_MCP_URL         agent MCP endpoint (default: http://127.0.0.1:PORT/mcp/message)
//	AGENT_MCP_TOKEN       bearer token for agent MCP calls
//	OAUTH_ISSUER          enable OAuth 2.1; set to canonical issuer URL (requires ENABLE_TOKENS)
//	OAUTH_DB_PATH         path to the OAuth SQLite database (default: ./oauth.db)
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"
	agentflow "smeldr.dev/agent/flow"
	smeldr "smeldr.dev/core"
	"smeldr.dev/mcp"
	"smeldr.dev/media"
	"smeldr.dev/oauth"
	"smeldr.dev/social"
)

// ServerConfig holds all configuration derived from environment variables.
// Construct it via [parseConfig] for production use, or set fields directly in tests.
type ServerConfig struct {
	Secret               string
	BaseURL              string
	Port                 string
	Addr                 string
	EnableTokens         bool
	EnableGovernance     bool
	EnableRelations      bool
	EnableDynamicContent bool
	EnableBlocks         bool
	EnableOrchestration  bool
	EnableRedirects      bool
	EnablePageMeta       bool
	EnableMedia          bool
	MediaBackend         string
	EnableSocial         bool
	MastodonClientID     string
	MastodonClientSecret string
	MastodonInstanceURL  string
	EnableWebhooks       bool
	EnableAgents         bool
	AgentMCPURL          string
	AgentMCPToken        string
	OAuthIssuer          string
	OAuthDBPath          string
}

// ServerResult holds the live components returned by [buildApp].
// Call StopAll when shutting down to stop all background goroutines.
type ServerResult struct {
	App        *smeldr.App
	MCP        *mcp.Server
	TokenStore *smeldr.TokenStore // nil when EnableTokens=false
	StopAll    func()             // stops all background goroutines; safe to call multiple times
}

// parseConfig reads all server configuration from environment variables.
func parseConfig() ServerConfig {
	port := envOr("PORT", "8080")
	return ServerConfig{
		Secret:               requireEnv("SECRET"),
		BaseURL:              os.Getenv("BASE_URL"),
		Port:                 port,
		Addr:                 envOr("ADDR", "127.0.0.1:"+port),
		EnableTokens:         os.Getenv("ENABLE_TOKENS") != "",
		EnableGovernance:     os.Getenv("ENABLE_GOVERNANCE") != "",
		EnableRelations:      os.Getenv("ENABLE_RELATIONS") != "",
		EnableDynamicContent: os.Getenv("ENABLE_DYNAMIC_CONTENT") != "",
		EnableBlocks:         os.Getenv("ENABLE_BLOCKS") != "",
		EnableOrchestration:  os.Getenv("ENABLE_ORCHESTRATION") != "",
		EnableRedirects:      os.Getenv("ENABLE_REDIRECTS") != "",
		EnablePageMeta:       os.Getenv("ENABLE_PAGE_META") != "",
		EnableMedia:          os.Getenv("ENABLE_MEDIA") != "",
		MediaBackend:         envOr("MEDIA_STORE_BACKEND", "local"),
		EnableSocial:         os.Getenv("ENABLE_SOCIAL") != "",
		MastodonClientID:     os.Getenv("MASTODON_CLIENT_ID"),
		MastodonClientSecret: os.Getenv("MASTODON_CLIENT_SECRET"),
		MastodonInstanceURL:  os.Getenv("MASTODON_INSTANCE_URL"),
		EnableWebhooks:       os.Getenv("ENABLE_WEBHOOKS") != "",
		EnableAgents:         os.Getenv("ENABLE_AGENTS") != "",
		AgentMCPURL:          envOr("AGENT_MCP_URL", "http://127.0.0.1:"+port+"/mcp/message"),
		AgentMCPToken:        os.Getenv("AGENT_MCP_TOKEN"),
		OAuthIssuer:          os.Getenv("OAUTH_ISSUER"),
		OAuthDBPath:          envOr("OAUTH_DB_PATH", "./oauth.db"),
	}
}

// buildApp wires all enabled subsystems and returns the live server components.
// It does not call app.Run — that remains the caller's responsibility.
// All subsystem failures return an error instead of calling log.Fatalf.
func buildApp(cfg ServerConfig, db *sql.DB) (ServerResult, error) {
	if err := migrateDB(db); err != nil {
		return ServerResult{}, fmt.Errorf("migrate: %w", err)
	}

	if cfg.EnableRelations {
		if err := smeldr.CreateRelationTables(db); err != nil {
			return ServerResult{}, fmt.Errorf("create relation tables: %w", err)
		}
	}

	var tokenStore *smeldr.TokenStore
	if cfg.EnableTokens {
		tokenStore = smeldr.NewTokenStore(db, cfg.Secret)
	}

	app := smeldr.New(smeldr.Config{
		BaseURL:    cfg.BaseURL,
		Secret:     []byte(cfg.Secret),
		DB:         db,
		TokenStore: tokenStore,
	})

	if cfg.EnableGovernance {
		store := smeldr.NewRoleStore(db)
		if err := app.Governance(store); err != nil {
			return ServerResult{}, fmt.Errorf("governance: %w", err)
		}
	}

	if cfg.EnableRelations {
		store, err := smeldr.NewRelationStore(db)
		if err != nil {
			return ServerResult{}, fmt.Errorf("relation store: %w", err)
		}
		app.Relations(store)
	}

	// Early Handler call initialises the nav tree and probes smeldr_tokens.
	app.Handler()

	if cfg.EnableDynamicContent {
		app.ServeDynamicContent()
	}

	if cfg.EnableBlocks {
		// ServeDynamicContent also calls CreateBlockTables; this is idempotent.
		if err := smeldr.CreateBlockTables(db); err != nil {
			return ServerResult{}, fmt.Errorf("create block tables: %w", err)
		}
	}

	if cfg.EnableOrchestration {
		if err := smeldr.CreateOrchestrationTables(db); err != nil {
			return ServerResult{}, fmt.Errorf("create orchestration tables: %w", err)
		}
		smeldr.RegisterOrchestrationTypes(app, db)
	}

	if cfg.EnableRedirects {
		if err := app.Redirects(db); err != nil {
			return ServerResult{}, fmt.Errorf("redirects: %w", err)
		}
	}

	if cfg.EnablePageMeta {
		if err := smeldr.CreatePageMetaTable(db); err != nil {
			return ServerResult{}, fmt.Errorf("page meta table: %w", err)
		}
		app.PageMeta(smeldr.NewPageMetaStore(db))
	}

	var stopFuncs []func()
	var mcpOptions []mcp.ServerOption

	if cfg.EnableMedia {
		if cfg.MediaBackend != "local" {
			return ServerResult{}, fmt.Errorf("unsupported MEDIA_STORE_BACKEND %q (only \"local\" is supported)", cfg.MediaBackend)
		}
		store := media.NewLocalMediaStore(app)
		mediaSrv := media.Register(app, store)
		mcpOptions = append(mcpOptions, mcp.WithModule(mediaSrv))
	}

	if cfg.EnableSocial {
		srv := social.New(db, social.Config{
			Secret: []byte(cfg.Secret),
			Mastodon: social.MastodonConfig{
				ClientID:     cfg.MastodonClientID,
				ClientSecret: cfg.MastodonClientSecret,
				InstanceURL:  cfg.MastodonInstanceURL,
				RedirectURL:  cfg.BaseURL + "/oauth/mastodon/callback",
			},
		})
		srv.Register(app)
		stopFuncs = append(stopFuncs, srv.Stop)
		mcpOptions = append(mcpOptions,
			mcp.WithModule(srv.PostModule()),
			mcp.WithModule(srv.CredentialModule()),
			mcp.WithModule(srv.ConfigModule()),
			mcp.WithModule(srv.ScheduleModule()),
		)
	}

	if cfg.EnableWebhooks {
		app.Webhooks(smeldr.NewWebhookStore(db, []byte(cfg.Secret)))
	}

	// ENABLE_AGENTS must register before mcp.New so AgentJob appears in MCP tools.
	if cfg.EnableAgents {
		agentMod := agentflow.New(db, agentflow.Config{
			MCPURL:         cfg.AgentMCPURL,
			MCPToken:       cfg.AgentMCPToken,
			StreamableHTTP: true,
		})
		agentMod.Register(app)
		stopFuncs = append(stopFuncs, agentMod.Stop)
	}

	if cfg.OAuthIssuer != "" {
		oauthStore, err := oauth.NewSQLiteStore(cfg.OAuthDBPath)
		if err != nil {
			return ServerResult{}, fmt.Errorf("oauth store: %w", err)
		}
		stopFuncs = append(stopFuncs, func() { oauthStore.Close() }) //nolint:errcheck
		oauthSrv := oauth.New(oauth.Config{
			Issuer: cfg.OAuthIssuer,
			VerifyBearer: func(token string) bool {
				if tokenStore == nil {
					return false
				}
				_, ok := smeldr.VerifyTokenString(token, []byte(cfg.Secret), tokenStore)
				return ok
			},
		}, oauthStore)
		mcpOptions = append(mcpOptions, mcp.WithOAuth(oauthSrv), mcp.WithForgeFallback())
	}

	if cfg.EnableDynamicContent {
		mcpOptions = append(mcpOptions, mcp.WithDynamicContent())
	}
	if cfg.EnableBlocks {
		mcpOptions = append(mcpOptions, mcp.WithBlocks())
	}
	if cfg.EnablePageMeta {
		mcpOptions = append(mcpOptions, mcp.WithPageMeta(db))
	}

	mcpSrv := mcp.New(app, mcpOptions...)
	mcpSrv.Register(app)

	app.Health()

	stopAll := func() {
		for _, stop := range stopFuncs {
			stop()
		}
	}

	return ServerResult{
		App:        app,
		MCP:        mcpSrv,
		TokenStore: tokenStore,
		StopAll:    stopAll,
	}, nil
}

func main() {
	cfg := parseConfig()
	dbPath := envOr("DATABASE_PATH", "smeldr.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		log.Fatalf("smeldr-server: open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("smeldr-server: ping db: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	result, err := buildApp(cfg, db)
	if err != nil {
		log.Fatalf("smeldr-server: %v", err)
	}
	defer result.StopAll()

	log.Printf("smeldr-server: listening on %s", cfg.Addr)
	if err := result.App.Run(cfg.Addr); err != nil {
		log.Fatalf("smeldr-server: %v", err)
	}
}

// migrateDB creates the infrastructure tables that optional subsystems need.
// Called unconditionally — all statements are idempotent.
func migrateDB(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS smeldr_tokens (
			id         TEXT NOT NULL PRIMARY KEY,
			name       TEXT NOT NULL,
			role       TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL,
			expires_at TIMESTAMPTZ,
			revoked_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_webhook_endpoints (
			id         TEXT    PRIMARY KEY,
			events     TEXT    NOT NULL,
			target_url TEXT    NOT NULL,
			secret_enc TEXT    NOT NULL,
			active     BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			return fmt.Errorf("migrateDB: %w", err)
		}
	}
	return nil
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("smeldr-server: required env var %s is not set", key)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
