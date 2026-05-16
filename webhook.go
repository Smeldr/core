package forge

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"
)

// Titled is an optional interface implemented by content types that can
// provide a human-readable title for webhook event payloads. When a content
// type implements Titled, the title field is included in the data object
// delivered to webhook receivers. Types that do not implement Titled omit
// the field gracefully.
type Titled interface {
	ContentTitle() string
}

// WebhookEndpoint is a persisted outbound webhook target. Create and manage
// endpoints via [WebhookStore]. The signing secret is returned once at
// creation and cannot be retrieved afterwards.
//
// Database DDL (must be executed before [App.Webhooks] is called):
//
//	CREATE TABLE forge_webhook_endpoints (
//	    id         TEXT    PRIMARY KEY,
//	    events     TEXT    NOT NULL,      -- JSON array of event names
//	    target_url TEXT    NOT NULL,
//	    secret_enc TEXT    NOT NULL,      -- AES-256-GCM encrypted, base64
//	    active     BOOLEAN NOT NULL DEFAULT TRUE,
//	    created_at TIMESTAMPTZ NOT NULL
//	);
type WebhookEndpoint struct {
	ID        string    `json:"id"`
	Events    []string  `json:"events"`
	TargetURL string    `json:"target_url"`
	secretEnc string    // never serialised — secrets are write-only
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// WebhookEventPayload is the JSON envelope delivered in every outbound
// webhook POST body. The Data field contains event-specific content information.
type WebhookEventPayload struct {
	ID        string          `json:"id"`
	Event     string          `json:"event"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// webhookEventData is the content-specific object embedded in the Data field
// of every [WebhookEventPayload].
type webhookEventData struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title,omitempty"`
}

// WebhookStore manages [WebhookEndpoint] records in the database. Signing
// secrets are stored AES-256-GCM encrypted using a key derived from the
// application secret.
//
// # Config.Secret rotation warning
//
// The AES-256 encryption key is derived from Config.Secret via SHA-256. If
// Config.Secret changes, all stored webhook signing secrets become
// unreadable and existing endpoints will fail delivery with a decryption
// error. Rotate Config.Secret only during a planned maintenance window,
// and re-create all webhook endpoints afterwards.
type WebhookStore struct {
	db     DB
	appKey [32]byte
}

// NewWebhookStore creates a [WebhookStore] backed by db. appSecret is the
// application's Config.Secret; it is hashed with SHA-256 to derive the
// 32-byte AES-256-GCM key used to encrypt stored webhook signing secrets.
func NewWebhookStore(db DB, appSecret []byte) *WebhookStore {
	key := sha256.Sum256(appSecret)
	return &WebhookStore{db: db, appKey: key}
}

// encryptSecret encrypts plaintext using AES-256-GCM. The nonce is prepended
// to the ciphertext and the combined value is base64-encoded.
func (s *WebhookStore) encryptSecret(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(s.appKey[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptSecret decrypts a base64-encoded AES-256-GCM ciphertext produced
// by encryptSecret.
func (s *WebhookStore) decryptSecret(enc string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("forge: webhook secret: base64 decode: %w", err)
	}
	block, err := aes.NewCipher(s.appKey[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, fmt.Errorf("forge: webhook secret ciphertext too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// DecryptSecret decrypts the stored signing secret for ep. Called internally
// by the worker pool at delivery time; exposed so callers with a
// [WebhookStore] reference can verify secrets during integration tests.
func (s *WebhookStore) DecryptSecret(ep WebhookEndpoint) ([]byte, error) {
	return s.decryptSecret(ep.secretEnc)
}

// Create registers a new webhook endpoint. targetURL is validated for SSRF
// safety (HTTPS required; no private or loopback IPs). A 32-byte random
// signing secret is generated, encrypted, and persisted. The plaintext secret
// is returned once — it cannot be retrieved again.
func (s *WebhookStore) Create(ctx context.Context, targetURL string, events []string) (WebhookEndpoint, string, error) {
	if err := validateWebhookURL(targetURL); err != nil {
		return WebhookEndpoint{}, "", err
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return WebhookEndpoint{}, "", err
	}
	secretPlain := base64.RawURLEncoding.EncodeToString(raw)
	enc, err := s.encryptSecret([]byte(secretPlain))
	if err != nil {
		return WebhookEndpoint{}, "", err
	}
	eventsJSON, _ := json.Marshal(events)
	ep := WebhookEndpoint{
		ID:        NewID(),
		Events:    events,
		TargetURL: targetURL,
		secretEnc: enc,
		Active:    true,
		CreatedAt: time.Now().UTC(),
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO forge_webhook_endpoints (id, events, target_url, secret_enc, active, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		ep.ID, string(eventsJSON), ep.TargetURL, ep.secretEnc, ep.Active, ep.CreatedAt,
	)
	if err != nil {
		return WebhookEndpoint{}, "", err
	}
	return ep, secretPlain, nil
}

// List returns all webhook endpoints ordered by creation time descending.
// The secretEnc field is never populated on returned values — secrets are
// write-only.
func (s *WebhookStore) List(ctx context.Context) ([]WebhookEndpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, events, target_url, active, created_at FROM forge_webhook_endpoints ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebhookEndpoint
	for rows.Next() {
		var ep WebhookEndpoint
		var eventsJSON string
		if err := rows.Scan(&ep.ID, &eventsJSON, &ep.TargetURL, &ep.Active, &ep.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(eventsJSON), &ep.Events)
		out = append(out, ep)
	}
	return out, rows.Err()
}

// Delete removes the endpoint with the given ID.
func (s *WebhookStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM forge_webhook_endpoints WHERE id = $1`, id,
	)
	return err
}

// EndpointsForEvent returns all active endpoints subscribed to the given event
// name. The secretEnc field IS populated on returned values so the worker
// pool can decrypt the signing secret at delivery time.
//
// The SQL LIKE filter pre-screens rows by checking whether the events JSON
// array contains the event string as a quoted element. Event names are
// well-controlled internal constants and never contain % or ", making the
// LIKE pattern safe. A Go-level exact match is still applied after scanning
// to handle any false positives from the substring match.
func (s *WebhookStore) EndpointsForEvent(ctx context.Context, event string) ([]WebhookEndpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, events, target_url, secret_enc, active, created_at
		 FROM forge_webhook_endpoints
		 WHERE active AND events LIKE '%"' || $1 || '"%'`,
		event,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebhookEndpoint
	for rows.Next() {
		var ep WebhookEndpoint
		var eventsJSON string
		if err := rows.Scan(&ep.ID, &eventsJSON, &ep.TargetURL, &ep.secretEnc, &ep.Active, &ep.CreatedAt); err != nil {
			return nil, err
		}
		var events []string
		_ = json.Unmarshal([]byte(eventsJSON), &events)
		ep.Events = events
		for _, e := range events {
			if e == event {
				out = append(out, ep)
				break
			}
		}
	}
	return out, rows.Err()
}

// signalToEventSuffix maps a Signal constant to its webhook event suffix.
// Returns ("", false) for signals that are not delivered as webhook events
// (BeforeCreate, BeforeUpdate, BeforeDelete, SitemapRegenerate).
func signalToEventSuffix(sig Signal) (string, bool) {
	switch sig {
	case AfterCreate:
		return "created", true
	case AfterUpdate:
		return "updated", true
	case AfterPublish:
		return "published", true
	case AfterUnpublish:
		return "unpublished", true
	case AfterArchive:
		return "archived", true
	case AfterDelete:
		return "deleted", true
	case AfterSchedule:
		return "scheduled", true
	default:
		return "", false
	}
}

// buildEventName returns the full webhook event name for typeName and sig.
// Returns ("", false) when sig does not map to a delivery event.
//
// Example: buildEventName("Post", AfterPublish) → ("post.published", true).
func buildEventName(typeName string, sig Signal) (string, bool) {
	suffix, ok := signalToEventSuffix(sig)
	if !ok {
		return "", false
	}
	return strings.ToLower(typeName) + "." + suffix, true
}

// buildWebhookPayload constructs a serialised [WebhookEventPayload] for the
// given content item and signal. The data object includes type, id, and slug
// from the embedded Node; the title field is added when item implements
// [Titled].
func buildWebhookPayload(typeName string, item any, sig Signal) ([]byte, error) {
	eventName, ok := buildEventName(typeName, sig)
	if !ok {
		return nil, fmt.Errorf("forge: signal %q is not a webhook delivery event", sig)
	}
	n := extractNode(item)
	data := webhookEventData{
		Type: strings.ToLower(typeName),
		ID:   n.ID,
		Slug: n.Slug,
	}
	if t, ok := item.(Titled); ok {
		data.Title = t.ContentTitle()
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	payload := WebhookEventPayload{
		ID:        NewID(),
		Event:     eventName,
		Timestamp: time.Now().UTC(),
		Data:      json.RawMessage(dataJSON),
	}
	return json.Marshal(payload)
}

// webhookDispatch is the [App.OnSignal] handler registered by [App.Webhooks].
// It builds the webhook event payload from ev and enqueues an [OutboundJob]
// for each active endpoint subscribed to the event. Errors during payload
// build or endpoint lookup are logged but not returned, because the bus logs
// handler errors at Warn level; returning nil avoids double-logging.
func webhookDispatch(ctx context.Context, ev SignalEvent, sig Signal, store *WebhookStore, pool *workerPool) error {
	eventName, ok := buildEventName(ev.Type, sig)
	if !ok {
		return nil
	}
	payload, err := buildWebhookPayload(ev.Type, ev.raw, sig)
	if err != nil {
		slog.WarnContext(ctx, "webhook payload build failed", "error", err, "signal", sig, "type", ev.Type)
		return nil
	}
	endpoints, err := store.EndpointsForEvent(ctx, eventName)
	if err != nil {
		slog.WarnContext(ctx, "webhook endpoints lookup failed", "error", err, "event", eventName)
		return nil
	}
	now := time.Now()
	for _, ep := range endpoints {
		job := OutboundJob{
			ID:          NewID(),
			EndpointID:  ep.ID,
			TargetURL:   ep.TargetURL,
			SecretEnc:   ep.secretEnc,
			Payload:     payload,
			Event:       eventName,
			NextRetryAt: now,
			CreatedAt:   now,
			ExpiresAt:   now.Add(24 * time.Hour),
			Status:      "pending",
		}
		if err := pool.Enqueue(ctx, job); err != nil {
			slog.WarnContext(ctx, "webhook enqueue failed", "error", err, "endpoint", ep.ID)
		}
	}
	return nil
}

// validateWebhookURL validates rawURL for SSRF safety. Returns a
// [*ValidationError] describing the rejection reason when the URL fails any
// check.
//
// Rejects:
//   - URLs with a scheme other than https
//   - Hostnames of "localhost" or with a ".local" suffix
//   - URLs whose hostname resolves to any private or loopback IP range
func validateWebhookURL(rawURL string) error {
	u, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return Err("url", "invalid URL: "+err.Error())
	}
	if u.Scheme != "https" {
		return Err("url", "must use HTTPS")
	}
	host := u.Hostname()
	if host == "" {
		return Err("url", "URL has no host")
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return Err("url", "must not target localhost or .local hostnames")
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return Err("url", "host cannot be resolved: "+err.Error())
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			return Err("url", "must not target a private IP address")
		}
	}
	return nil
}

// privateRanges holds the pre-compiled private, loopback, and link-local CIDR
// blocks checked by isPrivateIP. Compiled once at init to avoid repeated
// allocations on every validateWebhookURL call.
var privateRanges []*net.IPNet

func init() {
	for _, cidr := range []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	} {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("forge: invalid private CIDR: " + cidr)
		}
		privateRanges = append(privateRanges, block)
	}
}

// isPrivateIP reports whether ip falls within a private, loopback, or
// link-local range: 127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16,
// ::1/128, fc00::/7 (IPv6 ULA), or fe80::/10 (IPv6 link-local).
func isPrivateIP(ip net.IP) bool {
	for _, block := range privateRanges {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}
