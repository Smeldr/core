package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"smeldr.dev/agent"
	smeldr "smeldr.dev/core"
)

// Default cron schedules, used when ServerConfig is built by hand rather than
// by parseConfig, whose envOr defaults are the same.
const (
	defaultStructuralSweepSchedule = "0 * * * *"
	defaultEvalQueueDrainSchedule  = "*/5 * * * *"
	defaultStandingDriftSchedule   = "30 3 * * *"
)

// bootChecks holds what validateBoot parsed, so buildApp parses nothing twice.
type bootChecks struct {
	titleCutover         time.Time
	signalMessageCutover time.Time
	signalEventCutover   time.Time
	gitHubKey            []byte // the App's private key, when ENABLE_GITHUB
}

// validateBoot checks every refusal that depends on the configuration alone:
// flag dependencies, required variables and values that must parse. buildApp
// calls it before its first write, so a refused boot writes nothing to the
// database (T955; the rule T946 and T948 follow). It opens no database,
// leaves no goroutine running and logs nothing. Each error names the
// variable, never a secret value. A new flag adds its refusal here.
func validateBoot(cfg ServerConfig) (bootChecks, error) {
	var checks bootChecks
	// MustConfig's rules (core's ValidateConfig), so a missing BASE_URL or a
	// short SECRET is refused here, before any write, instead of panicking
	// in smeldr.New after the first one (T969).
	if _, err := smeldr.ValidateConfig(smeldr.Config{BaseURL: cfg.BaseURL, Secret: []byte(cfg.Secret)}); err != nil {
		return checks, configError(err)
	}
	var err error
	if checks.titleCutover, err = parseCutover("TITLE_CUTOVER", cfg.TitleCutover); err != nil {
		return checks, err
	}
	if checks.signalMessageCutover, err = parseCutover("SIGNAL_MESSAGE_CUTOVER", cfg.SignalMessageCutover); err != nil {
		return checks, err
	}
	if checks.signalEventCutover, err = parseCutover("SIGNAL_EVENT_CUTOVER", cfg.SignalEventCutover); err != nil {
		return checks, err
	}
	switch {
	case cfg.governanceRecords() && !cfg.EnableRelations:
		return checks, fmt.Errorf("%s requires ENABLE_RELATIONS (a Decision is ratified in a place, and a place is a relation)", recordsFlag(cfg))
	case cfg.EnableCheck && (!cfg.EnableAuthority || !cfg.governanceRecords()):
		return checks, errors.New("ENABLE_CHECK requires ENABLE_AUTHORITY and ENABLE_GOVERNANCE_RECORDS (or ENABLE_ORCHESTRATION)")
	case cfg.EnableEventLog && !cfg.EnableEventStream:
		return checks, errors.New("ENABLE_EVENT_LOG requires ENABLE_EVENT_STREAM")
	case cfg.EnableStructuralSweep && !cfg.EnableRelations:
		return checks, errors.New("ENABLE_STRUCTURAL_SWEEP requires ENABLE_RELATIONS")
	case cfg.EnableEvalQueueDrain && !cfg.governanceRecords():
		return checks, errors.New("ENABLE_EVAL_QUEUE_DRAIN requires ENABLE_GOVERNANCE_RECORDS (or ENABLE_ORCHESTRATION)")
	case cfg.EnableStandingDriftCheck && !cfg.governanceRecords():
		return checks, errors.New("ENABLE_STANDING_DRIFT_CHECK requires ENABLE_GOVERNANCE_RECORDS (or ENABLE_ORCHESTRATION)")
	case cfg.EnableMedia && cfg.MediaBackend != "local":
		return checks, fmt.Errorf("unsupported MEDIA_STORE_BACKEND %q (only \"local\" is supported)", cfg.MediaBackend)
	}
	for _, s := range []struct {
		on       bool
		schedule string
		name     string
	}{
		{cfg.EnableStructuralSweep, scheduleOr(cfg.StructuralSweepSchedule, defaultStructuralSweepSchedule), "structural sweep scheduler"},
		{cfg.EnableEvalQueueDrain, scheduleOr(cfg.EvalQueueDrainSchedule, defaultEvalQueueDrainSchedule), "eval queue drain scheduler"},
		{cfg.EnableStandingDriftCheck, scheduleOr(cfg.StandingDriftSchedule, defaultStandingDriftSchedule), "standing drift check scheduler"},
	} {
		if s.on {
			if err := checkSchedule(s.schedule); err != nil {
				return checks, fmt.Errorf("%s: %w", s.name, err)
			}
		}
	}
	if cfg.EnableGitHub {
		if checks.gitHubKey, err = validateGitHub(cfg); err != nil {
			return checks, err
		}
	}
	return checks, nil
}

