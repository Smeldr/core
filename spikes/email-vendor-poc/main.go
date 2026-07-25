// Command email-vendor-poc is a throwaway spike comparing Lettermint and
// Sweego against the five flows in
// smeldr/architect/analysis/transactional-email-poc-2026-07-19.md (T158).
//
// Each flow is triggered by hand, not go test — flows 3-5 need a human to
// check an inbox, observe webhook delivery, or eyeball a persisted payload,
// which isn't something a unit-test assertion can verify. See REPORT.md for
// results as each flow is run.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

type envConfig struct {
	sweegoAPIKey       string
	sweegoAPISecret    string
	sweegoSMTPHost     string
	sweegoSenderDomain string
	lettermintToken    string
}

// loadEnv reads .env (KEY=VALUE lines, # comments ignored) from the current
// directory. No dependency pulled in for this — the format is trivial and
// this spike's go.mod stays at zero dependencies, matching the code-volume
// comparison the spec asks for.
func loadEnv() (envConfig, error) {
	f, err := os.Open(".env")
	if err != nil {
		return envConfig{}, fmt.Errorf("open .env (copy .env.example, fill in real values): %w", err)
	}
	defer f.Close()

	vals := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if err := scanner.Err(); err != nil {
		return envConfig{}, err
	}

	return envConfig{
		sweegoAPIKey:       vals["SWEEGO_API_KEY"],
		sweegoAPISecret:    vals["SWEEGO_API_SECRET"],
		sweegoSMTPHost:     vals["SWEEGO_SMTP_HOST"],
		sweegoSenderDomain: vals["SWEEGO_SENDER_DOMAIN"],
		lettermintToken:    vals["LETTERMINT_PROJECT_TOKEN"],
	}, nil
}

func main() {
	webhook := flag.Bool("webhook", false, "start the inbound webhook receiver (flows 3-5 setup) instead of sending")
	addr := flag.String("addr", ":8089", "listen address for -webhook")
	vendor := flag.String("vendor", "", "lettermint|sweego (flows 1-2)")
	flowFlag := flag.Int("flow", 0, "1 or 2 (outbound flows; 3-5 are inbound, use -webhook)")
	from := flag.String("from", "", "sender address — must be verified/allowed in the vendor's own dashboard")
	to := flag.String("to", "", "recipient address (flows 1-2)")
	sendHTML := flag.Bool("html", false, "also send an HTML body (needed for open/click tracking pixels — plain-text-only sends cannot be tracked)")
	flag.Parse()

	if *webhook {
		if err := runWebhookServer(*addr); err != nil {
			log.Fatal(err)
		}
		return
	}

	cfg, err := loadEnv()
	if err != nil {
		log.Fatal(err)
	}

	switch *flowFlag {
	case 1:
		runFlow(cfg, *vendor, *from, *to, *sendHTML,
			"Your Smeldr sign-in link",
			"Click to sign in: https://smeldr.example/auth/magic?token=poc-test-token-abc123\n\nThis link expires in 15 minutes.")
	case 2:
		runFlow(cfg, *vendor, *from, *to, *sendHTML,
			"You've been invited to join an org on Smeldr",
			"Peter Ravn Thers invited you to join their organization on Smeldr.\n\nAccept: https://smeldr.example/org/invite?token=poc-invite-token-xyz789")
	case 3, 4, 5:
		log.Fatalf("flow %d is inbound: start the receiver with `go run . -webhook`, "+
			"expose it via a tunnel, register the tunnel URL as the inbound webhook in "+
			"the %s dashboard, then send a real email to the configured inbound address "+
			"(flow 4: attach a PDF). Check received/%s/ for the captured payload.",
			*flowFlag, *vendor, *vendor)
	default:
		log.Fatal("set -flow=1 or -flow=2 (outbound), or -webhook (inbound receiver for flows 3-5)")
	}
}

// runFlow sends one outbound email (flows 1-2 share this — only subject/body differ).
// When sendHTML is set, an HTML alternative body is included alongside the plain-text
// body — open/click tracking (pixel + link rewriting) generally requires HTML content;
// a plain-text-only send has nothing for a vendor to instrument.
func runFlow(cfg envConfig, vendor, from, to string, sendHTML bool, subject, text string) {
	if to == "" {
		log.Fatal("-to is required")
	}
	if from == "" {
		log.Fatal("-from is required — must be a sender address verified/allowed in the vendor's dashboard")
	}

	html := ""
	if sendHTML {
		html = textToSimpleHTML(text)
	}

	switch vendor {
	case "lettermint":
		resp, raw, err := sendLettermint(cfg.lettermintToken, lettermintSendRequest{
			From:    from,
			To:      []string{to},
			Subject: subject,
			Text:    text,
			HTML:    html,
		})
		if err != nil {
			log.Fatalf("lettermint send failed: %v", err)
		}
		fmt.Printf("lettermint: sent, message_id=%s status=%s\nraw response: %s\n", resp.MessageID, resp.Status, raw)
	case "sweego":
		resp, raw, err := sendSweego(cfg.sweegoAPIKey, sweegoSendRequest{
			From:        sweegoAddress{Email: from},
			Recipients:  []sweegoAddress{{Email: to}},
			Subject:     subject,
			MessageTxt:  text,
			MessageHTML: html,
		})
		if err != nil {
			log.Fatalf("sweego send failed: %v", err)
		}
		fmt.Printf("sweego: sent, transaction_id=%s credit_left=%s\nraw response: %s\n", resp.TransactionID, resp.CreditLeft, raw)
	default:
		log.Fatalf("unknown vendor %q — want lettermint or sweego (-vendor=...)", vendor)
	}
}

// textToSimpleHTML wraps plain text in a minimal HTML body, converting bare
// https:// URLs into real <a href> anchors — the shape most vendors' click-tracking
// rewriting actually operates on.
func textToSimpleHTML(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		start := strings.Index(line, "https://")
		if start == -1 {
			lines[i] = "<p>" + line + "</p>"
			continue
		}
		end := len(line)
		for j := start; j < len(line); j++ {
			if line[j] == ' ' {
				end = j
				break
			}
		}
		url := line[start:end]
		lines[i] = "<p>" + line[:start] + `<a href="` + url + `">` + url + `</a>` + line[end:] + "</p>"
	}
	return "<html><body>" + strings.Join(lines, "\n") + "</body></html>"
}
