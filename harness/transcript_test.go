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
		{
			name: "session_file may name a fixed path below a folder per session",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*/logs/transcript.jsonl"
session_file = "{session_id}/logs/transcript.jsonl"
`,
		},
		{
			name: "session_file may share the file name with the id",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "T-*.json"
session_file = "{session_id}.json"
`,
		},
		{
			name: "session_file keeps the id a whole folder name",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*/logs/transcript.jsonl"
session_file = "c-{session_id}/logs/transcript.jsonl"
`,
			wantErr: "whole folder name",
		},
		{
			name: "session_file may not climb out of dir",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*/*/transcript.jsonl"
session_file = "{session_id}/../transcript.jsonl"
`,
			wantErr: `no ".."`,
		},
		{
			name: "session_file may not hold a backslash",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*.jsonl"
session_file = '{session_id}\x.jsonl'
`,
			wantErr: `no "\"`,
		},
		{
			name: "session_file names no placeholder but the id",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*/*/transcript.jsonl"
session_file = "{session_id}/{home}/transcript.jsonl"
`,
			wantErr: "no other placeholder",
		},
		{
			name: "session_file names the id once",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*.json"
session_file = "{session_id}-{session_id}.json"
`,
			wantErr: "no other placeholder",
		},
		{
			name: "session_file has no empty path element",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*/*/transcript.jsonl"
session_file = "{session_id}//transcript.jsonl"
`,
			wantErr: "empty",
		},
		{
			name: "session_file below a folder needs a glob that spans folders",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*.jsonl"
session_file = "{session_id}/transcript.jsonl"
`,
			wantErr: "path elements",
		},
		{
			name: "session_file has as many folders as the glob",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*/transcript.jsonl"
session_file = "{session_id}/logs/transcript.jsonl"
`,
			wantErr: "path elements",
		},
		{
			name: "session_file names a file the glob matches",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*/transcript_full.jsonl"
session_file = "{session_id}/transcript.jsonl"
`,
			wantErr: "where glob",
		},
		{
			name: `session_file has no "." path element`,
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*/*/transcript.jsonl"
session_file = "{session_id}/./transcript.jsonl"
`,
			wantErr: `"." path element`,
		},
		{
			// filepath.Match("[^/]/t.jsonl", "a/t.jsonl") is true: the class
			// matches the separator, so the glob has no path elements to
			// hold session_file's against.
			name: "session_file needs a glob with no / in a character class",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "[^/]/t.jsonl"
session_file = "{session_id}/t.jsonl"
`,
			wantErr: "character class",
		},
		{
			name: "session_file names a file exclude keeps",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*/*.jsonl"
exclude = ["*.ledger.jsonl"]
session_file = "{session_id}/out.jsonl"
`,
		},
		{
			name: "session_file names no file exclude leaves out",
			block: `[transcript]
reader = "amp-thread"
dir = "{home}/x"
glob = "*/*.jsonl"
exclude = ["*.ledger.jsonl"]
session_file = "{session_id}/out.ledger.jsonl"
`,
			wantErr: "exclude",
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

// A session id is the hook's word, so it names the file session_file gives it
// and nothing else: an id with a "/", a "\" or a ".." in it could point the
// daemon outside dir, one ending in "." could name another session's file on
// Windows, and neither names a file.
func TestSessionPath(t *testing.T) {
	tr := Transcript{Reader: "amp-thread", Dir: "{home}/x", Glob: "*/logs/transcript.jsonl", SessionFile: "{session_id}/logs/transcript.jsonl"}
	root := tr.Root()
	if root == "" {
		t.Skip("no home directory")
	}
	if got, want := tr.SessionPath("c0ffee-1.2_x"), filepath.Join(root, "c0ffee-1.2_x", "logs", "transcript.jsonl"); got != want {
		t.Fatalf("SessionPath = %q, want %q", got, want)
	}
	// Ids that are odd but harmless name their own folder: one that starts
	// with "-" or ".", and one in another case. On a case-insensitive volume
	// "C0FFEE" reaches the same folder as "c0ffee"; that is the volume's
	// doing, and the file is still under dir.
	for _, id := range []string{"-x", ".x", "C0FFEE-1.2_X"} {
		if got, want := tr.SessionPath(id), filepath.Join(root, id, "logs", "transcript.jsonl"); got != want {
			t.Fatalf("SessionPath(%q) = %q, want %q", id, got, want)
		}
	}
	// An id ending in "." names nothing: Windows drops the dot, so "x." would
	// reach x's folder.
	for _, id := range []string{"", ".", "..", "...", "x.", "a/b", `a\b`, "a..b", "../x", strings.Repeat("a", 129)} {
		if got := tr.SessionPath(id); got != "" {
			t.Fatalf("session id %q named %q", id, got)
		}
	}
}

// Every bundled manifest loads under the session_file rules, Amp's, whose
// id shares the file name with its extension, still names its thread file,
// and Antigravity CLI's names the file in the conversation's folder.
func TestShippedSessionFiles(t *testing.T) {
	reg, errs := Load()
	if len(errs) != 0 {
		t.Fatalf("load: %v", errs)
	}
	tr := reg.TranscriptFor("amp")
	if tr == nil || tr.Root() == "" {
		t.Fatal("amp has no transcript block, or no home directory")
	}
	if got, want := tr.SessionPath("T-1"), filepath.Join(tr.Root(), "T-1.json"); got != want {
		t.Fatalf("amp SessionPath = %q, want %q", got, want)
	}
	tr = reg.TranscriptFor("antigravity")
	if tr == nil {
		t.Fatal("antigravity has no transcript block")
	}
	if got, want := tr.SessionPath("c-1"), filepath.Join(tr.Root(), "c-1", ".system_generated", "logs", "transcript_full.jsonl"); got != want {
		t.Fatalf("antigravity SessionPath = %q, want %q", got, want)
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
