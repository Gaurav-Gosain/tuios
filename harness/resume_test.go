package harness

import (
	"strings"
	"testing"
)

// TestResumeBlockIsChecked holds a manifest's [resume] to tokens every shell
// reads unquoted, with the placeholder somewhere past the program.
func TestResumeBlockIsChecked(t *testing.T) {
	head := "schema_version = 1\nid = \"x\"\n[detect]\ncomm = [\"xagent\"]\n"
	for _, tc := range []struct {
		name, body, wantErr string
	}{
		{"valid", `[resume]
argv = ["xagent", "--resume", "{session_id}"]`, ""},
		{"part of a token", `[resume]
argv = ["xagent", "--resume={session_id}"]`, ""},
		{"empty block", `[resume]
source = "none"`, ""},
		{"no placeholder", `[resume]
argv = ["xagent", "--continue"]`, "does not hold"},
		{"program is the placeholder", `[resume]
argv = ["{session_id}"]`, "argv[0]"},
		{"shell metacharacter", `[resume]
argv = ["xagent", "--resume", "{session_id}", ";", "rm"]`, "token 3"},
		{"space", `[resume]
argv = ["xagent --resume", "{session_id}"]`, "token 0"},
		{"empty token", `[resume]
argv = ["xagent", "", "{session_id}"]`, "token 1"},
		{"another placeholder", `[resume]
argv = ["xagent", "{cwd}", "{session_id}"]`, "token 1"},
		{"kept flags", `[resume]
argv = ["xagent", "--resume={session_id}"]
keep_flags = ["--yolo"]
keep_options = ["--model", "-m"]`, ""},
		{"kept resume flag", `[resume]
argv = ["xagent", "--resume={session_id}"]
keep_options = ["--resume"]`, "own flag"},
		{"kept flag with a value", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_options = ["--model=opus"]`, "--model=opus"},
		{"kept flag is not a flag", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["model"]`, "model"},
		{"kept flag with a metacharacter", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["--a;b"]`, "--a;b"},
		{"risky marks", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["--yolo"]
keep_options = ["--sandbox"]
keep_risky = ["--yolo", "--sandbox=off"]`, ""},
		{"risky flag not kept", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["--yolo"]
keep_risky = ["--bypass"]`, "--bypass"},
		{"risky value of a flag", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["--yolo"]
keep_risky = ["--yolo=1"]`, "--yolo=1"},
		{"kept flag twice", `[resume]
argv = ["xagent", "--resume", "{session_id}"]
keep_flags = ["--yolo"]
keep_options = ["--yolo"]`, "twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseManifest(tc.name+".toml", []byte(head+tc.body))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("parse: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("parse error = %v, want one naming %q", err, tc.wantErr)
			}
		})
	}
}
