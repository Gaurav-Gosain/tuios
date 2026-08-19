package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledClaudeCodeCarriesATranscript(t *testing.T) {
	r, errs := Load()
	if len(errs) != 0 {
		t.Fatalf("load errors: %v", errs)
	}
	tr := r.TranscriptFor("claude-code")
	if tr == nil {
		t.Fatal("claude-code has no transcript block")
	}
	if tr.Reader != ReaderJSONL || tr.Glob != "*.jsonl" {
		t.Fatalf("transcript = %+v", *tr)
	}
	if !tr.Verifies("cwd") || !tr.Verifies("version") {
		t.Fatalf("verify = %v, want cwd and version", tr.Verify)
	}
}

// Almost every harness has no transcript, and for those the daemon must behave
// exactly as it did before this existed. nil is how that is expressed.
func TestHarnessesWithoutATranscriptReturnNil(t *testing.T) {
	r, _ := Load()
	for _, id := range r.IDs() {
		if id == "claude-code" {
			continue
		}
		if tr := r.TranscriptFor(id); tr != nil {
			t.Fatalf("%s unexpectedly has a transcript block: %+v", id, *tr)
		}
	}
}

// The dashes mangling, pinned against the directory names observed on a machine
// with 39 of them. The nested case is the one that shows the replacement is
// literal: a path component that already begins with a dash keeps it, so the
// result carries a doubled dash rather than being normalised.
func TestDashPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/home/gaurav/dev/tuios", "-home-gaurav-dev-tuios"},
		{"/home/gaurav", "-home-gaurav"},
		{"/home/gaurav/dev/three-dee/three-dee", "-home-gaurav-dev-three-dee-three-dee"},
		{"/tmp/claude-1000/-home-gaurav-dev-tuios/x/work", "-tmp-claude-1000--home-gaurav-dev-tuios-x-work"},
	}
	for _, tc := range tests {
		if got := DashPath(tc.in); got != tc.want {
			t.Fatalf("DashPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExpandDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	tr := Transcript{Reader: ReaderJSONL, Dir: "{home}/.claude/projects/{cwd:dashes}", Glob: "*.jsonl"}
	want := filepath.Join(home, ".claude", "projects", "-home-x-proj")
	if got := tr.ExpandDir("/home/x/proj"); got != want {
		t.Fatalf("ExpandDir = %q, want %q", got, want)
	}
	// A pane whose directory is unknown yields no directory, which yields no
	// search and so no claim.
	if got := tr.ExpandDir(""); got != "" {
		t.Fatalf("ExpandDir(\"\") = %q, want empty", got)
	}
	// A harness with no block never expands to anything.
	if got := (Transcript{}).ExpandDir("/home/x"); got != "" {
		t.Fatalf("empty transcript expanded to %q", got)
	}
}

func TestTranscriptValidation(t *testing.T) {
	base := `schema_version = 1
id = "demo-agent"
[detect]
comm = ["demoagent"]
`
	tests := []struct {
		name, block, wantErr string
	}{
		{
			name:  "absent block is fine",
			block: "",
		},
		{
			name: "complete block is fine",
			block: `[transcript]
reader = "claude-code-jsonl"
dir = "{home}/.demo/{cwd:dashes}"
glob = "*.jsonl"
verify = ["cwd"]
`,
		},
		{
			name: "unknown reader is named",
			block: `[transcript]
reader = "wishful-thinking"
dir = "{home}/x"
glob = "*.jsonl"
`,
			wantErr: "is not one this build has",
		},
		{
			name: "half a block is an error, not silence",
			block: `[transcript]
reader = "claude-code-jsonl"
dir = "{home}/x"
`,
			wantErr: "needs both dir and glob",
		},
		{
			name: "a typo in a placeholder is caught rather than left in the path",
			block: `[transcript]
reader = "claude-code-jsonl"
dir = "{home}/x/{cwd:dashed}"
glob = "*.jsonl"
`,
			wantErr: "unknown placeholder",
		},
		{
			name: "verify may only name fields the reader reports",
			block: `[transcript]
reader = "claude-code-jsonl"
dir = "{home}/x"
glob = "*.jsonl"
verify = ["lastPrompt"]
`,
			wantErr: "unknown field",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseManifest("demo.toml", []byte(base+tc.block))
			switch {
			case tc.wantErr == "":
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			case err == nil:
				t.Fatalf("want error containing %q, got none", tc.wantErr)
			case !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}
