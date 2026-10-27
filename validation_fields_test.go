package smeldr

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// TestValidationError_Fields (T972): the fields come back in order, as a
// copy, and a nil receiver returns nil.
func TestValidationError_Fields(t *testing.T) {
	one := Err("title", "required")
	if got := one.Fields(); !reflect.DeepEqual(got, []FieldError{{Field: "title", Message: "required"}}) {
		t.Errorf("one: %v", got)
	}
	var ve *ValidationError
	if !errors.As(Require(Err("title", "required"), nil, Err("body", "too short")), &ve) {
		t.Fatal("Require did not return a ValidationError")
	}
	want := []FieldError{{Field: "title", Message: "required"}, {Field: "body", Message: "too short"}}
	if got := ve.Fields(); !reflect.DeepEqual(got, want) {
		t.Errorf("several: %v", got)
	}
	before := ve.Error()
	got := ve.Fields()
	got[0].Message = "changed"
	if ve.Error() != before || ve.Fields()[0].Message != "required" {
		t.Error("changing the returned slice changed the error")
	}
	var nilVE *ValidationError
	if f := nilVE.Fields(); f != nil {
		t.Errorf("nil receiver: %v", f)
	}
	wrapped := fmt.Errorf("config: %w", Err("base_url", "is required"))
	if errors.As(wrapped, &ve); ve.Fields()[0].Field != "base_url" {
		t.Errorf("wrapped: %v", ve.Fields())
	}
}

// TestWriteError_ValidationBodyUnchanged (T972): respond writes the fields
// through FieldError; the JSON body is byte for byte what it was.
func TestWriteError_ValidationBodyUnchanged(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), Require(Err("title", "required"), Err("body", "too short")))
	want := `{"error":{"code":"validation_failed","message":"Validation failed","request_id":"","fields":[{"field":"title","message":"required"},{"field":"body","message":"too short"}]}}` + "\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body:\n got %s\nwant %s", got, want)
	}
}
