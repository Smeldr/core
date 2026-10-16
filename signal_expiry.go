package smeldr

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultSignalExpiryMaxAge was the age after which the retired expiry sweep
// moved a pending or read Signal to expired. Kept for the API (D97); nothing
// reads it since D115.
const DefaultSignalExpiryMaxAge = 14 * 24 * time.Hour

// DefaultSignalExpiryBatchCap was the retired expiry sweep's per-run cap.
// Kept for the API (D97); nothing reads it since D115.
const DefaultSignalExpiryBatchCap = 200

// DefaultSignalExpiryExcludedTypes were the types the retired expiry sweep
// never expired. Kept for the API (D97); nothing reads it since D115, when
// nothing expires.
var DefaultSignalExpiryExcludedTypes = []string{
	"authorization-required",
	"review-requested",
	"conflict-detected",
}

// SignalExpiryConfig configured one run of the retired expiry sweep. Kept for
// the API (D97): [App.ExpireSignals] ignores it.
type SignalExpiryConfig struct {
	// MaxAge is ignored.
	MaxAge time.Duration
	// ExcludeTypes is ignored.
	ExcludeTypes []string
	// BatchCap is ignored.
	BatchCap int
}

// signalExpiryRetiredLogged makes [App.ExpireSignals] say once per process
// that the sweep is retired.
var signalExpiryRetiredLogged sync.Once

// ExpireSignals is retired (D115): nothing expires. A condition is open while
// it holds and resolved by core when it ends; a request is open until it is
// answered or withdrawn. It moves nothing, logs once per process that the
// sweep is retired, and returns zeros. It stays exported for the API (D97).
// [App.OpenSignalsOlderThan] surfaces old open Signals instead.
func (a *App) ExpireSignals(ctx context.Context, _ SignalExpiryConfig) (walked, expired, skipped int, err error) {
	signalExpiryRetiredLogged.Do(func() {
		slog.InfoContext(ctx, "smeldr: ExpireSignals is retired (D115): nothing expires; use App.OpenSignalsOlderThan to surface old open Signals")
	})
	return 0, 0, 0, nil
}
