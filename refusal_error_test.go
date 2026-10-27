package smeldr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRefusalError (T971): a refusal with a reason is still a forbidden for
// every errors.Is check, and is a smeldr.Error whose Public is the reason.
func TestRefusalError(t *testing.T) {
	const reason = "an agent token never ratifies"
	err := Refused(reason)
	wrapped := fmt.Errorf("Grant: %w", err)
	for _, e := range []error{err, wrapped} {
		if !errors.Is(e, ErrForbidden) {
			t.Errorf("%v: not ErrForbidden", e)
		}
		var re *RefusalError
		if !errors.As(e, &re) || re.Reason != reason {
			t.Errorf("%v: As = %v", e, re)
		}
		var fe Error
		if !errors.As(e, &fe) || fe.Public() != reason || fe.Code() != "forbidden" || fe.HTTPStatus() != http.StatusForbidden {
			t.Errorf("%v: as Error = %v", e, fe)
		}
	}
	if err.Error() != "forbidden: "+reason {
		t.Errorf("Error() = %q", err.Error())
	}
	if errors.Is(err, ErrConflict) {
		t.Error("a refusal is a conflict")
	}
}

// TestWriteError_Refusal (T971): over HTTP a wrapped refusal is a 403 whose
// message is the reason, where a plain forbidden stays "Forbidden".
func TestWriteError_Refusal(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("Grant: %w", Refused("no approve-change over Area are-1")), "no approve-change over Area are-1"},
		{fmt.Errorf("%w: names an item", ErrForbidden), "Forbidden"},
	} {
		rec := httptest.NewRecorder()
		WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), tc.err)
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusForbidden || body.Error.Code != "forbidden" || body.Error.Message != tc.want {
			t.Errorf("%v: %d %+v, want 403 forbidden %q", tc.err, rec.Code, body.Error, tc.want)
		}
	}
}
