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

// TestIdentify checks the shapes a harness launches in resolve to the right
// id, and that unrelated programs resolve to none.
//
// It pins the rule this registry exists to get right: argv is read only for
// an interpreter, and only the token it was asked to run. The first class
// harnesses are held to process identities measured on a real machine rather
// than to shapes invented to suit the matcher, and each such case says how it
// was obtained.
func TestIdentify(t *testing.T) {
	r, errs := Load()
	if len(errs) > 0 {
		t.Fatalf("bundled manifests failed to load: %v", errs)
	}
	tests := []struct {
		name string
		proc ProcInfo
		want string
	}{
		{"native claude", ProcInfo{Comm: "claude", Argv: []string{"claude"}}, "claude-code"},
		{"claude renamed over a versioned binary", ProcInfo{Comm: "2.1.222", Argv: []string{"claude", "--resume"},
			Exe: "/home/u/.local/share/claude/versions/2.1.222"}, "claude-code"},
		{"claude from npm", ProcInfo{Comm: "node",
			Argv: []string{"node", "/n/node_modules/@anthropic-ai/claude-code/cli.js"}}, "claude-code"},
		{"claude by install path alone", ProcInfo{Comm: "node", Exe: "/usr/bin/claude",
			Argv: []string{"node"}}, "claude-code"},
		// Measured: Claude Code's native install symlinks a version-named binary.
		{"claude", ProcInfo{Comm: "claude", Argv: []string{"claude", "--dangerously-skip-permissions", "-c"},
			Exe: "/home/u/.local/share/claude/versions/2.1.235"}, "claude-code"},
		{"codex native", ProcInfo{Comm: "codex", Argv: []string{"codex"}}, "codex"},
		// Not installed here, so this is the documented layout rather than a
		// measurement: codex ships a native binary and an npm package.
		{"codex native with its path", ProcInfo{Comm: "codex", Argv: []string{"codex"}, Exe: "/usr/local/bin/codex"}, "codex"},
		{"codex from the npm shim", ProcInfo{Comm: "node",
			Argv: []string{"node", "/n/node_modules/@openai/codex/bin/codex.js"}}, "codex"},
		{"codex from npm under node", ProcInfo{Comm: "node", Exe: "/usr/bin/node",
			Argv: []string{"node", "/u/n_m/@openai/codex/bin/codex.js"}}, "codex"},
		{"gemini", ProcInfo{Comm: "gemini", Argv: []string{"gemini"}}, "gemini-cli"},
		// Measured: gemini from a bun shim runs under node with comm rewritten to
		// "MainThread". The only place it says gemini is the token node was run
		// with, which is why argv0 reads that token too.
		{"gemini under node", ProcInfo{Comm: "MainThread",
			Argv: []string{"/home/u/node/bin/node", "/home/u/.bun/bin/gemini"},
			Exe:  "/home/u/node/bin/node"}, "gemini-cli"},
		// Measured: crush and opencode are native binaries and say so plainly.
		{"opencode", ProcInfo{Comm: "opencode", Argv: []string{"opencode"}, Exe: "/usr/bin/opencode"}, "opencode"},
		{"crush", ProcInfo{Comm: "crush", Argv: []string{"crush"}, Exe: "/usr/bin/crush"}, "crush"},
		{"droid via platform package", ProcInfo{Comm: "droid", Argv: []string{"droid"}}, "droid"},
		// Measured: pi 0.x from its bun shim. process.title rewrites comm and
		// argv[0] to "pi" and the script path is gone; the executable is node.
		{"pi", ProcInfo{Comm: "pi", Argv: []string{"pi"},
			Exe: "/home/u/.vite-plus/js_runtime/node/24.19.0/bin/node"}, "pi"},
		// The same name with no Node behind it is not the coding agent.
		{"a static binary called pi", ProcInfo{Comm: "pi", Argv: []string{"pi"},
			Exe: "/usr/local/bin/pi"}, ""},
		{"pi with no readable executable", ProcInfo{Comm: "pi", Argv: []string{"pi"}}, ""},

		{"editor in an opencode checkout", ProcInfo{Comm: "tail", Exe: "/usr/bin/tail",
			Argv: []string{"tail", "-f", "/home/u/dev/opencode/main.go"}}, ""},
		{"grep over a vendored claude-code", ProcInfo{Comm: "grep", Exe: "/usr/bin/grep",
			Argv: []string{"grep", "-r", "x", "/u/n_m/@anthropic-ai/claude-code/"}}, ""},
		{"build in an aider tree", ProcInfo{Comm: "go", Exe: "/usr/bin/go",
			Argv: []string{"go", "build", "./aider/..."}}, ""},
		{"plain shell", ProcInfo{Comm: "bash", Argv: []string{"-bash"}, Exe: "/usr/bin/bash"}, ""},
		{"unrelated tool", ProcInfo{Comm: "htop", Argv: []string{"htop"}, Exe: "/usr/bin/htop"}, ""},
		{"nothing at all", ProcInfo{}, ""},
	}
	for _, tt := range tests {
		got, _, ok := r.IdentifyDetail(tt.proc)
		if tt.want == "" && ok {
			t.Errorf("%s: identified as %q, want no match", tt.name, got)
		}
		if tt.want != "" && (!ok || got != tt.want) {
			t.Errorf("%s: identified as %q (%v), want %q", tt.name, got, ok, tt.want)
		}
	}
}
