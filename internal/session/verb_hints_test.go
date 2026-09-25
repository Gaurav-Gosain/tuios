package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// errorOf extracts the error object from a response, failing when the response
// carried a result instead.
func errorOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error envelope, got: %v", resp)
	}
	return e
}

// TestHintsAreOmittedWhenEmpty guards the backward-compatibility promise: an
// error with nothing useful to suggest must not carry an empty hint object,
// because a consumer checking for the field's presence would be misled.
func TestHintsAreOmittedWhenEmpty(t *testing.T) {
	e := hintedVerbError(ErrVerbInternal, "boom", &VerbHint{})
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "hint") {
		t.Errorf("an empty hint must be omitted, got %s", data)
	}

	// And the legacy constructor must keep producing a hint-free envelope.
	data, err = json.Marshal(newVerbError(ErrVerbInternal, "boom"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(data), `{"code":"internal","message":"boom"}`; got != want {
		t.Errorf("envelope = %s, want %s", got, want)
	}
}
