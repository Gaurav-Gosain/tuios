package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestRunExitsWithTheCommandsStatus holds `tuios run` to what makes it
// composable in a script: it prints what the command printed and exits with
// the command's status, and a shell that sent no status is said on stderr
// rather than guessed.
func TestRunExitsWithTheCommandsStatus(t *testing.T) {
	tests := []struct {
		name       string
		result     string
		wantOut    string
		wantStatus int
		wantErrOut bool
	}{
		{"success", `{"exit_code":0,"output":"ok"}`, "ok\n", 0, false},
		{"failure", `{"exit_code":2,"output":"FAIL"}`, "FAIL\n", 2, false},
		{"no status", `{"output":"^C"}`, "^C\n", 0, true},
		{"cut output", `{"exit_code":0,"output":"tail","truncated":true}`, "tail\n", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errs bytes.Buffer
			err := printRunResult(&out, &errs, json.RawMessage(tt.result))
			if got := exitStatusOf(err); got != tt.wantStatus {
				t.Fatalf("status = %d, want %d (err %v)", got, tt.wantStatus, err)
			}
			if out.String() != tt.wantOut {
				t.Fatalf("stdout = %q, want %q", out.String(), tt.wantOut)
			}
			if (errs.Len() > 0) != tt.wantErrOut {
				t.Fatalf("stderr = %q, want output: %v", errs.String(), tt.wantErrOut)
			}
		})
	}
}

// exitStatusOf is the status exitStatus would give err, without printing.
func exitStatusOf(err error) int {
	if err == nil {
		return 0
	}
	if s, ok := err.(*statusError); ok {
		return s.ExitStatus()
	}
	return 1
}
