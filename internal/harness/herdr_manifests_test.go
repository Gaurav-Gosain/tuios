package harness

import (
	"strings"
	"testing"
)

// The herdr-derived manifests, pinned the way the claude-code rules are: against
// screens written as the agent renders them, not against the substrings the
// rules happen to key on. The fixtures come from herdr's own manifests and the
// pane reads quoted in its comments (grok's are verbatim), which is the best
// evidence available for agents not installed here; the ones verified against a
// live pane are marked in their manifest.
func TestHerdrManifestsSpotTheirBlockers(t *testing.T) {
	r := testRegistry(t)
	for _, tc := range []struct {
		harness string
		name    string
		tail    []string
	}{
		{"amp", "tool approval", []string{
			"  Bash: go test ./...",
			"  Waiting for approval",
			"  [a] Allow all for this session   [d] Deny with feedback",
		}},
		// Captured from a live amp pane.
		{"amp", "login prompt", []string{
			"arch-btw% amp",
			"No API key found. Starting login flow...",
			"Would you like to log in to Amp? [(y)es, (n)o]:",
		}},
		{"antigravity", "permission prompt", []string{
			"Requesting permission for: run_command",
			"Do you want to proceed?",
			"  [y] yes   [n] no",
		}},
		{"cline", "act-mode command", []string{
			"[ACT MODE]",
			"Execute command?",
			"  > Yes",
			"    No",
		}},
		{"devin", "directory trust", []string{
			"Do you trust the authors of this directory?",
			"Devin may read files in this directory, even with untrusted content.",
			"  1. Yes, trust this directory",
			"  2. No",
		}},
		{"devin", "tool approval footer", []string{
			"  Approve once   Select   Confirm   Esc cancel",
		}},
		// Captured from a live copilot pane; the dialog the daemon classified
		// needs_input end to end.
		{"copilot", "folder trust dialog", []string{
			"│ Do you trust the files in this folder?                                     │",
			"│                                                                            │",
			"│ ❯ 1. Yes                                                                   │",
			"│   2. Yes, and remember this folder for future sessions                     │",
			"│   3. No (Esc)                                                              │",
			"│                                                                            │",
			"│ ↑/↓ to navigate · enter to select · esc to cancel                          │",
			"╰────────────────────────────────────────────────────────────────────────────╯",
		}},
		{"grok", "option dialog", []string{
			"┃  1 (●) Yes, proceed",
			"┃  2 (○) No, reject",
		}},
		{"grok", "permission footer hints", []string{
			"1/3:select │ Ctrl+o:yolo │ Ctrl+c:cancel",
		}},
		{"grok", "question dialog hints", []string{
			"Esc:unselect │ Tab:scrollback │ Shift+x:dismiss",
		}},
		{"hermes", "dangerous command", []string{
			"This command is dangerous:  rm -rf build/",
			"▸ 1. Allow once",
			"  2. Deny",
			"↑/↓ to select · Enter confirm",
		}},
		{"kilo", "permission banner", []string{
			"△ Permission required",
			"  bash: npm install",
		}},
		{"kimi", "approval panel", []string{
			"Run this command?  go build ./...",
			"  Approve  Reject  Revise",
			"  1/2/3 choose · ↵ confirm",
		}},
		{"kiro", "tool approval", []string{
			"Tool fs_write requires approval",
			"  y. Yes, single permission",
			"  t. Trust, always allow",
		}},
		{"maki", "permission prompt", []string{
			"Permission required: write src/main.rs",
			"  y allow · n deny",
		}},
		{"qoder", "user confirmation", []string{
			"Waiting for user confirmation",
			"  Allow   Reject",
		}},
		{"qwen", "folder trust", []string{
			"Do you trust this folder?",
			"  1. Trust folder (enter)",
			"  2. Don't trust (esc)",
		}},
		{"qwen", "tool confirmation", []string{
			"Shell Command Execution",
			"  go vet ./...",
			"Allow execution of: 'go'?",
			"● 1. Yes, allow once",
			"  2. Yes, allow always",
		}},
	} {
		t.Run(tc.harness+" "+tc.name, func(t *testing.T) {
			state, _, ok := r.Classify(tc.harness, tc.tail)
			if !ok {
				t.Fatalf("no rule matched a blocking prompt:\n%s", strings.Join(tc.tail, "\n"))
			}
			if state != "needs_input" {
				t.Errorf("classified as %q, want needs_input", state)
			}
		})
	}
}

// The working and idle chrome herdr also has rules for does not ship here, so a
// busy or resting pane must classify as nothing rather than as a blocker. This
// is the flip side of shipping only needs_input: silence is the contract.
func TestHerdrManifestsStayQuietOffTheirBlockers(t *testing.T) {
	r := testRegistry(t)
	for _, tc := range []struct {
		harness string
		name    string
		tail    []string
	}{
		{"devin", "working footer", []string{
			"Running tools   Esc to interrupt",
		}},
		{"grok", "working status line", []string{
			"⠧ Waiting on subagent… 2.8s   13s ⇣29.7k [stop]",
		}},
		{"grok", "idle footer", []string{
			"Ctrl+.:shortcuts",
		}},
		{"qwen", "working cancel hint", []string{
			"⠹ Reading files (12s · esc to cancel)",
		}},
		{"kimi", "moon spinner", []string{
			"🌕",
		}},
		{"cline", "resting prompt", []string{
			"cline is ready",
			"> ",
		}},
		{"amp", "streaming footer", []string{
			"╰ ⠧ streaming ─ esc to cancel",
		}},
	} {
		t.Run(tc.harness+" "+tc.name, func(t *testing.T) {
			if state, _, ok := r.Classify(tc.harness, tc.tail); ok {
				t.Errorf("classified %s chrome as %q; the rules for it were deliberately not shipped", tc.name, state)
			}
		})
	}
}

