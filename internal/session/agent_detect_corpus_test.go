package session

import (
	"strings"
	"testing"
)

// corpusCase is one real command line as the detector reads it: the foreground
// process group leader of a pane, and the other members of that group when the
// leader is a wrapper. Each case says how it was obtained, so a shape nobody has
// measured is labelled as such rather than passed off as evidence.
type corpusCase struct {
	name string
	// how is "measured" for a shape read from /proc on a real machine, or
	// "documented" for one taken from an installer's published layout.
	how  string
	info foregroundInfo
	// group holds the other members of the foreground process group, in the
	// order a breadth-first walk from the leader yields them.
	group []foregroundInfo
	// want is the harness id the pane must be attributed to, "" when it must not
	// be an agent, or nameList when only the flat name list can claim it.
	want string
}

const nameList = "(name list)"

// detectionCorpus is the table the matcher is held to. The false positives are
// the ones a maintainer reported from daily use, reproduced under a real PTY on
// 2026-09-09; the true positives are the harnesses tuios ships manifests for,
// launched the way their installers launch them.
var detectionCorpus = []corpusCase{
	// --- False positives. None of these runs an agent. ---
	{
		name: "a shell script in a checkout named crush",
		how:  "measured",
		info: foregroundInfo{comm: "build.sh", exe: "/usr/bin/bash",
			argv: []string{"/bin/bash", "/home/u/dev/crush/scripts/build.sh"}},
		group: []foregroundInfo{{comm: "sleep", exe: "/usr/bin/sleep", argv: []string{"sleep", "30"}}},
	},
	{
		name: "bash running a deploy script under a directory named claude",
		how:  "measured",
		info: foregroundInfo{comm: "bash", exe: "/usr/bin/bash",
			argv: []string{"bash", "/home/u/claude/deploy.sh"}},
		group: []foregroundInfo{{comm: "sleep", exe: "/usr/bin/sleep", argv: []string{"sleep", "30"}}},
	},
	{
		name: "python running a tool under a directory named codex",
		how:  "measured",
		info: foregroundInfo{comm: "python3", exe: "/usr/bin/python3.14",
			argv: []string{"python3", "/home/u/dev/codex/tool.py"}},
	},
	{
		name: "vim editing a file under a directory named codex",
		how:  "measured",
		info: foregroundInfo{comm: "vim", exe: "/usr/bin/vim",
			argv: []string{"vim", "/home/u/dev/codex/tool.py"}},
	},
	{
		name: "git log over a path named cursor",
		how:  "measured",
		info: foregroundInfo{comm: "git", exe: "/usr/bin/git",
			argv: []string{"git", "log", "--oneline", "--", "src/cursor/"}},
	},
	{
		name: "the pane shell at its prompt after cd into a checkout named crush",
		how:  "measured",
		info: foregroundInfo{comm: "fish", exe: "/usr/bin/fish", argv: []string{"-fish"}},
	},
	{
		name: "a compiled binary that lives under a checkout named claude-code",
		how:  "documented",
		info: foregroundInfo{comm: "server", exe: "/home/u/dev/claude-code/build/server",
			argv: []string{"./build/server"}},
	},
	{
		name: "a go build inside a checkout named crush",
		how:  "documented",
		info: foregroundInfo{comm: "go", exe: "/usr/lib/go/bin/go",
			argv: []string{"go", "build", "./..."}},
		group: []foregroundInfo{{comm: "compile", exe: "/usr/lib/go/pkg/tool/linux_amd64/compile",
			argv: []string{"/usr/lib/go/pkg/tool/linux_amd64/compile", "-p", "crush/internal/tui"}}},
	},
	{
		name: "node running a dev server from a project's node_modules under a checkout named crush",
		how:  "documented",
		info: foregroundInfo{comm: "node", exe: "/usr/bin/node",
			argv: []string{"node", "/home/u/dev/crush/node_modules/vite/bin/vite.js"}},
	},
	{
		name: "bun run of a script under a directory named codex",
		how:  "documented",
		info: foregroundInfo{comm: "bun", exe: "/home/u/.bun/bin/bun",
			argv: []string{"bun", "run", "/home/u/dev/codex/scripts/seed.ts"}},
	},
	{
		name: "grep for an agent's name",
		how:  "measured",
		info: foregroundInfo{comm: "grep", exe: "/usr/bin/grep",
			argv: []string{"grep", "-r", "claude", "."}},
	},
	{
		name: "pytest inside aider's own repository",
		how:  "documented",
		info: foregroundInfo{comm: "python3", exe: "/usr/bin/python3.13",
			argv: []string{"python3", "-m", "pytest", "tests/aider/test_x.py"}},
	},
	{
		name: "tail of a file in an opencode checkout",
		how:  "documented",
		info: foregroundInfo{comm: "tail", exe: "/usr/bin/tail",
			argv: []string{"tail", "-f", "/home/u/dev/opencode/main.go"}},
	},
	{
		name: "a static binary someone named pi",
		how:  "documented",
		info: foregroundInfo{comm: "pi", exe: "/usr/local/bin/pi", argv: []string{"pi"}},
	},
	{
		name: "the kilo text editor",
		how:  "documented",
		info: foregroundInfo{comm: "kilo", exe: "/usr/local/bin/kilo", argv: []string{"kilo", "notes.txt"}},
	},
	{
		name: "a shell with a background agent job",
		how:  "documented",
		info: foregroundInfo{comm: "bash", exe: "/usr/bin/bash", argv: []string{"-bash"}},
		// A background job is in its own process group. The walk never yields it
		// because it is not in the foreground group, so it is absent here; the
		// case pins that a shell at its prompt reads as no agent.
	},

	// --- True positives: the leader is the agent. ---
	{
		name: "claude from the native installer",
		how:  "measured",
		info: foregroundInfo{comm: "claude", exe: "/home/u/.local/share/claude/versions/2.1.266",
			argv: []string{"claude", "--dangerously-skip-permissions", "-c"}},
		want: "claude-code",
	},
	{
		name: "claude renamed over the version-named binary",
		how:  "measured",
		info: foregroundInfo{comm: "2.1.222", exe: "/home/u/.local/share/claude/versions/2.1.222",
			argv: []string{"claude", "--resume"}},
		want: "claude-code",
	},
	{
		name: "claude from npm",
		how:  "documented",
		info: foregroundInfo{comm: "node", exe: "/usr/bin/node",
			argv: []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}},
		want: "claude-code",
	},
	{
		name: "crush native",
		how:  "measured",
		info: foregroundInfo{comm: "crush", exe: "/usr/bin/crush", argv: []string{"crush"}},
		want: "crush",
	},
	{
		name: "crush from npm",
		how:  "documented",
		info: foregroundInfo{comm: "node", exe: "/usr/bin/node",
			argv: []string{"node", "/usr/lib/node_modules/@charmland/crush/run-crush.js"}},
		want: "crush",
	},
	{
		name: "opencode native",
		how:  "measured",
		info: foregroundInfo{comm: "opencode", exe: "/usr/bin/opencode", argv: []string{"opencode"}},
		want: "opencode",
	},
	{
		name: "opencode from npm",
		how:  "documented",
		info: foregroundInfo{comm: "node", exe: "/usr/bin/node",
			argv: []string{"node", "/usr/lib/node_modules/opencode-ai/bin/opencode"}},
		want: "opencode",
	},
	{
		name: "gemini from a bun shim",
		how:  "measured",
		info: foregroundInfo{comm: "MainThread", exe: "/home/u/.vite-plus/js_runtime/node/24.21.0/bin/node",
			argv: []string{"/home/u/.vite-plus/js_runtime/js_runtime/node/24.21.0/bin/node", "/home/u/.bun/bin/gemini", "--help"}},
		want: "gemini-cli",
	},
	{
		name: "pi from a bun shim",
		how:  "measured",
		info: foregroundInfo{comm: "pi", exe: "/home/u/.vite-plus/js_runtime/node/24.21.0/bin/node",
			argv: []string{"pi"}},
		want: "pi",
	},
	{
		name: "amp from its installer",
		how:  "documented",
		info: foregroundInfo{comm: "amp", exe: "/home/u/.amp/bin/amp", argv: []string{"amp"}},
		want: "amp",
	},
	{
		name: "codex native",
		how:  "documented",
		info: foregroundInfo{comm: "codex", exe: "/usr/local/bin/codex", argv: []string{"codex"}},
		want: "codex",
	},
	{
		name: "codex from npm",
		how:  "documented",
		info: foregroundInfo{comm: "node", exe: "/usr/bin/node",
			argv: []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js"}},
		want: "codex",
	},
	{
		name: "aider as a uv tool",
		how:  "documented",
		info: foregroundInfo{comm: "aider", exe: "/home/u/.local/share/uv/tools/aider-chat/bin/python3.12",
			argv: []string{"/home/u/.local/share/uv/tools/aider-chat/bin/python", "/home/u/.local/bin/aider"}},
		want: "aider",
	},
	{
		name: "aider as a python module",
		how:  "documented",
		info: foregroundInfo{comm: "python3", exe: "/usr/bin/python3.13",
			argv: []string{"python3", "-m", "aider"}},
		want: "aider",
	},
	{
		name: "aider run by its package path",
		how:  "documented",
		info: foregroundInfo{comm: "python3", exe: "/usr/bin/python3.13",
			argv: []string{"python3", "/usr/lib/python3.13/site-packages/aider/main.py"}},
		want: "aider",
	},
	{
		name: "droid from the platform package",
		how:  "documented",
		info: foregroundInfo{comm: "droid", exe: "/home/u/n/node_modules/@factory/cli-linux-x64/bin/droid",
			argv: []string{"droid"}},
		want: "droid",
	},
	{
		name: "qwen from npm",
		how:  "documented",
		info: foregroundInfo{comm: "node", exe: "/usr/bin/node",
			argv: []string{"node", "/usr/lib/node_modules/@qwen-code/qwen-code/cli-entry.js"}},
		want: "qwen",
	},
	{
		name: "kilo npm shim",
		how:  "documented",
		info: foregroundInfo{comm: "node", exe: "/usr/bin/node",
			argv: []string{"node", "/usr/lib/node_modules/@kilocode/cli/bin/kilo"}},
		want: "kilo",
	},
	{
		name: "copilot node loader",
		how:  "documented",
		info: foregroundInfo{comm: "node", exe: "/usr/bin/node",
			argv: []string{"node", "/usr/lib/node_modules/@github/copilot/npm-loader.js"}},
		want: "copilot",
	},
	{
		name: "cline npm shim",
		how:  "documented",
		info: foregroundInfo{comm: "node", exe: "/usr/bin/node",
			argv: []string{"node", "/usr/lib/node_modules/cline/bin/cline"}},
		want: "cline",
	},
	{
		name: "npx opencode with a version pin",
		how:  "documented",
		info: foregroundInfo{comm: "npx", exe: "/usr/bin/node", argv: []string{"npx", "-y", "opencode@latest"}},
		want: "opencode",
	},
	{
		name: "a name from the user's own list",
		how:  "documented",
		info: foregroundInfo{comm: "mycli", exe: "/usr/local/bin/mycli", argv: []string{"mycli"}},
		want: nameList,
	},

	// --- True positives: the leader is a wrapper and the agent is behind it. ---
	{
		name: "claude behind sh -c with a trailing command",
		how:  "measured",
		info: foregroundInfo{comm: "sh", exe: "/usr/bin/bash", argv: []string{"sh", "-c", "claude; true"}},
		group: []foregroundInfo{{comm: "claude", exe: "/home/u/.local/share/claude/versions/2.1.266",
			argv: []string{"claude"}}},
		want: "claude-code",
	},
	{
		name: "claude behind a wrapper script that does not exec",
		how:  "measured",
		info: foregroundInfo{comm: "agent-launcher", exe: "/usr/bin/bash",
			argv: []string{"/bin/sh", "/home/u/bin/agent-launcher"}},
		group: []foregroundInfo{{comm: "claude", exe: "/home/u/.local/share/claude/versions/2.1.266",
			argv: []string{"claude"}}},
		want: "claude-code",
	},
	{
		name: "claude under timeout",
		how:  "measured",
		info: foregroundInfo{comm: "timeout", exe: "/usr/bin/timeout", argv: []string{"timeout", "60", "claude"}},
		group: []foregroundInfo{{comm: "claude", exe: "/home/u/.local/share/claude/versions/2.1.266",
			argv: []string{"claude"}}},
		want: "claude-code",
	},
	{
		name: "codex behind npx, which stays resident above it",
		how:  "documented",
		// npm sets its process title, which on Linux rewrites both comm and the
		// argument memory, so the command line reads back as the title alone.
		info: foregroundInfo{comm: "npm exec", exe: "/usr/bin/node", argv: []string{"npm exec"}},
		group: []foregroundInfo{{comm: "node", exe: "/usr/bin/node",
			argv: []string{"node", "/home/u/.npm/_npx/1a2b/node_modules/.bin/codex"}}},
		want: "codex",
	},
	{
		name: "aider behind uvx, which stays resident above it",
		how:  "documented",
		info: foregroundInfo{comm: "uvx", exe: "/home/u/.local/bin/uvx", argv: []string{"uvx", "aider-chat"}},
		group: []foregroundInfo{{comm: "aider", exe: "/home/u/.cache/uv/archive-v0/9f/bin/python3.12",
			argv: []string{"/home/u/.cache/uv/archive-v0/9f/bin/python", "/home/u/.cache/uv/archive-v0/9f/bin/aider"}}},
		want: "aider",
	},
	{
		name: "opencode behind a version manager's exec",
		how:  "documented",
		info: foregroundInfo{comm: "mise", exe: "/home/u/.local/bin/mise",
			argv: []string{"mise", "exec", "--", "opencode"}},
		group: []foregroundInfo{{comm: "opencode", exe: "/home/u/.local/share/mise/installs/opencode/1.0/bin/opencode",
			argv: []string{"opencode"}}},
		want: "opencode",
	},
	{
		name: "claude behind nix develop",
		how:  "documented",
		info: foregroundInfo{comm: "nix", exe: "/nix/store/abc-nix/bin/nix",
			argv: []string{"nix", "develop", "-c", "claude"}},
		group: []foregroundInfo{
			{comm: "bash", exe: "/nix/store/def-bash/bin/bash", argv: []string{"bash", "-c", "claude"}},
			{comm: "claude", exe: "/nix/store/ghi-claude/bin/claude", argv: []string{"claude"}},
		},
		want: "claude-code",
	},
	{
		name:  "a wrapper that runs no agent",
		how:   "documented",
		info:  foregroundInfo{comm: "timeout", exe: "/usr/bin/timeout", argv: []string{"timeout", "60", "make"}},
		group: []foregroundInfo{{comm: "make", exe: "/usr/bin/make", argv: []string{"make"}}},
	},
	{
		// A documented gap rather than a wish: build tools are not wrappers, and
		// walking every build's children on every tick would cost far more panes
		// than it would ever find agents in. A person who runs an agent this way
		// has the hook shim, OSC 9;4 and set-agent-state.
		name:  "crush run from a Makefile target (documented gap: build tools are not walked)",
		how:   "documented",
		info:  foregroundInfo{comm: "make", exe: "/usr/bin/make", argv: []string{"make", "run"}},
		group: []foregroundInfo{{comm: "crush", exe: "/home/u/dev/crush/crush", argv: []string{"./crush"}}},
	},
	{
		name:  "a wrapper whose child merely mentions an agent",
		how:   "documented",
		info:  foregroundInfo{comm: "sh", exe: "/usr/bin/bash", argv: []string{"sh", "-c", "grep claude log; true"}},
		group: []foregroundInfo{{comm: "grep", exe: "/usr/bin/grep", argv: []string{"grep", "claude", "log"}}},
	},
}

