package forgemcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/forge-cms/forge"
)

// ServeStdio runs the MCP server over newline-delimited JSON on the given
// reader and writer. It is intended for use with local AI tools such as Claude
// Desktop and Cursor that launch the server as a child process and communicate
// over stdin/stdout:
//
//	srv.ServeStdio(ctx, os.Stdin, os.Stdout)
//
// Each request is read as a single JSON line, dispatched through [Server.handle],
// and the response is written as a single JSON line followed by a newline.
// Empty lines are skipped. Malformed JSON returns a -32700 parse-error response.
// ServeStdio returns when ctx is cancelled or in reaches EOF.
//
// The stdio transport runs with [forge.Admin] privileges — the process runs
// locally and the operator is trusted.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	forgeCtx := forge.NewContextWithUser(forge.User{
		ID:    "stdio",
		Roles: []forge.Role{forge.Admin},
	})

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)

	lineCh := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		errCh <- scanner.Err()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return err
		case line := <-lineCh:
			if strings.TrimSpace(line) == "" {
				continue
			}
			var resp jsonRPCResponse
			var req jsonRPCRequest
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				resp = jsonRPCResponse{
					JSONRPC: "2.0",
					Error:   &jsonRPCError{Code: -32700, Message: "parse error"},
				}
			} else {
				resp = s.handle(forgeCtx, req)
			}
			b, _ := json.Marshal(resp)
			fmt.Fprintf(out, "%s\n", b)
		}
	}
}

// Handler returns an [http.Handler] that serves the MCP protocol over HTTP
// Server-Sent Events (SSE). Two routes are registered on a fresh [http.ServeMux]:
//
//   - GET  /mcp         — SSE endpoint; upgrades to an event stream and sends an
//     initial "event: open" keepalive, then blocks until the client disconnects.
//   - POST /mcp/message — accepts a JSON-RPC request body, authenticates the
//     caller, dispatches through [Server.handle], and returns the JSON-RPC
//     response.
//
// Authentication (POST /mcp/message):
//   - If the server was constructed with a non-empty secret (auto-inherited from
//     [forge.App] via [New], or set via [WithSecret]), every request must carry a
//     valid "Authorization: Bearer <token>" header. Missing or invalid tokens
//     return HTTP 401 before any JSON-RPC processing.
//   - If no secret is configured, requests are treated as [forge.GuestUser].
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mcp", s.sseHandler)
	mux.HandleFunc("POST /mcp/message", s.messageHandler)
	return mux
}

// sseHandler handles GET /mcp, upgrading the HTTP connection to a Server-Sent
// Events stream. It sends an initial "event: open" keepalive and then blocks
// until the client disconnects or the request context is cancelled.
func (s *Server) sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	fmt.Fprint(w, "event: open\ndata: {}\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	<-r.Context().Done()
}

// messageHandler handles POST /mcp/message. It enforces the authentication
// boundary (HTTP 401) before any JSON-RPC decoding, applies a 1 MiB body
// limit, and writes a JSON-RPC response for every outcome.
func (s *Server) messageHandler(w http.ResponseWriter, r *http.Request) {
	// Authentication boundary: HTTP-level 401 before any JSON-RPC decoding.
	var forgeCtx forge.Context
	if len(s.secret) > 0 {
		user, ok := forge.VerifyBearerToken(r, s.secret, s.tokenStore)
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		forgeCtx = forge.NewContextWithUser(user)
	} else {
		// No secret configured: treat caller as GuestUser.
		forgeCtx = forge.NewContextWithUser(forge.GuestUser)
	}

	// Body limit: protect against large payloads.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "Request Too Large", http.StatusRequestEntityTooLarge)
			return
		}
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &jsonRPCError{Code: -32700, Message: "parse error"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
		return
	}

	resp := s.handle(forgeCtx, req)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
