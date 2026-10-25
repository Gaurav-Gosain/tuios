package harness

import (
	"path/filepath"
	"strings"
	"testing"
)

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

// The folder a Pi hook may name a transcript in follows Pi's own rule for its
// agent folder, the one the extension install already honours: PI_CODING_AGENT_DIR
// with a leading ~ expanded, else ~/.pi/agent. A relative value names no folder
// the daemon could check a path against, so it gives no root.
func TestPiAgentPlaceholder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tr := Transcript{Reader: "pi-jsonl", Dir: "{pi_agent}/sessions", Glob: "*/*.jsonl"}
	tests := []struct{ name, env, want string }{
		{"unset", "", filepath.Join(home, ".pi", "agent", "sessions")},
		{"blank", "  ", filepath.Join(home, ".pi", "agent", "sessions")},
		{"absolute", "/srv/pi-agent", "/srv/pi-agent/sessions"},
		{"home shorthand", "~/work/pi", filepath.Join(home, "work", "pi", "sessions")},
		{"bare home", "~", filepath.Join(home, "sessions")},
		{"relative", "pi-agent", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_CODING_AGENT_DIR", tc.env)
			if got := tr.Root(); got != tc.want {
				t.Fatalf("Root() = %q, want %q", got, tc.want)
			}
		})
	}
}
