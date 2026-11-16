package main

import (
	"context"
	"log/slog"
	"net/url"
	"strings"

	smeldr "smeldr.dev/core"
	"smeldr.dev/oauth"
)

// oauthSubject is the subject an OAuth sign-in or refresh stands for: the
// stored token tokenID, as TokenStore.ActingUser reads it now (core T1287,
// D195). A token that is unknown, revoked or expired, or a store that
// cannot be read, gives no subject; a read failure is logged, never a pass.
func oauthSubject(ts *smeldr.TokenStore, tokenID string) (oauth.Subject, bool) {
	u, exp, ok, err := ts.ActingUser(context.Background(), tokenID)
	if err != nil {
		slog.Error("oauth: the token behind a sign-in cannot be read", "error", err)
		return oauth.Subject{}, false
	}
	if !ok {
		return oauth.Subject{}, false
	}
	return oauth.Subject{UserID: u.ID, TokenID: tokenID, ExpiresAt: exp}, true
}

// origin is the scheme and host of a URL, lowercased, or the value itself
// when it does not parse.
func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// redactURL is raw with any password replaced, for a log line.
func redactURL(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Redacted()
	}
	return raw
}
