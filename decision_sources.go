package smeldr

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The roles a [DecisionSource] plays for its Decision (D148).
const (
	// DecisionSourceRoleSource is the text the claim is drawn from: a
	// readme, a token file, a specification, an ADR.
	DecisionSourceRoleSource = "source"
	// DecisionSourceRoleEvidence is material that supports the claim.
	DecisionSourceRoleEvidence = "evidence"
	// DecisionSourceRoleImplementation is code that carries the claim out.
	DecisionSourceRoleImplementation = "implementation"
)

// DecisionSourcesMax is the most sources one Decision carries (D148).
const DecisionSourcesMax = 20

// decisionSourceAnchorMax is the longest anchor a source may have.
const decisionSourceAnchorMax = 200

var (
	decisionSourceCommitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
	decisionSourceHashRe   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	decisionSourceRoles    = []string{DecisionSourceRoleSource, DecisionSourceRoleEvidence, DecisionSourceRoleImplementation}
)

// DecisionSource is one pinned external source of a [Decision] (D148): a file
// at a commit, never a branch. The Decision carries the claim in its own
// text; the source says where it came from. Core stores the pin and never
// fetches it: the reader fetches the file at the commit and may compare the
// hash.
type DecisionSource struct {
	// Repository is owner/name, stored in lower case.
	Repository string `json:"repository"`
	// Commit is the full 40-character commit SHA, stored in lower case.
	Commit string `json:"commit"`
	// Path is one file in the repository, relative to its root.
	Path string `json:"path"`
	// Anchor optionally points into the file: a heading or a line range.
	Anchor string `json:"anchor,omitempty"`
	// Role is what the source is for the claim: source, evidence or
	// implementation.
	Role string `json:"role" smeldr_enum:"source|evidence|implementation"`
	// Hash is optionally sha256:<64 hex> of the file's content at the commit.
	Hash string `json:"hash,omitempty"`
}

// DecisionSources is a Decision's list of pinned sources, stored as a JSON
// array. A nil list reads and writes as [].
type DecisionSources []DecisionSource

// Value stores the list as a JSON array.
func (s DecisionSources) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]DecisionSource(s))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan reads the list from its JSON array; NULL or empty is no sources.
func (s *DecisionSources) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case nil:
		*s = DecisionSources{}
		return nil
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		return fmt.Errorf("smeldr: cannot scan %T into DecisionSources", src)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		*s = DecisionSources{}
		return nil
	}
	var out []DecisionSource
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("smeldr: DecisionSources: %w", err)
	}
	if out == nil {
		out = []DecisionSource{}
	}
	*s = out
	return nil
}

// MarshalJSON writes a nil list as [], never null.
func (s DecisionSources) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]DecisionSource(s))
}

// normalizeDecisionSources checks each source and stores it in its canonical
// form: repository and commit in lower case, the rest trimmed (D148). At most
// [DecisionSourcesMax]; the same repository, commit, path and anchor twice is
// refused.
func normalizeDecisionSources(sources DecisionSources) (DecisionSources, error) {
	if len(sources) > DecisionSourcesMax {
		return nil, Err("sources", fmt.Sprintf("has %d sources; a Decision carries at most %d", len(sources), DecisionSourcesMax))
	}
	out := make(DecisionSources, 0, len(sources))
	seen := map[[4]string]int{}
	for i, src := range sources {
		field := func(name string) string { return fmt.Sprintf("sources[%d].%s", i, name) }
		repo, err := normalizeRepository(src.Repository)
		if err != nil {
			return nil, Err(field("repository"), fmt.Sprintf("%q is not owner/name, for example acme/billing", strings.TrimSpace(src.Repository)))
		}
		src.Repository = repo
		src.Commit = strings.ToLower(strings.TrimSpace(src.Commit))
		if !decisionSourceCommitRe.MatchString(src.Commit) {
			return nil, Err(field("commit"), "must be a full 40-character commit SHA, never a branch, a tag or a short SHA")
		}
		src.Path = strings.TrimSpace(src.Path)
		if err := checkRepositoryPath(field("path"), "a path", "docs/adr/0007-money.md", src.Path, false); err != nil {
			return nil, err
		}
		src.Anchor = strings.TrimSpace(src.Anchor)
		switch {
		case len(src.Anchor) > decisionSourceAnchorMax:
			return nil, Err(field("anchor"), fmt.Sprintf("is longer than %d characters", decisionSourceAnchorMax))
		case strings.ContainsAny(src.Anchor, "\r\n"):
			return nil, Err(field("anchor"), "must be one line: a heading or a line range")
		}
		src.Role = strings.TrimSpace(src.Role)
		if !slices.Contains(decisionSourceRoles, src.Role) {
			return nil, Err(field("role"), "must be source, evidence or implementation")
		}
		src.Hash = strings.ToLower(strings.TrimSpace(src.Hash))
		if src.Hash != "" && !decisionSourceHashRe.MatchString(src.Hash) {
			return nil, Err(field("hash"), "must be sha256: followed by 64 hex characters, the SHA-256 of the file at the commit")
		}
		key := [4]string{src.Repository, src.Commit, src.Path, src.Anchor}
		if first, dup := seen[key]; dup {
			return nil, Err(field("path"), fmt.Sprintf("is the same source as sources[%d]: the same repository, commit, path and anchor", first))
		}
		seen[key] = i
		out = append(out, src)
	}
	return out, nil
}

// prepareDecision normalizes a Decision's sources and, on an update of a
// Decision that is no longer proposed, requires ratify over its place for a
// change to them (D148): a new or changed pin changes what the claim points
// to. A non-proposed Decision with no place is refused, fail closed.
func (app *App) prepareDecision(ctx Context, existing any, d *Decision) error {
	sources, err := normalizeDecisionSources(d.Sources)
	if err != nil {
		return err
	}
	d.Sources = sources
	old, ok := existing.(*Decision)
	if !ok || old.Status == "proposed" || slices.Equal(old.Sources, d.Sources) {
		return nil
	}
	if len(old.Sources) == 0 && len(d.Sources) == 0 {
		return nil
	}
	if app.governance == nil {
		return nil
	}
	db := app.cfg.DB
	label := recordLabel(ctx, db, "smeldr_decisions", old.ID)
	p, found, err := decisionPlace(ctx, db, old.ID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: changing %s's sources needs ratify over its place, and it has none (D148)", ErrForbidden, label)
	}
	place := recordLabel(ctx, db, placeTables[p.targetType], p.targetID)
	return requireOperationOver(ctx, app.governance, OperationRatify, AuthTarget{TypeName: p.targetType, ID: p.targetID},
		fmt.Sprintf("changing %s's sources", label), place, "D148")
}