// Every herdr-derived manifest resolves the process shapes its agent really
// runs as, including the node shims whose comm never says the agent's name.
func TestHerdrManifestsIdentifyTheirProcesses(t *testing.T) {
	r := testRegistry(t)
	for _, c := range []struct {
		name string
		p    ProcInfo
		want string
	}{
		{"amp native install", ProcInfo{Comm: "amp", Argv: []string{"amp"}, Exe: "/home/u/.amp/bin/amp"}, "amp"},
		{"amp moved elsewhere is not claimed", ProcInfo{Comm: "amp", Argv: []string{"amp"}, Exe: "/usr/local/bin/amp"}, ""},
		{"antigravity", ProcInfo{Comm: "agy", Argv: []string{"agy"}, Exe: "/home/u/.local/bin/agy"}, "antigravity"},
		{"cline native child", ProcInfo{Comm: "cline", Argv: []string{"cline"}, Exe: ""}, "cline"},
		{"cline npm shim", ProcInfo{Comm: "node", Argv: []string{"node", "/n/node_modules/cline/bin/cline"}, Exe: "/usr/bin/node"}, "cline"},
		{"devin", ProcInfo{Comm: "devin", Argv: []string{"devin"}, Exe: "/home/u/.local/bin/devin"}, "devin"},
		{"copilot node loader", ProcInfo{Comm: "node", Argv: []string{"node", "/n/node_modules/@github/copilot/npm-loader.js"}, Exe: "/usr/bin/node"}, "copilot"},
		{"copilot native child", ProcInfo{Comm: "copilot", Argv: []string{"copilot"}, Exe: ""}, "copilot"},
		{"grok native install", ProcInfo{Comm: "grok", Argv: []string{"grok"}, Exe: "/home/u/.grok/bin/grok"}, "grok"},
		{"grok elsewhere is not claimed", ProcInfo{Comm: "grok", Argv: []string{"grok"}, Exe: "/usr/bin/grok"}, ""},
		{"hermes python launcher", ProcInfo{Comm: "hermes", Argv: []string{"python3", "/home/u/.hermes/hermes-agent/main.py"}, Exe: "/usr/bin/python3.12"}, "hermes"},
		{"kilo npm shim", ProcInfo{Comm: "node", Argv: []string{"node", "/n/node_modules/@kilocode/cli/bin/kilo"}, Exe: "/usr/bin/node"}, "kilo"},
		{"kilo native child", ProcInfo{Comm: "kilo", Argv: []string{"kilo"}, Exe: "/n/node_modules/@kilocode/cli-linux-x64/bin/kilo"}, "kilo"},
		{"kilo the text editor is not claimed", ProcInfo{Comm: "kilo", Argv: []string{"kilo", "notes.txt"}, Exe: "/usr/local/bin/kilo"}, ""},
		{"kimi native install", ProcInfo{Comm: "kimi", Argv: []string{"kimi"}, Exe: "/home/u/.kimi-code/bin/kimi"}, "kimi"},
		{"kimi python cli", ProcInfo{Comm: "kimi", Argv: []string{"kimi"}, Exe: "/home/u/.local/share/uv/tools/kimi-cli/bin/python3.13"}, "kimi"},
		{"kiro", ProcInfo{Comm: "kiro-cli", Argv: []string{"kiro-cli"}, Exe: "/home/u/.local/bin/kiro-cli"}, "kiro"},
		{"maki", ProcInfo{Comm: "maki", Argv: []string{"maki"}, Exe: "/home/u/.cargo/bin/maki"}, "maki"},
		{"qoder", ProcInfo{Comm: "node", Argv: []string{"node", "/n/node_modules/@qoder-ai/qodercli/bundle/qodercli.js"}, Exe: "/usr/bin/node"}, "qoder"},
		{"qwen from npm", ProcInfo{Comm: "node", Argv: []string{"node", "/n/node_modules/@qwen-code/qwen-code/cli-entry.js"}, Exe: "/usr/bin/node"}, "qwen"},
		{"a static binary named qwen is not claimed", ProcInfo{Comm: "qwen", Argv: []string{"qwen"}, Exe: "/usr/local/bin/qwen"}, ""},
	} {
		got, _, ok := r.IdentifyDetail(c.p)
		if c.want == "" {
			if ok {
				t.Errorf("%s: identified as %q, want no harness", c.name, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("%s: identified as %q (ok=%v), want %q", c.name, got, ok, c.want)
		}
	}
}
