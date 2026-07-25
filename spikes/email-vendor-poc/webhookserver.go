package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// runWebhookServer starts a local HTTP server for flows 3-5 (inbound). It
// accepts POSTs at /webhook/{vendor} and persists the raw request body to
// received/{vendor}/{timestamp}.json for manual inspection.
//
// Deliberately schema-agnostic: this spike does not pre-guess the inbound
// payload shape from documentation that could not be reliably fetched (see
// sweego.go's comment on the same issue). Capturing the real payload from a
// real inbound send is the actual point of flows 3-5 — "verify against
// reality," per the spec's own stated discipline — not a parsed struct.
//
// Requires a publicly reachable URL (tunnel tool, per the approved plan) to
// actually receive vendor-originated webhook calls; not reachable from the
// public internet by itself.
func runWebhookServer(addr string) error {
	const outDir = "received"

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/", func(w http.ResponseWriter, r *http.Request) {
		vendor := r.URL.Path[len("/webhook/"):]
		if vendor == "" {
			http.Error(w, "vendor required in path, e.g. /webhook/lettermint", http.StatusBadRequest)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}

		vendorDir := filepath.Join(outDir, vendor)
		if err := os.MkdirAll(vendorDir, 0o755); err != nil {
			log.Printf("mkdir %s: %v", vendorDir, err)
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}

		fname := filepath.Join(vendorDir, fmt.Sprintf("%d.json", time.Now().UnixNano()))
		if err := os.WriteFile(fname, body, 0o644); err != nil {
			log.Printf("write %s: %v", fname, err)
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}

		var probe any
		if json.Unmarshal(body, &probe) == nil {
			log.Printf("[%s] webhook received -> %s (%d bytes, valid JSON)", vendor, fname, len(body))
		} else {
			log.Printf("[%s] webhook received -> %s (%d bytes, NOT valid JSON)", vendor, fname, len(body))
		}

		w.WriteHeader(http.StatusOK)
	})

	log.Printf("webhook server listening on %s (routes: /webhook/lettermint, /webhook/sweego)", addr)
	return http.ListenAndServe(addr, mux)
}
