package session

import (
	"testing"
)

// TestAgentBaseName checks the reduction of a comm or argv token to the base name
// used for matching: directories, a login-shell '-' prefix, script extensions and
// a trailing NUL are all stripped, and the result is lowercased.
func TestAgentBaseName(t *testing.T) {
	cases := map[string]string{
		"claude":                   "claude",
		"/usr/local/bin/claude":    "claude",
		"Claude":                   "claude",
		"claude.js":                "claude",
		"/opt/agents/opencode.mjs": "opencode",
		"-bash":                    "bash",
		"node\x00":                 "node",
		"  aider  ":                "aider",
		"":                         "",
	}
	for in, want := range cases {
		if got := agentBaseName(in); got != want {
			t.Errorf("agentBaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAgentMatcher checks the detection decision across the shapes a harness
// actually launches in: a native binary, a versioned binary behind a renamed
// process, the interpreter wrappers npm and pip installs produce, and the
// non-agent cases that must keep reading as non-agents.
func TestAgentMatcher(t *testing.T) {
	m := newAgentMatcher([]string{"mycli", "  Spaced-Agent "})
	cases := []struct {
		name string
		info foregroundInfo
		want bool
	}{
		{"bare claude", foregroundInfo{comm: "claude", argv: []string{"claude"}}, true},
		{"path claude", foregroundInfo{comm: "claude", argv: []string{"/usr/bin/claude", "--resume"}}, true},
		{"codex", foregroundInfo{comm: "codex", argv: []string{"codex"}}, true},
		{"cursor-agent", foregroundInfo{comm: "cursor-agent", argv: []string{"cursor-agent"}}, true},
		{"plain shell", foregroundInfo{comm: "bash", argv: []string{"-bash"}}, false},
		{"unrelated tool", foregroundInfo{comm: "vim", argv: []string{"vim", "notes.md"}}, false},
		{"node wrapper running claude", foregroundInfo{comm: "node", argv: []string{"node", "/usr/lib/node_modules/claude/cli.js"}}, true},
		// The same generic entry point outside a package directory is a script in
		// a checkout, and a checkout's name is not evidence.
		{"node running a script in a checkout named claude", foregroundInfo{comm: "node", argv: []string{"node", "/home/u/dev/claude/cli.js"}}, false},
		{"npx opencode", foregroundInfo{comm: "npx", argv: []string{"npx", "opencode"}}, true},
		{"node without agent arg", foregroundInfo{comm: "node", argv: []string{"node", "server.js"}}, false},
		{"user-added name", foregroundInfo{comm: "mycli", argv: []string{"mycli"}}, true},
		{"user-added name case-insensitive", foregroundInfo{comm: "spaced-agent", argv: []string{"spaced-agent"}}, true},
		// A non-interpreter comm that does not match must not be rescued by an
		// incidental argument that happens to share an agent's name.
		{"non-interpreter with agent-like arg", foregroundInfo{comm: "grep", argv: []string{"grep", "claude", "log.txt"}}, false},

		// The native installer keeps one binary per release, so the real
		// executable is a version number and only its directory names the agent.
		{
			"versioned binary named only by its directory",
			foregroundInfo{
				comm: "2.1.222",
				argv: []string{"claude", "--resume"},
				exe:  "/home/u/.local/share/claude/versions/2.1.222",
			},
			true,
		},
		// comm is the kernel's 15-character truncation, so a longer name is cut.
		{
			"comm truncated at fifteen characters",
			foregroundInfo{comm: "octofriend-cli-", argv: []string{"octofriend"}, exe: "/usr/local/bin/octofriend"},
			true,
		},
		// The esbuild-style npm shim spawns a native child from a platform package.
		{
			"platform package binary",
			foregroundInfo{
				comm: "droid",
				argv: []string{"droid"},
				exe:  "/home/u/n/node_modules/@factory/cli-linux-x64/bin/droid",
			},
			true,
		},
		// A python-installed agent runs as the interpreter.
		{
			"python running aider",
			foregroundInfo{comm: "python3", argv: []string{"python3", "/usr/lib/py/site-packages/aider/main.py"}, exe: "/usr/bin/python3.13"},
			true,
		},
		{
			"bun running an agent script",
			foregroundInfo{comm: "bun", argv: []string{"bun", "/home/u/.bun/install/global/node_modules/opencode/index.ts"}, exe: "/home/u/.bun/bin/bun"},
			true,
		},
		// A wrapper script renames the process while the binary stays the shell.
		{
			"renamed wrapper over a shell",
			foregroundInfo{comm: "my-launcher", argv: []string{"my-launcher", "codex"}, exe: "/usr/bin/bash"},
			true,
		},
		// An unrelated python program stays unrelated.
		{
			"python running something else",
			foregroundInfo{comm: "python3", argv: []string{"python3", "manage.py", "runserver"}, exe: "/usr/bin/python3.13"},
			false,
		},
		// A binary that merely lives near an agent-named directory it is not.
		{
			"unrelated binary with a clean path",
			foregroundInfo{comm: "htop", argv: []string{"htop"}, exe: "/usr/bin/htop"},
			false,
		},
	}
	for _, c := range cases {
		if got := m.isAgent(c.info); got != c.want {
			t.Errorf("%s: isAgent(%+v) = %v, want %v", c.name, c.info, got, c.want)
		}
	}
}

// fakeResolver returns a resolve function backed by a per-PTY table, so agent
// detection can be exercised without a real /proc or a real agent process.
type fakeProc struct {
	info    foregroundInfo
	running bool
}

func fakeResolver(table map[string]fakeProc) func(string) (foregroundInfo, bool) {
	return func(ptyID string) (foregroundInfo, bool) {
		p, ok := table[ptyID]
		if !ok {
			return foregroundInfo{}, false
		}
		return p.info, p.running
	}
}

func ptyIDOfWindow(t *testing.T, sess *Session, windowID string) string {
	t.Helper()
	for _, w := range sess.GetState().Windows {
		if w.ID == windowID {
			return w.PTYID
		}
	}
	t.Fatalf("window %s not found", windowID)
	return ""
}
