package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// revRequiredTypes are the governance types whose updates and transitions must
// name the rev the caller read (T1010): without it, two writers overwrite each
// other unseen, and a person can ratify a body that changed after they read it.
// Read-only after init.
var revRequiredTypes = map[string]bool{
	"Decision": true, "Amendment": true, "Organization": true,
	"Domain": true, "Area": true, "CoverageRule": true,
}

// RevConflictError is a write that named a rev the record no longer has: it
// changed since the caller read it (T1010). errors.Is(err, [ErrRevConflict])
// holds, and its message, safe to show (the caller can read the record), says
// what to do: read it again, apply the change again only if it still holds,
// otherwise escalate. A 409 over HTTP, -32002 over MCP.
type RevConflictError struct {
	Type    string // the record's type, e.g. "Decision"
	ID      string // the record as the caller named it
	Sent    int    // the rev the caller sent
	Current int    // the rev the record has now
	// Ratify is set when the write was a ratification, which must name the
	// version the person read.
	Ratify bool
}

// Error returns the guidance.
func (e *RevConflictError) Error() string {
	msg := fmt.Sprintf("%s %s changed since you read it (you sent rev %d, it is now rev %d). Read it again. "+
		"Apply your change again only if it still holds against the new version; if it does not, send a Signal to the record's owner. "+
		"Never resend the same change unread.", e.Type, e.ID, e.Sent, e.Current)
	if e.Ratify {
		msg += " A ratification must name the version the person read: show them what changed before they ratify again."
	}
	return msg
}

// Code returns "rev_conflict", the code of [ErrRevConflict].
func (e *RevConflictError) Code() string { return "rev_conflict" }

// HTTPStatus returns 409.
func (e *RevConflictError) HTTPStatus() int { return http.StatusConflict }

// Public returns the guidance: it names only the caller's record and revs.
func (e *RevConflictError) Public() string { return e.Error() }

// Is reports whether target is [ErrRevConflict].
func (e *RevConflictError) Is(target error) bool { return target == ErrRevConflict }

// revMissing applies the rev rule to a write of typeName's item id that named
// no rev (T1010): nothing for a type outside [revRequiredTypes]; before
// cutover (or with a zero cutover) a rev_missing warning; after it, a
// [ValidationError] on rev. write names the call, e.g. "update_decision".
func revMissing(typeName, id, write string, cutover time.Time) (*CreateWarning, error) {
	if !revRequiredTypes[typeName] {
		return nil, nil
	}
	return revMissingFor(id, write, cutover)
}

// revMissingFor is the rev rule for a write that always names a rev (a
// governance relation, a change check answer, a premise confirmation): a
// rev_missing warning, or a [ValidationError] on rev once cutover has passed.
func revMissingFor(id, write string, cutover time.Time) (*CreateWarning, error) {
	msg := fmt.Sprintf("%s %s: send the rev you read, so a change made since is not overwritten unseen", write, id)
	if !cutover.IsZero() && !time.Now().Before(cutover) {
		return nil, Err("rev", msg+"; it is required")
	}
	if !cutover.IsZero() {
		msg += "; it is refused without one from " + cutover.UTC().Format(time.RFC3339)
	}
	return &CreateWarning{Code: "rev_missing", Field: "rev", Message: msg}, nil
}

// sentRev returns the rev a partial update names, matched the way
// encoding/json matches the Rev field (any case), and whether it names one. A
// rev that is not a number is a [ValidationError] on rev, never read as 0.
func sentRev(fields map[string]any) (int, bool, error) {
	for k, v := range fields {
		if !strings.EqualFold(k, "rev") {
			continue
		}
		switch n := v.(type) {
		case float64:
			return int(n), true, nil
		case int:
			return n, true, nil
		case int64:
			return int(n), true, nil
		}
		return 0, true, Err("rev", "must be a number: the rev you read")
	}
	return 0, false, nil
}

// bumpRev advances the rev of the row id in table by one, and when expected is
// set only while the row still has that rev; it returns the new rev. A table
// with no rev column returns hasRev false and changes nothing. With expected
// set, a row that has moved on returns a [*RevConflictError] with its current
// rev (T1010).
func bumpRev(ctx context.Context, exec DB, table, typeName, id string, expected *int) (newRev int, hasRev bool, err error) {
	q := "UPDATE " + quoteIdent(table) + " SET rev = rev + 1 WHERE id = $1"
	args := []any{id}
	if expected != nil {
		q += " AND rev = $2"
		args = append(args, *expected)
	}
	err = exec.QueryRowContext(ctx, q+" RETURNING rev", args...).Scan(&newRev)
	switch {
	case err == nil:
		return newRev, true, nil
	case isNoSuchColumn(err, "rev"):
		return 0, false, nil
	case errors.Is(err, sql.ErrNoRows) && expected != nil:
		var current int
		if cerr := exec.QueryRowContext(ctx, "SELECT rev FROM "+quoteIdent(table)+" WHERE id = $1", id).Scan(&current); cerr != nil {
			return 0, true, fmt.Errorf("%w: rev check: %s", ErrInternal, cerr)
		}
		return 0, true, &RevConflictError{Type: typeName, ID: id, Sent: *expected, Current: current}
	case errors.Is(err, sql.ErrNoRows):
		return 0, true, nil
	default:
		return 0, true, fmt.Errorf("%w: rev update: %s", ErrInternal, err)
	}
}
