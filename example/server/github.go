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
// changecheck.transitioned). validateBoot has checked the configuration and
// checkGitHubToken and checkGitHubGrant the adapter's token, all before the
// first write (T955), so what is left here is the one write (the core
// webhook) and the mount. No secret, key or token is logged or returned.
func mountGitHub(ctx context.Context, cfg ServerConfig, app *smeldr.App, webhooks *smeldr.WebhookStore, key []byte) error {
	g := cfg.GitHub
	coreSecret := g.CoreWebhookSecret
	if coreSecret == "" {
		var err error
		if coreSecret, err = ensureGitHubCoreWebhook(ctx, webhooks, gitHubCoreEventsURL(cfg)); err != nil {
			return err
		}
	}
	adapterCfg := gitHubAdapterConfig(cfg, app, key)
	adapterCfg.CoreWebhookSecret = []byte(coreSecret)
	adapterCfg.NamesShown = g.NamesShown
	adapter, err := github.New(adapterCfg)
	if err != nil {
		return gitHubKeyError(err)
	}
	app.Handle("POST /github/webhook", adapter)
	app.Handle("POST /github/core-events", adapter.CoreEvents())
	slog.InfoContext(ctx, "github: adapter mounted", "webhook", "/github/webhook", "core_events", "/github/core-events", "app_id", g.AppID)
	return nil
}

// gitHubAdapterConfig is the adapter's Config without the core webhook
// secret and the names shown.
func gitHubAdapterConfig(cfg ServerConfig, checker github.Checker, key []byte) github.Config {
	g := cfg.GitHub
	return github.Config{
		Checker:       checker,
		Actor:         smeldr.User{ID: g.AdapterTokenID},
		WebhookSecret: []byte(g.WebhookSecret),
		AppID:         g.AppID,
		PrivateKeyPEM: key,
		CheckURL:      g.CheckURL,
		ItemURL:       g.ItemURL,
	}
}

// noChecker stands in for the App in checkGitHubAdapterConfig's check build:
// github.New makes no call, so none of these runs.
type noChecker struct{}

func (noChecker) CheckChange(context.Context, smeldr.ChangeInput) (smeldr.ChangeCheckResult, error) {
	return smeldr.ChangeCheckResult{}, errors.New("github: check build only")
}

func (noChecker) RecordMerge(context.Context, smeldr.MergeInput) (smeldr.MergeResult, error) {
	return smeldr.MergeResult{}, errors.New("github: check build only")
}

func (noChecker) ChangeCheckOf(context.Context, string) (smeldr.ChangeCheckResult, error) {
	return smeldr.ChangeCheckResult{}, errors.New("github: check build only")
}

// checkGitHubAdapterConfig validates the adapter's own config (the key
// parses) by a check build of github.New, before any write (T946, T955).
// New only checks: no network call, no goroutine, and no log while
// NamesShown is unset.
func checkGitHubAdapterConfig(cfg ServerConfig, key []byte) error {
	check := gitHubAdapterConfig(cfg, noChecker{}, key)
	check.CoreWebhookSecret = []byte("pending")
	if _, err := github.New(check); err != nil {
		return gitHubKeyError(err)
	}
	return nil
}

// gitHubCoreEventsURL is the target of the core webhook the server registers.
func gitHubCoreEventsURL(cfg ServerConfig) string {
	return strings.TrimRight(cfg.BaseURL, "/") + "/github/core-events"
}

// gitHubKeyError is the refusal for an adapter config that github.New
// refuses; with every other field checked before, that is the key. The
// adapter's errors name the Config field and the parse failure, never key
// bytes.
func gitHubKeyError(err error) error {
	return fmt.Errorf("ENABLE_GITHUB: GITHUB_APP_PRIVATE_KEY is not an RSA private key in PEM: %w", err)
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

// checkGitHubToken checks that the adapter's identity is a live job token's
// user: an adapter never acts as a person. It only reads, and buildApp calls
// it before its first write (T955). On a database with no token table the
// read fails and the boot is refused with nothing written.
func checkGitHubToken(ctx context.Context, tokens *smeldr.TokenStore, userID string) error {
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
	return nil
}

// checkGitHubGrant checks that the adapter's user holds check_change. It
// needs the role store wired, so buildApp calls it right after
// App.Governance and before every other write. It is only reached when
// checkGitHubToken found a live token, so the governance tables exist and
// Governance's idempotent migration adds no schema (T955).
func checkGitHubGrant(app *smeldr.App, userID string) error {
	err := app.AuthorizeCapability(smeldr.NewContextWithUser(smeldr.User{ID: userID}), smeldr.ResourceItem, "check_change", smeldr.AuthTarget{})
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
