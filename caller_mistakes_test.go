package smeldr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// T1051: caller mistakes that answered 500 are typed 422 refusals on their
// field, with the same words.

func TestCallerMistakes_Typed(t *testing.T) {
	ctx := context.Background()
	rs := NewRoleStore(setupGovernanceDB(t))
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: newSQLiteDB(t)})
	rels := setupRelationStore(t)
	_, grantErr := rs.Grant(ctx, RoleGrant{RoleName: "author"})
	_, grantRoleErr := rs.Grant(ctx, RoleGrant{TokenID: "t1"})
	_, revokeErr := rs.RevokeWithReasonAt(ctx, "", "gone", nil)
	_, defineTypeErr := app.DefineContentType(ctx, &ContentTypeSchema{})

	tests := []struct {
		name   string
		err    error
		typ    ProblemType
		field  string
		msg    string
		values map[string]string
	}{
		{"DefineRole name", rs.DefineRole(ctx, RoleDefinition{}), problemFieldRequired, "name", "role.Name must not be empty", nil},
		{"DefineRole trust_level 1", rs.DefineRole(ctx, RoleDefinition{Name: "r", TrustLevel: 1}), problemFieldNotInList, "trust_level",
			"trust_level 1 is not defined: use 0 (direct) or 2 (plan required)", map[string]string{"value": "1", "allowed": "0, 2"}},
		{"DefineRole trust_level 3", rs.DefineRole(ctx, RoleDefinition{Name: "r", TrustLevel: 3}), problemFieldNotInList, "trust_level",
			"trust_level 3 is not defined: use 0 (direct) or 2 (plan required)", map[string]string{"value": "3", "allowed": "0, 2"}},
		{"Grant token_id", grantErr, problemFieldRequired, "token_id", "TokenID must not be empty", nil},
		{"Grant role_name", grantRoleErr, problemFieldRequired, "role_name", "RoleName must not be empty", nil},
		{"Revoke grant_id", revokeErr, problemFieldRequired, "grant_id", "grantID must not be empty", nil},
		{"DefineContentType type_name", defineTypeErr, problemFieldRequired, "type_name", "TypeName is required", nil},
		{"schema type_name", ValidateSchemaDef(&ContentTypeSchema{}), problemFieldRequired, "type_name", "schema TypeName is required", nil},
		{"schema url_prefix", ValidateSchemaDef(&ContentTypeSchema{TypeName: "x", URLPrefix: "x"}), problemFieldInvalidFormat, "url_prefix",
			`URLPrefix must start with "/", got "x"`, map[string]string{"value": "x"}},
		{"schema field name", ValidateSchemaDef(&ContentTypeSchema{TypeName: "x", Fields: []byte(`[{"type":"string"}]`)}), problemSchemaFieldInvalid, "fields",
			"schema field Name is required", nil},
		{"RegisterFlow name", app.RegisterFlow(StateFlow{}), problemFieldRequired, "name", "StateFlow.Name is required", nil},
		{"RegisterFlow type_name", app.RegisterFlow(StateFlow{Name: "f"}), problemFieldRequired, "type_name", "StateFlow.TypeName is required", nil},
		{"RegisterReferenceType", rels.RegisterReferenceType(ctx, ""), problemFieldRequired, "type_name", "typeName must not be empty", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProblemTypeOf(tc.err); got != tc.typ {
				t.Fatalf("type %q (%v); want %q", got, tc.err, tc.typ)
			}
			if got := ProblemValuesOf(tc.err); !reflect.DeepEqual(got, tc.values) {
				t.Errorf("values %v; want %v", got, tc.values)
			}
			var ve *ValidationError
			if !errors.As(tc.err, &ve) || len(ve.Fields()) != 1 || ve.Fields()[0].Field != tc.field || ve.Fields()[0].Message != tc.msg {
				t.Errorf("err %v; want a ValidationError on %s: %s", tc.err, tc.field, tc.msg)
			}
			w := httptest.NewRecorder()
			WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), tc.err)
			if w.Code != http.StatusUnprocessableEntity {
				t.Errorf("status %d; want 422 (it answered 500 before T1051)", w.Code)
			}
		})
	}
	// 0 and 2 stay valid.
	for _, level := range []int{0, 2} {
		if err := rs.DefineRole(ctx, RoleDefinition{Name: "ok-role", TrustLevel: level}); err != nil {
			t.Errorf("trust_level %d: %v", level, err)
		}
	}
}
