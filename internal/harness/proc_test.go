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

func TestPackageSegments(t *testing.T) {
	tests := []struct {
		have, want string
		match      bool
	}{
		// A package under a package-manager directory.
		{"/u/n_m/node_modules/@anthropic-ai/claude-code/cli.js", "@anthropic-ai/claude-code", true},
		{"/u/lib/python3/site-packages/aider/main.py", "/aider/", true},
		{"/u/.bun/install/global/node_modules/opencode/index.ts", "/opencode/", true},
		{"/u/.npm/_npx/1a2b/node_modules/cline/bin/cline", "cline/bin/cline", true},
		{"/u/.bun/install/cache/opencode-ai@1.2.3@@@1/bin/opencode", "opencode-ai", true},
		// A scoped name is an npm scope wherever it sits.
		{"/u/n_m/@openai/codex/bin/codex.js", "@openai/codex", true},
		// A bare token names a package rather than a path.
		{"opencode@latest", "/opencode/", true},
		{"aider==0.1.0", "/aider/", true},
		// A directory in a person's checkout is not a package. The shipped
		// matcher said yes to all of these.
		{"/u/dev/opencode/main.go", "/opencode/", false},
		{"/u/dev/crush/scripts/build.sh", "/crush/", false},
		{"/u/dev/crush/node_modules/vite/bin/vite.js", "/crush/", false},
		{"/u/.dotfiles/crush/x.sh", "/crush/", false},
		// A substring test said yes to all of these.
		{"/u/n_m/node_modules/opencode-legacy/main.js", "/opencode/", false},
		{"/u/n_m/node_modules/myopencode/main.js", "/opencode/", false},
		{"/u/n_m/node_modules/opencode-ai-fork/x", "opencode-ai", false},
	}
	for _, tt := range tests {
		got := packageSegments(segments(tt.have), segments(tt.want))
		if got != tt.match {
			t.Errorf("packageSegments(%q, %q) = %v, want %v", tt.have, tt.want, got, tt.match)
		}
	}
}

// TestWraps pins which processes are worth looking behind: interpreters and
// resident launchers, by name or by executable, and nothing else.
func TestWraps(t *testing.T) {
	tests := []struct {
		name string
		proc ProcInfo
		want bool
	}{
		{"a shell", ProcInfo{Comm: "sh", Exe: "/usr/bin/bash"}, true},
		{"a renamed shell script", ProcInfo{Comm: "agent-launcher", Exe: "/usr/bin/bash"}, true},
		{"timeout", ProcInfo{Comm: "timeout", Exe: "/usr/bin/timeout"}, true},
		{"npm exec", ProcInfo{Comm: "npm exec", Exe: "/usr/bin/node"}, true},
		{"a version manager", ProcInfo{Comm: "mise", Exe: "/u/.local/bin/mise"}, true},
		{"an editor", ProcInfo{Comm: "vim", Exe: "/usr/bin/vim"}, false},
		{"a build", ProcInfo{Comm: "go", Exe: "/usr/lib/go/bin/go"}, false},
		{"an agent", ProcInfo{Comm: "claude", Exe: "/u/.local/share/claude/versions/2.1.266"}, false},
	}
	for _, tt := range tests {
		if got := tt.proc.Wraps(); got != tt.want {
			t.Errorf("%s: Wraps() = %v, want %v", tt.name, got, tt.want)
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
