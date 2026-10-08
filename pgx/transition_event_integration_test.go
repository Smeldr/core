//go:build integration

package pgx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// EventPost is a compiled type for the D107 per-path proof on Postgres.
type EventPost struct {
	smeldr.Node
	Title string
}

// eventCounts counts App-level deliveries per "<event>:<item id>".
type eventCounts struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *eventCounts) add(k string) {
	c.mu.Lock()
	c.n[k]++
	c.mu.Unlock()
}

func (c *eventCounts) get(k string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[k]
}

// Every status change on Postgres, through HTTP PUT, the MCP publish tool,
// transition_item and a runtime-defined type's status change, gives one
// transition record, AfterPublish once on the bus, no AfterUpdate, and one
// AfterTransition to the listeners (D107).
func TestPG_OneRecordAndEventPerTransition(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE event_posts (
		id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
		published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL, rev INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, create := range []func(smeldr.DB) error{smeldr.CreateProvenanceTable, smeldr.CreateBlockTables, smeldr.CreateSchemaTable} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}

	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	app.Provenance(smeldr.NewProvenanceStore(db))
	counts := &eventCounts{n: map[string]int{}}
	for _, sig := range []smeldr.LifecycleEvent{smeldr.AfterUpdate, smeldr.AfterPublish} {
		s := sig
		app.OnSignal(s, func(_ context.Context, ev smeldr.SignalEvent) error {
			counts.add(string(s) + ":" + ev.NodeID)
			return nil
		})
	}
	app.AddSignalListener(func(sig smeldr.LifecycleEvent, _ string, item any) {
		if sig != smeldr.AfterTransition {
			return
		}
		switch v := item.(type) {
		case *EventPost:
			counts.add(string(sig) + ":" + v.ID)
		case *smeldr.DynamicNode:
			counts.add(string(sig) + ":" + v.ID)
		}
	})
	repo := smeldr.NewSQLRepo[*EventPost](db, smeldr.Table("event_posts"))
	m := smeldr.NewModule((*EventPost)(nil), smeldr.Repo(repo), smeldr.At("/event-posts"))
	app.Content(m)
	fields, _ := json.Marshal([]smeldr.SchemaField{{Name: "Title", Type: "string", Required: true, Role: "title"}})
	desc, err := app.DefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "memo", Kind: "content", Fields: fields})
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	h := app.Handler()

	caller := smeldr.NewContextWithUser(smeldr.User{ID: "pg-user", Roles: []smeldr.Role{smeldr.Editor}})
	tok, err := smeldr.SignToken(smeldr.User{ID: "pg-user", Roles: []smeldr.Role{smeldr.Editor}}, pgTestSecret, 0)
	if err != nil {
		t.Fatal(err)
	}
	seed := func(id string) {
		if err := repo.Save(ctx, &EventPost{Node: smeldr.Node{ID: id, Slug: id, Status: smeldr.Draft}, Title: "Event " + id}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	paths := map[string]func(t *testing.T) string{
		"PUT": func(t *testing.T) string {
			seed("ep-put")
			b, _ := json.Marshal(map[string]any{"Title": "Event ep-put", "Status": "published"})
			r := httptest.NewRequest(http.MethodPut, "/event-posts/ep-put", bytes.NewReader(b))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+tok)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code >= 300 {
				t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
			}
			return "ep-put"
		},
		"MCP publish": func(t *testing.T) string {
			seed("ep-mcp")
			if err := m.MCPPublish(caller, "ep-mcp", ""); err != nil {
				t.Fatalf("MCPPublish: %v", err)
			}
			return "ep-mcp"
		},
		"transition_item": func(t *testing.T) string {
			seed("ep-ti")
			if _, err := app.TransitionItem(caller, "EventPost", "ep-ti", string(smeldr.Published)); err != nil {
				t.Fatalf("TransitionItem: %v", err)
			}
			return "ep-ti"
		},
		"dynamic": func(t *testing.T) string {
			dyn, err := app.DynamicContentRepo(desc.Name)
			if err != nil {
				t.Fatal(err)
			}
			node, err := dyn.CreateDraft(ctx, map[string]any{"Title": "Dynamic"})
			if err != nil {
				t.Fatalf("CreateDraft: %v", err)
			}
			if err := dyn.SetStatus(caller, node.ID, smeldr.Published); err != nil {
				t.Fatalf("SetStatus: %v", err)
			}
			return node.ID
		},
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			id := run(t)
			publish := string(smeldr.AfterPublish) + ":" + id
			moved := string(smeldr.AfterTransition) + ":" + id
			deadline := time.Now().Add(3 * time.Second)
			for (counts.get(publish) < 1 || counts.get(moved) < 1) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			if got := counts.get(publish); got != 1 {
				t.Errorf("AfterPublish on the bus = %d; want 1", got)
			}
			if got := counts.get(string(smeldr.AfterUpdate) + ":" + id); got != 0 {
				t.Errorf("AfterUpdate on the bus = %d; want 0 for a status-only change", got)
			}
			if got := counts.get(moved); got != 1 {
				t.Errorf("AfterTransition = %d; want 1", got)
			}
			var n int
			if err := db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM smeldr_provenance WHERE subject_id = $1 AND verb = 'transition'`, id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Errorf("transition records = %d; want 1", n)
			}
		})
	}
}
