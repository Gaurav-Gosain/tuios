package harness

import "testing"

func TestMatchExeGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// The patterns every bundled manifest shipped, against the paths those
		// agents really install to. All of these matched nothing before.
		{"*/claude", "/usr/bin/claude", true},
		{"*/claude", "/home/u/.local/bin/claude", true},
		{"*/share/claude/versions/*", "/home/u/.local/share/claude/versions/2.1.235", true},
		{"*/codex", "/usr/bin/codex", true},
		{"*/codex", "/home/u/.local/share/npm/bin/codex", true},
		{"*/cursor-agent", "/usr/local/bin/cursor-agent", true},
		{"*/.cursor/*/agent", "/home/u/.cursor/1.2.3/agent", true},
		{"**/.cursor/**/agent", "/home/u/.cursor/versions/1.2.3/agent", true},

		// A component wildcard still does not cross a separator.
		{"*/claude", "/usr/bin/claude-helper", false},
		{"*/claude", "/usr/bin/notclaude", false},
		{"*/share/claude/versions/*", "/home/u/share/claude/2.1.235", false},

		// ** spans components; a leading "/" anchors at the root.
		{"**/claude", "/a/b/c/d/claude", true},
		{"/usr/**/claude", "/usr/local/lib/claude", true},
		{"/usr/**/claude", "/opt/local/lib/claude", false},
		{"/usr/bin/claude", "/usr/bin/claude", true},
		{"/usr/bin/claude", "/home/u/bin/claude", false},

		// An unanchored pattern is a suffix match, so a bare name works.
		{"claude", "/usr/bin/claude", true},
		{"claude", "/usr/bin/claude/inner", false},
	}
	for _, tt := range tests {
		if got := matchExeGlob(tt.pattern, tt.path); got != tt.want {
			t.Errorf("matchExeGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestRunToken(t *testing.T) {
	tests := []struct {
		name string
		proc ProcInfo
		want string
	}{
		{"not an interpreter", ProcInfo{Comm: "tail", Exe: "/usr/bin/tail",
			Argv: []string{"tail", "-f", "/home/u/dev/opencode/main.go"}}, ""},
		{"grep in an agent tree", ProcInfo{Comm: "grep", Exe: "/usr/bin/grep",
			Argv: []string{"grep", "-r", "x", "node_modules/@anthropic-ai/claude-code/"}}, ""},
		{"node running a script", ProcInfo{Comm: "node", Exe: "/usr/bin/node",
			Argv: []string{"node", "/u/n_m/@anthropic-ai/claude-code/cli.js", "--resume"}},
			"/u/n_m/@anthropic-ai/claude-code/cli.js"},
		{"node -e skips the flag value only when flagged", ProcInfo{Comm: "node", Exe: "/usr/bin/node",
			Argv: []string{"node", "-e", "setTimeout(()=>{},1)", "/u/@anthropic-ai/claude-code/cli.js"}},
			"setTimeout(()=>{},1)"},
		{"python module", ProcInfo{Comm: "python3", Exe: "/usr/bin/python3.13",
			Argv: []string{"python3", "-m", "aider"}}, "aider"},
		{"pytest inside an agent repo", ProcInfo{Comm: "python3", Exe: "/usr/bin/python3.13",
			Argv: []string{"python3", "-m", "pytest", "tests/aider/test_x.py"}}, "pytest"},
		{"bun run", ProcInfo{Comm: "bun", Exe: "/home/u/.bun/bin/bun",
			Argv: []string{"bun", "run", "/u/.bun/g/node_modules/opencode/index.ts"}},
			"/u/.bun/g/node_modules/opencode/index.ts"},
		{"npx with a flag", ProcInfo{Comm: "npx", Argv: []string{"npx", "-y", "opencode@latest"}},
			"opencode@latest"},
		{"interpreter with nothing to run", ProcInfo{Comm: "bash", Argv: []string{"-bash"}}, ""},
	}
	for _, tt := range tests {
		if got := tt.proc.RunToken(); got != tt.want {
			t.Errorf("%s: RunToken() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestContainsSegments(t *testing.T) {
	tests := []struct {
		have, want string
		match      bool
	}{
		{"/u/n_m/@anthropic-ai/claude-code/cli.js", "@anthropic-ai/claude-code", true},
		{"/u/lib/python3/site-packages/aider/main.py", "/aider/", true},
		{"/u/dev/opencode/main.go", "/opencode/", true},
		// A substring test said yes to all of these.
		{"/u/dev/opencode-legacy/main.go", "/opencode/", false},
		{"/u/dev/myopencode/main.go", "/opencode/", false},
		{"/u/opencode-ai-fork/x", "opencode-ai", false},
		// A version pin still names the package.
		{"opencode@latest", "/opencode/", true},
		{"aider==0.1.0", "/aider/", true},
	}
	for _, tt := range tests {
		got := containsSegments(segments(tt.have), segments(tt.want))
		if got != tt.match {
			t.Errorf("containsSegments(%q, %q) = %v, want %v", tt.have, tt.want, got, tt.match)
		}
	}
}

// TestIdentifyGatesArgv pins the rule this registry exists to get right: argv is
// read only for an interpreter, and only the token it was asked to run.
func TestIdentifyGatesArgv(t *testing.T) {
	r, errs := Load()
	if len(errs) > 0 {
		t.Fatalf("bundled manifests failed to load: %v", errs)
	}
	tests := []struct {
		name string
		proc ProcInfo
		want string
	}{
		{"editor in an opencode checkout", ProcInfo{Comm: "tail", Exe: "/usr/bin/tail",
			Argv: []string{"tail", "-f", "/home/u/dev/opencode/main.go"}}, ""},
		{"grep over a vendored claude-code", ProcInfo{Comm: "grep", Exe: "/usr/bin/grep",
			Argv: []string{"grep", "-r", "x", "/u/n_m/@anthropic-ai/claude-code/"}}, ""},
		{"build in an aider tree", ProcInfo{Comm: "go", Exe: "/usr/bin/go",
			Argv: []string{"go", "build", "./aider/..."}}, ""},
		{"real claude", ProcInfo{Comm: "claude", Exe: "/home/u/.local/share/claude/versions/2.1.235",
			Argv: []string{"claude", "--dangerously-skip-permissions", "-c"}}, "claude-code"},
		{"claude by install path alone", ProcInfo{Comm: "node", Exe: "/usr/bin/claude",
			Argv: []string{"node"}}, "claude-code"},
		{"claude under node", ProcInfo{Comm: "node", Exe: "/usr/bin/node",
			Argv: []string{"node", "/u/n_m/@anthropic-ai/claude-code/cli.js"}}, "claude-code"},
	}
	for _, tt := range tests {
		got, _, ok := r.IdentifyDetail(tt.proc)
		if tt.want == "" && ok {
			t.Errorf("%s: identified as %q, want no match", tt.name, got)
		}
		if tt.want != "" && got != tt.want {
			t.Errorf("%s: identified as %q, want %q", tt.name, got, tt.want)
		}
	}
}
