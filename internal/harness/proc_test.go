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
	runIdentifyCases(t, r, tests)
}

// TestIdentifyMeasuredLaunches pins the five harnesses the project treats as
// first class against the process identities measured on a real machine, rather
// than against shapes invented to suit the matcher. Each case names how it was
// obtained.
func TestIdentifyMeasuredLaunches(t *testing.T) {
	r, errs := Load()
	if len(errs) > 0 {
		t.Fatalf("bundled manifests failed to load: %v", errs)
	}
	tests := []struct {
		name string
		proc ProcInfo
		want string
	}{
		// Measured: pi 0.x from its bun shim. process.title rewrites comm and
		// argv[0] to "pi" and the script path is gone; the executable is node.
		{"pi", ProcInfo{Comm: "pi", Argv: []string{"pi"},
			Exe: "/home/u/.vite-plus/js_runtime/node/24.19.0/bin/node"}, "pi"},
		// The same name with no Node behind it is not the coding agent.
		{"a static binary called pi", ProcInfo{Comm: "pi", Argv: []string{"pi"},
			Exe: "/usr/local/bin/pi"}, ""},
		{"pi with no readable executable", ProcInfo{Comm: "pi", Argv: []string{"pi"}}, ""},
		// Measured: crush and opencode are native binaries and say so plainly.
		{"crush", ProcInfo{Comm: "crush", Argv: []string{"crush"}, Exe: "/usr/bin/crush"}, "crush"},
		{"opencode", ProcInfo{Comm: "opencode", Argv: []string{"opencode"}, Exe: "/usr/bin/opencode"}, "opencode"},
		// Measured: Claude Code's native install symlinks a version-named binary.
		{"claude", ProcInfo{Comm: "claude", Argv: []string{"claude", "-c"},
			Exe: "/home/u/.local/share/claude/versions/2.1.235"}, "claude-code"},
		// Measured: gemini from a bun shim runs under node with comm rewritten to
		// "MainThread". The only place it says gemini is the token node was run
		// with, which is why argv0 reads that token too.
		{"gemini under node", ProcInfo{Comm: "MainThread",
			Argv: []string{"/home/u/node/bin/node", "/home/u/.bun/bin/gemini"},
			Exe:  "/home/u/node/bin/node"}, "gemini-cli"},
		// Not installed here, so this is the documented layout rather than a
		// measurement: codex ships a native binary and an npm package.
		{"codex native", ProcInfo{Comm: "codex", Argv: []string{"codex"}, Exe: "/usr/local/bin/codex"}, "codex"},
		{"codex from npm", ProcInfo{Comm: "node", Exe: "/usr/bin/node",
			Argv: []string{"node", "/u/n_m/@openai/codex/bin/codex.js"}}, "codex"},
	}
	runIdentifyCases(t, r, tests)
}

func runIdentifyCases(t *testing.T, r *Registry, tests []struct {
	name string
	proc ProcInfo
	want string
},
) {
	t.Helper()
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
