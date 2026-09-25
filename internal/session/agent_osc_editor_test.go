package session

import (
	"slices"
	"testing"
	"time"
)

// nvimProgressBytes is what Neovim 0.12 writes for one progress message: its
// nvim.progress autocmd (runtime/lua/vim/_core/defaults.lua) sends
// OSC 9;4;1;<percent> while the message is running and OSC 9;4;0;0 when it
// ends, both ST-terminated. Any progress message does it: an LSP client or a
// plugin echoing one, vim.pack, :checkhealth.
var nvimProgressBytes = []string{
	"\x1b]9;4;1;0\x1b\\",
	"\x1b]9;4;1;40\x1b\\",
	"\x1b]9;4;1;100\x1b\\",
	"\x1b]9;4;0;0\x1b\\",
}

// brewUpgradeOutput is the tail of a `brew bundle install --upgrade` run as a
// pane printed it, drawn the way Homebrew draws it: the cursor hidden, the
// download line redrawn inside synchronized updates, then plain text naming
// claude, a check mark, a beer mug (escaped here) and a starship prompt.
const brewUpgradeOutput = "\x1b[?25l" +
	"\x1b[?2026h\x1b[0G\x1b[K\x1b[34m⠋\x1b[0m Cask claude-code@latest (2.1.281)   Downloading\x1b[?2026l" +
	"\x1b[0G\x1b[K\x1b[32m\u2714\ufe0e\x1b[0m Cask claude-code@latest (2.1.281)   Downloaded  220.9MB/220.9MB" +
	"\x1b[?25h" +
	"==> Upgrading claude-code@latest\r\n" +
	"  2.1.278 -> 2.1.281\r\n" +
	"==> Unlinking Binary '/opt/homebrew/bin/claude'\r\n" +
	"==> Linking Binary 'claude' to '/opt/homebrew/bin/claude'\r\n" +
	"==> Purging files for version 2.1.278 of Cask claude-code@latest\r\n" +
	"\U0001F37A  claude-code@latest was successfully upgraded!\r\n" +
	"Skipping install of scraped cask. It is already installed.\r\n" +
	"Using scraped\r\n" +
	"==> This operation has freed approximately 7.3GB of disk space.\r\n" +
	"❯ "

// TestEditorProgressThenBrewLeavesAPlainPane is the regression test for a shell
// pane listed as an agent after `nvim Brewfile` and then `brew bundle`. Neovim
// sent its OSC 9;4 progress pair, which moved the pane from no state to working
// and then idle, attributed to no harness. Nothing cleared it afterwards: the
// pane sat at its prompt listed as an idle agent with source osc, through the
// whole brew run and after it. The brew output itself was never the trigger,
// and it is replayed here to show it stays inert.
//
// The bytes go through the emulator and are applied the way the daemon's output
// handler applies them, and a detection tick then sees the pane back at its
// shell. A pane attributed to a harness gets the same bytes as the positive
// control: there the sequence still drives state.
func TestEditorProgressThenBrewLeavesAPlainPane(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, plain, agent := twoWindowSession(t, d, "up")
	c := dialVerb(t, sp)
	attributeHarness(t, sess, agent, "claude-code")

	// feed writes one chunk to a pane's emulator and then does what the
	// daemon's output handler does with a report the emulator parked.
	feed := func(windowID, chunk string) {
		t.Helper()
		ptyID := ptyIDOfWindow(t, sess, windowID)
		pty := sess.GetPTY(ptyID)
		feedVT(t, pty, chunk)
		if state, ok := pty.takeAgentProgress(); ok {
			sess.applyPaneProgress(ptyID, windowID, state, d.agentMatcher.registry)
		}
	}
	for _, win := range []string{plain, agent} {
		for _, chunk := range nvimProgressBytes {
			feed(win, chunk)
		}
		feed(win, brewUpgradeOutput)
	}
	sess.settleAgentHolds(time.Now().Add(time.Hour))

	// Both panes are back at fish. The plain pane never ran an agent binary;
	// the attributed one is left to what its harness said.
	atShell := foregroundInfo{comm: "fish", argv: []string{"/opt/homebrew/bin/fish"}, exe: "/opt/homebrew/bin/fish", pid: 4242, shellPID: 4242}
	table := map[string]fakeProc{
		ptyIDOfWindow(t, sess, plain): {info: atShell, running: true},
		ptyIDOfWindow(t, sess, agent): {info: atShell, running: true},
	}
	sess.applyAgentDetection(fakeResolver(table), d.agentMatcher.identifyDetail)

	if got := agentStateOf(t, sess, plain); got != AgentStateNone {
		t.Fatalf("the plain pane has agent state %q after an editor's progress bar and brew output, want none", got)
	}
	if got := agentStateOf(t, sess, agent); got != AgentStateIdle {
		t.Fatalf("the attributed pane = %q after the same bytes, want idle from the cleared bar", got)
	}

	res := result(t, c.call(t, `{"id":1,"verb":"list-agents","params":{"session":"up"}}`))
	rows, _ := res["agents"].([]any)
	var listed []string
	for _, r := range rows {
		listed = append(listed, r.(map[string]any)["window_id"].(string))
	}
	if slices.Contains(listed, plain) {
		t.Fatalf("list-agents lists the plain pane: %v", rows)
	}
	if !slices.Contains(listed, agent) {
		t.Fatalf("list-agents does not list the attributed pane: %v", rows)
	}

	// The report is still recorded on the plain pane, for anything that shows
	// progress as progress. "4;0" is what the reported pane held.
	if got := sess.GetPTY(ptyIDOfWindow(t, sess, plain)).ProgressText(); got != "4;0" {
		t.Fatalf("the plain pane's progress text = %q, want 4;0", got)
	}
}