// TestDetectionCorpus holds the matcher to the corpus: every false positive must
// read as no agent, and every real launch must be attributed to its harness.
func TestDetectionCorpus(t *testing.T) {
	m := newAgentMatcher([]string{"mycli"})
	for _, c := range detectionCorpus {
		t.Run(c.name, func(t *testing.T) {
			info := c.info
			info.group = staticGroup(c.group)
			d, ok := m.identifyDetail(info)
			switch {
			case c.want == "" && ok:
				t.Errorf("%s case: identified as %q on %s, but this pane runs no agent", c.how, d.harness, d.rule)
			case c.want == nameList && (!ok || d.harness != ""):
				t.Errorf("%s case: identify = (%q, %v), want a name-list match", c.how, d.harness, ok)
			case c.want != "" && c.want != nameList && (!ok || d.harness != c.want):
				t.Errorf("%s case: identify = (%q, %v) on %q, want %q", c.how, d.harness, ok, d.rule, c.want)
			}
		})
	}
}

// TestDetectionCorpusNamesTheProcessThatDecided pins that a match through a
// wrapper is attributed to the process that matched, not to the wrapper, and
// that the wrapper chain is reported so the explanation can name it.
func TestDetectionCorpusNamesTheProcessThatDecided(t *testing.T) {
	m := newAgentMatcher(nil)
	for _, c := range detectionCorpus {
		if c.want == "" || len(c.group) == 0 {
			continue
		}
		info := c.info
		info.group = staticGroup(c.group)
		d, ok := m.identifyDetail(info)
		if !ok {
			t.Errorf("%s: not identified", c.name)
			continue
		}
		if d.proc.comm == c.info.comm {
			t.Errorf("%s: attributed to the wrapper %q rather than the agent behind it", c.name, d.proc.comm)
		}
		if len(d.via) == 0 || d.via[0] != agentBaseName(c.info.comm) {
			t.Errorf("%s: via = %v, want it to start with the wrapper %q", c.name, d.via, c.info.comm)
		}
	}
}

// staticGroup turns a fixed list into the iterator the resolver hands the
// matcher, so a corpus case stands in for a real process tree.
func staticGroup(members []foregroundInfo) func(yield func(foregroundInfo) bool) {
	if len(members) == 0 {
		return nil
	}
	return func(yield func(foregroundInfo) bool) {
		for _, m := range members {
			if !yield(m) {
				return
			}
		}
	}
}

// TestDetectionCorpusIsLabelled keeps every case honest about its provenance.
func TestDetectionCorpusIsLabelled(t *testing.T) {
	for _, c := range detectionCorpus {
		if c.how != "measured" && c.how != "documented" {
			t.Errorf("%s: how = %q, want measured or documented", c.name, c.how)
		}
		if strings.TrimSpace(c.name) == "" {
			t.Error("a corpus case has no name")
		}
	}
}
