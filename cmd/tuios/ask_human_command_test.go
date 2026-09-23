package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestAskHumanPrintsOnlyTheAnswer keeps stdout to the answer, so a script can
// capture it, and gives each way the question can end its own status: 0 with
// an answer, 2 for no answer yet, 1 for a question that ended with none.
func TestAskHumanPrintsOnlyTheAnswer(t *testing.T) {
	tests := []struct {
		name       string
		result     string
		wantOut    string
		wantStatus int
	}{
		{"answered", `{"status":"answered","answer":"yes","request_id":"r1"}`, "yes\n", 0},
		{"pending", `{"status":"pending","request_id":"r1"}`, "", askPendingStatus},
		{"dismissed", `{"status":"dismissed","request_id":"r1"}`, "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errs bytes.Buffer
			err := printAskHuman(&out, &errs, json.RawMessage(tt.result))
			if got := exitStatusOf(err); got != tt.wantStatus {
				t.Fatalf("status = %d, want %d", got, tt.wantStatus)
			}
			if out.String() != tt.wantOut {
				t.Fatalf("stdout = %q, want %q", out.String(), tt.wantOut)
			}
			if tt.wantStatus == askPendingStatus && !bytes.Contains(errs.Bytes(), []byte("--request-id r1")) {
				t.Fatalf("a pending ask does not say how to come back: %q", errs.String())
			}
		})
	}
}

// TestPopupWaitPrintsTheOutputAndExitsWithItsStatus is the CLI half of popup
// --wait: the captured output as it was printed, and the command's status,
// with a popup closed by hand reading as interrupted.
func TestPopupWaitPrintsTheOutputAndExitsWithItsStatus(t *testing.T) {
	var out bytes.Buffer
	if err := printPopupResult(&out, json.RawMessage(`{"exit_code":0,"stdout":"picked\n"}`)); err != nil || out.String() != "picked\n" {
		t.Fatalf("got %q, %v; want the output as printed and no error", out.String(), err)
	}
	if got := exitStatusOf(printPopupResult(&out, json.RawMessage(`{"exit_code":3}`))); got != 3 {
		t.Fatalf("status = %d, want 3", got)
	}
	if got := exitStatusOf(printPopupResult(&out, json.RawMessage(`{"exit_code":-1}`))); got != 130 {
		t.Fatalf("status of a popup closed by hand = %d, want 130", got)
	}
}
