package forgemedia_test

import (
	"database/sql"

	forge "forge-cms.dev/forge"
	forgemedia "forge-cms.dev/forge-media"
)

// ExampleRegister demonstrates the minimal forge-media wiring pattern.
//
// Call Register to create the [Server] and mount all four HTTP routes
// (upload, serve, list, delete) on the application in a single step.
//
// To expose media via MCP, pass the returned *Server to
// forgemcp.WithModule when constructing the MCP server:
//
//	mcpSrv := forgemcp.New(app, forgemcp.WithModule(mediaSrv))
func ExampleRegister() {
	var db *sql.DB // initialise from your application database setup

	app := forge.New(forge.MustConfig(forge.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("change-this-secret-in-production!"),
		DB:      db,
	}))

	store := forgemedia.NewLocalMediaStore(app)
	forgemedia.Register(app, store)
}
