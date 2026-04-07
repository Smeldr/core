package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// runStatus calls /_health on the configured Forge instance and prints the
// response. Exits with code 1 if the instance is unreachable or unhealthy.
func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: forge-cli status")
	}
	fs.Parse(args) //nolint:errcheck

	cfg, err := loadConfig()
	if err != nil {
		fatal("%v", err)
	}

	url := cfg.ForgeURL + "/_health"
	raw, code, err := request(cfg, http.MethodGet, url, nil)
	if err != nil {
		fatal("unable to reach %s: %v", cfg.ForgeURL, err)
	}
	if code >= 400 {
		fatal("/_health returned %d: %s", code, strings.TrimSpace(string(raw)))
	}

	var health map[string]any
	if err := json.Unmarshal(raw, &health); err != nil {
		// Not JSON — print raw
		fmt.Println(strings.TrimSpace(string(raw)))
		return
	}

	out, _ := json.MarshalIndent(health, "", "  ")
	fmt.Println(string(out))
}
