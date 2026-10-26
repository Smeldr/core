package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	smeldr "smeldr.dev/core"
	"smeldr.dev/github"
)

// gitHubCoreEvent is the core event the adapter turns into a new commit
// status and comment.
const gitHubCoreEvent = "changecheck.transitioned"

// gitHubConfig is the environment of ENABLE_GITHUB (T940): the pull request
// check's GitHub adapter (smeldr.dev/github), configured without Go.
type gitHubConfig struct {
	AppID             string   // GITHUB_APP_ID
	PrivateKey        string   // GITHUB_APP_PRIVATE_KEY (PEM; a literal \n is a newline)
	PrivateKeyFile    string   // GITHUB_APP_PRIVATE_KEY_FILE
	WebhookSecret     string   // GITHUB_WEBHOOK_SECRET
	AdapterTokenID    string   // GITHUB_ADAPTER_TOKEN_ID: the token_id create_token returned (the token's user ID, not list_tokens' fingerprint) of a job token that holds check_change
	CoreWebhookSecret string   // GITHUB_CORE_WEBHOOK_SECRET: optional; set means the operator registers the core webhook
	CheckURL          string   // GITHUB_CHECK_URL
	ItemURL           string   // GITHUB_ITEM_URL
	NamesShown        []string // GITHUB_NAMES_SHOWN, comma separated owner/name
}

