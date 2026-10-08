// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

// listingProvenanceStore keeps what is appended and lists it back; it has no
// batched read, so it exercises latestBySubject's per-id path.
type listingProvenanceStore struct {
	recs    []ProvenanceRecord
	listErr error
}

func (s *listingProvenanceStore) Append(_ context.Context, r ProvenanceRecord) error {
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now().UTC()
	}
	s.recs = append(s.recs, r)
	return nil
}

func (s *listingProvenanceStore) List(_ context.Context, f ProvenanceFilter) ([]ProvenanceRecord, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	var out []ProvenanceRecord
	for _, r := range s.recs {
		if r.SubjectType == f.SubjectType && r.SubjectID == f.SubjectID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *listingProvenanceStore) of(subjectType, verb string) []ProvenanceRecord {
	var out []ProvenanceRecord
	for _, r := range s.recs {
		if r.SubjectType == subjectType && r.Verb == verb {
			out = append(out, r)
		}
	}
	return out
}

func fingerprint(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func reasonTokens(t *testing.T) (*TokenStore, *listingProvenanceStore, *sql.DB) {
	t.Helper()
	db := newTestTokensDB(t)
	if err := EnsureTokenUserIDColumn(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTokenActorClassColumn(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	ts := NewTokenStore(db, "test-secret-32-bytes-xxxxxxxxxxxx")
	prov := &listingProvenanceStore{}
	ts.setProvenanceStore(prov)
	return ts, prov, db
}

// Every mint writes a Token/assert record whose subject is the fingerprint,
// never the raw token, with the caller as actor, or the mechanism as a job.
func TestMintRecordsProvenance(t *testing.T) {
	admin := NewTestContext(User{ID: "admin-1", Roles: []Role{Admin, Human}})
	cases := []struct {
		name       string
		mint       func(ts *TokenStore) (string, error)
		wantKind   string
		wantActor  string
		wantReason string
	}{
		{"Create, no caller", func(ts *TokenStore) (string, error) {
			return ts.Create(context.Background(), "a", "author", time.Hour)
		}, "job", mintActorStore, ""},
		{"CreateWithID, caller", func(ts *TokenStore) (string, error) {
			raw, _, err := ts.CreateWithID(admin, "b", "editor", time.Hour)
			return raw, err
		}, "human", "admin-1", ""},
		{"CreateClassified, caller", func(ts *TokenStore) (string, error) {
			raw, _, err := ts.CreateClassified(admin, "c", "editor", Agent, time.Hour)
			return raw, err
		}, "human", "admin-1", ""},
		{"CreateClassifiedWithReason, caller", func(ts *TokenStore) (string, error) {
			raw, _, err := ts.CreateClassifiedWithReason(admin, "d", "editor", Job, time.Hour, "  nightly import  ")
			return raw, err
		}, "human", "admin-1", "nightly import"},
		{"bootstrap", func(ts *TokenStore) (string, error) {
			uid, created := ts.ensureBootstrap(context.Background())
			if !created || uid == "" {
				return "", errors.New("bootstrap not created")
			}
			return "", nil
		}, "job", mintActorBootstrap, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts, prov, _ := reasonTokens(t)
			raw, err := c.mint(ts)
			if err != nil {
				t.Fatal(err)
			}
			recs := prov.of("Token", "assert")
			if len(recs) != 1 {
				t.Fatalf("mint records = %d; want 1", len(recs))
			}
			r := recs[0]
			if r.ActorKind != c.wantKind || r.ActorID != c.wantActor || r.Reason != c.wantReason {
				t.Errorf("record = %s %q reason %q; want %s %q reason %q", r.ActorKind, r.ActorID, r.Reason, c.wantKind, c.wantActor, c.wantReason)
			}
			if raw != "" {
				if r.SubjectID != fingerprint(raw) {
					t.Errorf("subject = %q; want the fingerprint", r.SubjectID)
				}
				if strings.Contains(r.SubjectID, raw) || strings.Contains(r.Reason, raw) {
					t.Error("the raw token appears in the record")
				}
			}
		})
	}
}

// A revocation carries its reason; Revoke records an empty one.
func TestTokenRevokeWithReason(t *testing.T) {
	ts, prov, _ := reasonTokens(t)
	admin := NewTestContext(User{ID: "admin-1", Roles: []Role{Admin}})
	raw, err := ts.Create(admin, "a", "author", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	raw2, _ := ts.Create(admin, "b", "author", time.Hour)
	if err := ts.RevokeWithReason(admin, fingerprint(raw), "left the project"); err != nil {
		t.Fatal(err)
	}
	if err := ts.Revoke(admin, fingerprint(raw2)); err != nil {
		t.Fatal(err)
	}
	got := prov.of("Token", "invalidate")
	if len(got) != 2 || got[0].Reason != "left the project" || got[1].Reason != "" || got[0].ActorID != "admin-1" {
		t.Errorf("revoke records = %+v; want the reason, then none, by admin-1", got)
	}
}

// Grant and RevokeWithReason carry their reasons on the RoleGrant records.
func TestGrantAndRevokeReasons(t *testing.T) {
	db := setupGovernanceDB(t)
	rs := NewRoleStore(db)
	prov := &listingProvenanceStore{}
	rs.setProvenanceStore(prov)
	ctx := NewTestContext(User{ID: "admin-1", Roles: []Role{Admin}})
	if err := rs.DefineRole(ctx, RoleDefinition{Name: "r", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	id, err := rs.Grant(ctx, RoleGrant{TokenID: "tok-1", RoleName: "r", Reason: " on-call this week "})
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.RevokeWithReason(ctx, id, "rotation over"); err != nil {
		t.Fatal(err)
	}
	a, i := prov.of("RoleGrant", "assert"), prov.of("RoleGrant", "invalidate")
	if len(a) != 1 || a[0].Reason != "on-call this week" || len(i) != 1 || i[0].Reason != "rotation over" {
		t.Errorf("assert = %+v, invalidate = %+v; want the two reasons", a, i)
	}
}

// A reason longer than 1000 characters is refused and nothing is written.
func TestAccessReasonTooLong(t *testing.T) {
	long := strings.Repeat("x", maxActReason+1)
	ts, prov, db := reasonTokens(t)
	if _, _, err := ts.CreateClassifiedWithReason(context.Background(), "a", "author", "", time.Hour, long); !isValidation(err) {
		t.Errorf("mint err = %v; want a ValidationError", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_tokens`).Scan(&n); err != nil || n != 0 {
		t.Errorf("tokens = %d (%v); want none minted", n, err)
	}
	raw, _ := ts.Create(context.Background(), "b", "author", time.Hour)
	if err := ts.RevokeWithReason(context.Background(), fingerprint(raw), long); !isValidation(err) {
		t.Errorf("revoke err = %v; want a ValidationError", err)
	}
	if len(prov.of("Token", "invalidate")) != 0 {
		t.Error("a refused revoke wrote a record")
	}

	gdb := setupGovernanceDB(t)
	rs := NewRoleStore(gdb)
	if err := rs.DefineRole(context.Background(), RoleDefinition{Name: "r", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(context.Background(), RoleGrant{TokenID: "t", RoleName: "r", Reason: long}); !isValidation(err) {
		t.Errorf("grant err = %v; want a ValidationError", err)
	}
	if gs, _ := rs.ListGrants(context.Background(), "t"); len(gs) != 0 {
		t.Errorf("grants = %+v; want none", gs)
	}
	if err := rs.RevokeWithReason(context.Background(), "any", long); !isValidation(err) {
		t.Errorf("grant revoke err = %v; want a ValidationError", err)
	}
	if got, err := actReason(strings.Repeat("æ", maxActReason)); err != nil || got == "" {
		t.Errorf("1000 multi-byte characters = %v; want accepted", err)
	}
}

func isValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// List shows mint and revoke reasons, through the batched read of the default
// store and the per-id read of another; a token without records shows none.
func TestTokenListReasons(t *testing.T) {
	for _, batched := range []bool{true, false} {
		name := "per id"
		if batched {
			name = "batched"
		}
		t.Run(name, func(t *testing.T) {
			db := newTestTokensDB(t)
			ts := NewTokenStore(db, "test-secret-32-bytes-xxxxxxxxxxxx")
			old, _ := ts.Create(context.Background(), "old", "author", time.Hour) // before provenance: no record
			if batched {
				if err := CreateProvenanceTable(db); err != nil {
					t.Fatal(err)
				}
				ts.setProvenanceStore(NewProvenanceStore(db))
			} else {
				ts.setProvenanceStore(&listingProvenanceStore{})
			}
			raw, _, _ := ts.CreateClassifiedWithReason(context.Background(), "new", "author", "", time.Hour, "for the import")
			if err := ts.RevokeWithReason(context.Background(), fingerprint(raw), "import done"); err != nil {
				t.Fatal(err)
			}
			recs, err := ts.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range recs {
				switch r.ID {
				case fingerprint(raw):
					if r.Reason != "for the import" || r.RevokeReason != "import done" {
						t.Errorf("new token reasons = %q, %q", r.Reason, r.RevokeReason)
					}
				case fingerprint(old):
					if r.Reason != "" || r.RevokeReason != "" {
						t.Errorf("old token reasons = %q, %q; want none", r.Reason, r.RevokeReason)
					}
				}
			}
		})
	}
}

// A failed reason read leaves the reasons empty and still returns the list.
func TestTokenListReasons_ReadFailureIsFailOpen(t *testing.T) {
	ts, prov, _ := reasonTokens(t)
	if _, _, err := ts.CreateClassifiedWithReason(context.Background(), "a", "author", "", time.Hour, "r"); err != nil {
		t.Fatal(err)
	}
	prov.listErr = errors.New("down")
	recs, err := ts.List(context.Background())
	if err != nil || len(recs) != 1 || recs[0].Reason != "" {
		t.Errorf("List = %+v, %v; want the token, no reason", recs, err)
	}
}

// ListGrants shows a grant's reason, and fails open on a read error.
func TestListGrantsReason(t *testing.T) {
	db := setupGovernanceDB(t)
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	rs := NewRoleStore(db)
	rs.setProvenanceStore(NewProvenanceStore(db))
	ctx := context.Background()
	if err := rs.DefineRole(ctx, RoleDefinition{Name: "r", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "t", RoleName: "r", Reason: "steward of docs"}); err != nil {
		t.Fatal(err)
	}
	gs, err := rs.ListGrants(ctx, "t")
	if err != nil || len(gs) != 1 || gs[0].Reason != "steward of docs" {
		t.Fatalf("ListGrants = %+v, %v; want the reason", gs, err)
	}
	rs.setProvenanceStore(&listingProvenanceStore{listErr: errors.New("down")})
	gs, err = rs.ListGrants(ctx, "t")
	if err != nil || len(gs) != 1 || gs[0].Reason != "" {
		t.Errorf("ListGrants on a failed read = %+v, %v; want the grant, no reason", gs, err)
	}
}

// failBatchedStore has a batched read that fails.
type failBatchedStore struct{ listingProvenanceStore }

func (failBatchedStore) listBySubjects(context.Context, string, string, []string) ([]ProvenanceRecord, error) {
	return nil, errors.New("down")
}

func TestLatestBySubject_EdgeCases(t *testing.T) {
	ctx := context.Background()
	if m, err := latestBySubject(ctx, nil, "Token", "assert", []string{"a"}); err != nil || len(m) != 0 {
		t.Errorf("nil store = %v, %v", m, err)
	}
	if m, err := latestBySubject(ctx, &listingProvenanceStore{}, "Token", "assert", nil); err != nil || len(m) != 0 {
		t.Errorf("no ids = %v, %v", m, err)
	}
	if _, err := latestBySubject(ctx, &failBatchedStore{}, "Token", "assert", []string{"a"}); err == nil {
		t.Error("batched read error = nil; want it returned")
	}
	s := &listingProvenanceStore{}
	early, late := time.Now().UTC().Add(-time.Minute), time.Now().UTC()
	_ = s.Append(ctx, ProvenanceRecord{SubjectType: "Token", SubjectID: "a", Verb: "assert", Reason: "late", Timestamp: late})
	_ = s.Append(ctx, ProvenanceRecord{SubjectType: "Token", SubjectID: "a", Verb: "assert", Reason: "early", Timestamp: early})
	_ = s.Append(ctx, ProvenanceRecord{SubjectType: "Token", SubjectID: "a", Verb: "invalidate", Reason: "other verb", Timestamp: late.Add(time.Second)})
	m, err := latestBySubject(ctx, s, "Token", "assert", []string{"a"})
	if err != nil || m["a"].Reason != "late" {
		t.Errorf("latest = %+v, %v; want the later assert", m["a"], err)
	}
}

// secondListFails lets the first List call through and fails the rest.
type secondListFails struct {
	listingProvenanceStore
	calls int
}

func (s *secondListFails) List(ctx context.Context, f ProvenanceFilter) ([]ProvenanceRecord, error) {
	s.calls++
	if s.calls > 1 {
		return nil, errors.New("down")
	}
	return s.listingProvenanceStore.List(ctx, f)
}

// A failure on the revoke-reason read is fail-open too.
func TestTokenListReasons_RevokeReadFailure(t *testing.T) {
	ts, _, _ := reasonTokens(t)
	store := &secondListFails{}
	ts.setProvenanceStore(store)
	if _, err := ts.Create(context.Background(), "a", "author", time.Hour); err != nil {
		t.Fatal(err)
	}
	recs, err := ts.List(context.Background())
	if err != nil || len(recs) != 1 || recs[0].Reason != "" {
		t.Errorf("List = %+v, %v; want the token, no reasons", recs, err)
	}
}