// scheduleOr returns schedule, or def when it is empty.
func scheduleOr(schedule, def string) string {
	if schedule == "" {
		return def
	}
	return schedule
}

// checkSchedule parses schedule with the scheduler buildApp starts later,
// then stops it: the parse is the scheduler's own, and nothing keeps running.
func checkSchedule(schedule string) error {
	s, err := agent.NewSweepScheduler(schedule, "UTC", func(context.Context) (int, int, int, error) { return 0, 0, 0, nil })
	if err != nil {
		return err
	}
	s.Stop()
	return nil
}

// validateGitHub checks ENABLE_GITHUB's configuration: the flags it needs,
// the variables, the key (by a check build of the adapter) and, when the
// server registers its own core webhook, the webhook's URL. It returns the
// key. The adapter's token is state, not configuration, and is checked
// against the database by checkGitHubToken and checkGitHubGrant.
func validateGitHub(cfg ServerConfig) ([]byte, error) {
	if !cfg.EnableTokens || !cfg.EnableGovernance || !cfg.EnableRelations || !cfg.EnableWebhooks || !cfg.governanceRecords() {
		return nil, errors.New("ENABLE_GITHUB requires ENABLE_TOKENS, ENABLE_GOVERNANCE, ENABLE_RELATIONS, ENABLE_WEBHOOKS and ENABLE_GOVERNANCE_RECORDS (or ENABLE_ORCHESTRATION)")
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
		return nil, fmt.Errorf("ENABLE_GITHUB: not set: %s", strings.Join(missing, ", "))
	}
	key, err := gitHubPrivateKey(g)
	if err != nil {
		return nil, err
	}
	if err := checkGitHubAdapterConfig(cfg, key); err != nil {
		return nil, err
	}
	if g.CoreWebhookSecret == "" {
		if err := smeldr.ValidateWebhookURL(gitHubCoreEventsURL(cfg)); err != nil {
			return nil, fmt.Errorf("ENABLE_GITHUB: the core webhook for %s cannot be created (BASE_URL must be a public https URL): %w", gitHubCoreEvent, err)
		}
	}
	return key, nil
}

// configError names the variable behind a core config error: BASE_URL,
// SECRET or SMELDR_CONFIG. It never contains SECRET's value; a BASE_URL
// that does not parse is named with its value, as core's message does.
func configError(err error) error {
	msg := err.Error()
	for field, variable := range map[string]string{"base_url": "BASE_URL", "secret": "SECRET", "smeldr_config": "SMELDR_CONFIG"} {
		if strings.Contains(msg, "validation failed: "+field+": ") {
			return fmt.Errorf("%s: %s", variable, strings.TrimPrefix(msg, "validation failed: "+field+": "))
		}
	}
	return err
}

// recordsFlag names the flag that turned governance records on.
func recordsFlag(cfg ServerConfig) string {
	if cfg.EnableOrchestration && !cfg.EnableGovernanceRecords {
		return "ENABLE_ORCHESTRATION"
	}
	return "ENABLE_GOVERNANCE_RECORDS"
}