// parseGitHubConfig reads the GITHUB_* variables.
func parseGitHubConfig(getenv func(string) string) gitHubConfig {
	var names []string
	for _, n := range strings.Split(getenv("GITHUB_NAMES_SHOWN"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return gitHubConfig{
		AppID:             strings.TrimSpace(getenv("GITHUB_APP_ID")),
		PrivateKey:        getenv("GITHUB_APP_PRIVATE_KEY"),
		PrivateKeyFile:    strings.TrimSpace(getenv("GITHUB_APP_PRIVATE_KEY_FILE")),
		WebhookSecret:     getenv("GITHUB_WEBHOOK_SECRET"),
		AdapterTokenID:    strings.TrimSpace(getenv("GITHUB_ADAPTER_TOKEN_ID")),
		CoreWebhookSecret: getenv("GITHUB_CORE_WEBHOOK_SECRET"),
		CheckURL:          strings.TrimSpace(getenv("GITHUB_CHECK_URL")),
		ItemURL:           strings.TrimSpace(getenv("GITHUB_ITEM_URL")),
		NamesShown:        names,
	}
}

// mountGitHub mounts the GitHub adapter on POST /github/webhook (the GitHub
// App's webhook URL) and POST /github/core-events (a core webhook for
// changecheck.transitioned). Every check fails closed and names the variable
// or the dependency, never a value: no secret, key or token is logged or
// returned.
func mountGitHub(ctx context.Context, cfg ServerConfig, app *smeldr.App, tokens *smeldr.TokenStore, webhooks *smeldr.WebhookStore) error {
	if !cfg.EnableTokens || !cfg.EnableGovernance || !cfg.EnableRelations || !cfg.EnableWebhooks || !cfg.governanceRecords() {
		return errors.New("ENABLE_GITHUB requires ENABLE_TOKENS, ENABLE_GOVERNANCE, ENABLE_RELATIONS, ENABLE_WEBHOOKS and ENABLE_GOVERNANCE_RECORDS (or ENABLE_ORCHESTRATION)")
	}
	g := cfg.GitHub
	var missing []string
	if g.AppID == "" {
		missing = append(missing, "GITHUB_APP_ID")
	}
	if g.PrivateKey == "" && g.PrivateKeyFile == "" {
		missing = append(missing, "GITHUB_APP_PRIVATE_KEY (or GITHUB_APP_PRIVATE_KEY_FILE)")
	}
	if g.WebhookSecret == "" {
		missing = append(missing, "GITHUB_WEBHOOK_SECRET")
	}
	if g.AdapterTokenID == "" {
		missing = append(missing, "GITHUB_ADAPTER_TOKEN_ID")
	}
	if len(missing) > 0 {
		return fmt.Errorf("ENABLE_GITHUB: not set: %s", strings.Join(missing, ", "))
	}
	key, err := gitHubPrivateKey(g)
	if err != nil {
		return err
	}
	if err := checkGitHubActor(ctx, app, tokens, g.AdapterTokenID); err != nil {
		return err
	}
	coreSecret := g.CoreWebhookSecret
	if coreSecret == "" {
		if coreSecret, err = ensureGitHubCoreWebhook(ctx, webhooks, strings.TrimRight(cfg.BaseURL, "/")+"/github/core-events"); err != nil {
			return err
		}
	}
	adapter, err := github.New(github.Config{
		Checker:           app,
		Actor:             smeldr.User{ID: g.AdapterTokenID},
		WebhookSecret:     []byte(g.WebhookSecret),
		CoreWebhookSecret: []byte(coreSecret),
		AppID:             g.AppID,
		PrivateKeyPEM:     key,
		CheckURL:          g.CheckURL,
		ItemURL:           g.ItemURL,
		NamesShown:        g.NamesShown,
	})
	if err != nil {
		// The adapter's errors name the Config field and the parse failure,
		// never key bytes.
		return fmt.Errorf("ENABLE_GITHUB: GITHUB_APP_PRIVATE_KEY is not an RSA private key in PEM: %w", err)
	}
	app.Handle("POST /github/webhook", adapter)
	app.Handle("POST /github/core-events", adapter.CoreEvents())
	slog.InfoContext(ctx, "github: adapter mounted", "webhook", "/github/webhook", "core_events", "/github/core-events", "app_id", g.AppID)
	return nil
}

// gitHubPrivateKey returns the App's key from GITHUB_APP_PRIVATE_KEY or the
// file GITHUB_APP_PRIVATE_KEY_FILE names, never both.
func gitHubPrivateKey(g gitHubConfig) ([]byte, error) {
	if g.PrivateKey != "" && g.PrivateKeyFile != "" {
		return nil, errors.New("ENABLE_GITHUB: GITHUB_APP_PRIVATE_KEY and GITHUB_APP_PRIVATE_KEY_FILE are both set; set one")
	}
	if g.PrivateKeyFile != "" {
		b, err := os.ReadFile(g.PrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("ENABLE_GITHUB: GITHUB_APP_PRIVATE_KEY_FILE cannot be read: %w", err)
		}
		return b, nil
	}
	return []byte(strings.ReplaceAll(g.PrivateKey, `\n`, "\n")), nil
}

// checkGitHubActor checks that the adapter's identity is a live job token's
// user that holds check_change: an adapter never acts as a person.
func checkGitHubActor(ctx context.Context, app *smeldr.App, tokens *smeldr.TokenStore, userID string) error {
	records, err := tokens.List(ctx)
	if err != nil {
		return fmt.Errorf("ENABLE_GITHUB: GITHUB_ADAPTER_TOKEN_ID: the tokens cannot be read: %w", err)
	}
	now := time.Now().UTC()
	var token *smeldr.TokenRecord
	for i, r := range records {
		if r.UserID != nil && *r.UserID == userID && r.RevokedAt.IsZero() && r.ExpiresAt.After(now) {
			token = &records[i]
			break
		}
	}
	switch {
	case token == nil:
		return errors.New("ENABLE_GITHUB: GITHUB_ADAPTER_TOKEN_ID: no live token has that token_id (the one create_token returned)")
	case token.ActorClass != "job":
		return errors.New("ENABLE_GITHUB: GITHUB_ADAPTER_TOKEN_ID: the token is not of class job; create the adapter's token with class job")
	}
	err = app.AuthorizeCapability(smeldr.NewContextWithUser(smeldr.User{ID: userID}), smeldr.ResourceItem, "check_change", smeldr.AuthTarget{})
	switch {
	case errors.Is(err, smeldr.ErrForbidden):
		return errors.New("ENABLE_GITHUB: GITHUB_ADAPTER_TOKEN_ID: the token does not hold check_change; grant it a role with manage, for example editor")
	case err != nil:
		return fmt.Errorf("ENABLE_GITHUB: GITHUB_ADAPTER_TOKEN_ID: check_change cannot be checked: %w", err)
	}
	return nil
}

// ensureGitHubCoreWebhook returns the signing secret of the core webhook for
// changecheck.transitioned to target, creating the webhook when there is
// none: the server owns both ends, so the operator copies no secret, and a
// restart reuses the same webhook.
func ensureGitHubCoreWebhook(ctx context.Context, webhooks *smeldr.WebhookStore, target string) (string, error) {
	endpoints, err := webhooks.EndpointsForEvent(ctx, gitHubCoreEvent)
	if err != nil {
		return "", fmt.Errorf("ENABLE_GITHUB: the core webhook for %s cannot be read: %w", gitHubCoreEvent, err)
	}
	for _, ep := range endpoints {
		if ep.TargetURL != target {
			continue
		}
		secret, err := webhooks.DecryptSecret(ep)
		if err != nil {
			return "", fmt.Errorf("ENABLE_GITHUB: the core webhook for %s cannot be read: %w", gitHubCoreEvent, err)
		}
		return string(secret), nil
	}
	_, secret, err := webhooks.Create(ctx, target, []string{gitHubCoreEvent})
	if err != nil {
		return "", fmt.Errorf("ENABLE_GITHUB: the core webhook for %s cannot be created (BASE_URL must be a public https URL): %w", gitHubCoreEvent, err)
	}
	slog.InfoContext(ctx, "github: core webhook created", "event", gitHubCoreEvent, "target", target)
	return secret, nil
}
