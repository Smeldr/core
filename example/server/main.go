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

func main() {
	secret := requireEnv("SECRET")
	baseURL := os.Getenv("BASE_URL")
	dbPath := envOr("DATABASE_PATH", "smeldr.db")
	port := envOr("PORT", "8080")

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		log.Fatalf("smeldr-server: open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("smeldr-server: ping db: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if err := migrateDB(db); err != nil {
		log.Fatalf("smeldr-server: migrate: %v", err)
	}

	// ENABLE_RELATIONS: tables must exist before NewRelationStore.
	if os.Getenv("ENABLE_RELATIONS") != "" {
		if err := smeldr.CreateRelationTables(db); err != nil {
			log.Fatalf("smeldr-server: create relation tables: %v", err)
		}
	}

	// ENABLE_TOKENS: build TokenStore before Config so it wires into the App.
	var tokenStore *smeldr.TokenStore
	if os.Getenv("ENABLE_TOKENS") != "" {
		tokenStore = smeldr.NewTokenStore(db, secret)
	}

	app := smeldr.New(smeldr.Config{
		BaseURL:    baseURL,
		Secret:     []byte(secret),
		DB:         db,
		TokenStore: tokenStore,
	})

	if os.Getenv("ENABLE_GOVERNANCE") != "" {
		store := smeldr.NewRoleStore(db)
		if err := app.Governance(store); err != nil {
			log.Fatalf("smeldr-server: governance: %v", err)
		}
	}

	if os.Getenv("ENABLE_RELATIONS") != "" {
		store, err := smeldr.NewRelationStore(db)
		if err != nil {
			log.Fatalf("smeldr-server: relation store: %v", err)
		}
		app.Relations(store)
	}

	// Early Handler call initialises the nav tree and probes smeldr_tokens.
	app.Handler()

	if os.Getenv("ENABLE_DYNAMIC_CONTENT") != "" {
		app.ServeDynamicContent()
	}

	if os.Getenv("ENABLE_BLOCKS") != "" {
		// ServeDynamicContent also calls CreateBlockTables; this is idempotent.
		if err := smeldr.CreateBlockTables(db); err != nil {
			log.Fatalf("smeldr-server: create block tables: %v", err)
		}
	}

	if os.Getenv("ENABLE_REDIRECTS") != "" {
		if err := app.Redirects(db); err != nil {
			log.Fatalf("smeldr-server: redirects: %v", err)
		}
	}

	if os.Getenv("ENABLE_PAGE_META") != "" {
		if err := smeldr.CreatePageMetaTable(db); err != nil {
			log.Fatalf("smeldr-server: page meta table: %v", err)
		}
		app.PageMeta(smeldr.NewPageMetaStore(db))
	}

	var stopFuncs []func()
	var mcpOptions []mcp.ServerOption

	if os.Getenv("ENABLE_MEDIA") != "" {
		if backend := envOr("MEDIA_STORE_BACKEND", "local"); backend != "local" {
			log.Fatalf("smeldr-server: unsupported MEDIA_STORE_BACKEND %q (only \"local\" is supported)", backend)
		}
		store := media.NewLocalMediaStore(app)
		mediaSrv := media.Register(app, store)
		mcpOptions = append(mcpOptions, mcp.WithModule(mediaSrv))
	}

	if os.Getenv("ENABLE_SOCIAL") != "" {
		srv := social.New(db, social.Config{
			Secret: []byte(secret),
			Mastodon: social.MastodonConfig{
				ClientID:     os.Getenv("MASTODON_CLIENT_ID"),
				ClientSecret: os.Getenv("MASTODON_CLIENT_SECRET"),
				InstanceURL:  os.Getenv("MASTODON_INSTANCE_URL"),
				RedirectURL:  baseURL + "/oauth/mastodon/callback",
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

	if os.Getenv("ENABLE_WEBHOOKS") != "" {
		app.Webhooks(smeldr.NewWebhookStore(db, []byte(secret)))
	}

	// ENABLE_AGENTS must register before mcp.New so AgentJob appears in MCP tools.
	if os.Getenv("ENABLE_AGENTS") != "" {
		agentMod := agentflow.New(db, agentflow.Config{
			MCPURL:         envOr("AGENT_MCP_URL", "http://127.0.0.1:"+port+"/mcp/message"),
			MCPToken:       os.Getenv("AGENT_MCP_TOKEN"),
			StreamableHTTP: true,
		})
		agentMod.Register(app)
		stopFuncs = append(stopFuncs, agentMod.Stop)
	}

	// OAUTH_ISSUER: existing pattern, unchanged from site-dev.
	if oauthIssuer := os.Getenv("OAUTH_ISSUER"); oauthIssuer != "" {
		oauthDBPath := envOr("OAUTH_DB_PATH", "./oauth.db")
		oauthStore, err := oauth.NewSQLiteStore(oauthDBPath)
		if err != nil {
			log.Fatalf("smeldr-server: oauth store: %v", err)
		}
		defer oauthStore.Close()
		oauthSrv := oauth.New(oauth.Config{
			Issuer: oauthIssuer,
			VerifyBearer: func(token string) bool {
				if tokenStore == nil {
					return false
				}
				_, ok := smeldr.VerifyTokenString(token, []byte(secret), tokenStore)
				return ok
			},
		}, oauthStore)
		mcpOptions = append(mcpOptions, mcp.WithOAuth(oauthSrv), mcp.WithForgeFallback())
	}

	if os.Getenv("ENABLE_DYNAMIC_CONTENT") != "" {
		mcpOptions = append(mcpOptions, mcp.WithDynamicContent())
	}
	if os.Getenv("ENABLE_BLOCKS") != "" {
		mcpOptions = append(mcpOptions, mcp.WithBlocks())
	}
	if os.Getenv("ENABLE_PAGE_META") != "" {
		mcpOptions = append(mcpOptions, mcp.WithPageMeta(db))
	}

	mcpSrv := mcp.New(app, mcpOptions...)
	mcpSrv.Register(app)

	app.Health()

	defer func() {
		for _, stop := range stopFuncs {
			stop()
		}
	}()

	addr := envOr("ADDR", "127.0.0.1:"+port)
	log.Printf("smeldr-server: listening on %s", addr)
	if err := app.Run(addr); err != nil {
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
