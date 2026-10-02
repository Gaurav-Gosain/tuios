package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuitest"
)

// tuios-slim runs a subset of this suite. TUIOS_E2E_BIN names the slim binary
// and slim_suite.txt lists the files the subset is taken from; the slim job
// in .github/workflows/e2e-tui.yml runs it. A test of a feature that
// tuios-slim leaves out skips itself there, with the reason, through
// skipIfSlimLacks. The tests of the slim build itself are in this file and
// skip on the full build.

// slimBinary is true when the binary under test says "slim" in its version.
// runE2E sets it.
var slimBinary bool

// detectSlim reports whether bin is tuios-slim, from the first line of its
// version report.
func detectSlim(bin string) bool {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(string(out), "\n")
	return strings.Contains(first, " slim ")
}

// slimLacks maps the start of a test file's name to the feature its tests
// exercise that tuios-slim leaves out. See docs/SLIM.md.
var slimLacks = []struct{ prefix, feature string }{
	{"agent_", "the agent features"},
	{"ask_human", "the agent features"},
	{"fleet_", "the agent features"},
	{"harness_integrations", "the agent integrations"},
	{"human_origin", "the agent features"},
	{"inbox_", "the Inbox"},
	{"manifest_input", "agent detection"},
	{"rail_agent", "the agent features"},
	{"selector_", "the agent selectors"},
	{"witness_detector", "agent detection"},
	{"review", "the review"},
	{"fan_", "fan-out"},
	{"sidebar_worktrees", "worktrees"},
	{"screenshot_", "screenshots"},
	{"kitty_capture_mode", "screenshots"},
	{"settings_effect_picker", "the screen saver"},
	{"tape_", "project tapes"},
	{"host_", "hosts"},
	{"hosts_", "hosts"},
	{"cross_host", "hosts"},
	{"global_", "hosts"},
	{"remote_", "hosts"},
	{"sidebar_hosts", "hosts"},
	{"ssh_", "the SSH server"},
	{"herdr_", "the herdr API"},
	{"mcp_", "the MCP server"},
	{"tmux_shim", "the tmux shim"},
}

// slimLacksTests names tests in other files that reach a feature tuios-slim
// leaves out in a way the harness cannot see, such as a command typed into a
// pane.
var slimLacksTests = map[string]string{
	"TestWindowSizeTmuxShim": "the tmux shim",
	// These read the pane's grid size with tuios screenshot.
	"TestScrollbackResizeNarrowKeepsScreenLine":  "screenshots",
	"TestScrollbackResizeKeepsOutputUnderALoneA": "screenshots",
}

// slimDroppedCommands are the commands tuios-slim leaves out, as
// cmd/tuios/slim_commands.go lists them. A test that runs one through a
// harness helper skips on tuios-slim.
var slimDroppedCommands = map[string]bool{
	"agent-hook": true, "agent-log": true, "agent-proto": true, "agent-statusline": true,
	"ask-agent": true, "ask-human": true, "doctor": true,
	"explain-agent-detect": true, "explain-agent-screen": true,
	"fan": true, "get-agent-state": true, "hosts": true, "integration": true,
	"list-agents": true, "list-attention": true, "mcp": true, "notification": true, "pane": true,
	"peek-prompt": true, "queue": true, "read-agent-messages": true, "respond": true,
	"resume-agent": true, "review": true, "run": true, "screenshot": true, "send-agent-message": true,
	"set-agent-meta": true, "set-agent-session": true, "set-agent-state": true,
	"ssh": true, "start-agent": true, "stash": true, "stdio-proxy": true, "subscribe": true,
	"tape": true, "tmux": true, "tmux-pane": true, "tmux-shim": true, "update": true, "worktree": true,
}

// skipIfSlimRuns skips the calling test when the binary under test is
// tuios-slim and args run a command it leaves out.
func skipIfSlimRuns(t *testing.T, args []string) {
	t.Helper()
	if !slimBinary || requiresSlim[t.Name()] {
		return
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if slimDroppedCommands[a] {
			t.Skipf("tuios-slim leaves out tuios %s; see docs/SLIM.md", a)
		}
		return
	}
}

// requiresSlim are the tests that run dropped commands on purpose.
var requiresSlim = map[string]bool{
	"TestSlimDroppedCommandSaysSo":          true,
	"TestSlimRunsAnInstalledExtension":      true,
	"TestFullCLIOnASlimDaemon":              true,
	"TestSlimClientKeepsListeningAfterMail": true,
}

// skipIfSlimLacks skips the calling test when the binary under test is
// tuios-slim and the test exercises a feature tuios-slim leaves out. The
// harness calls it from every helper that starts tuios, so a test does not
// have to.
func skipIfSlimLacks(t *testing.T) {
	t.Helper()
	if !slimBinary {
		return
	}
	if what, ok := slimLacksTests[t.Name()]; ok {
		t.Skipf("tuios-slim leaves out %s; see docs/SLIM.md", what)
	}
	file := testFile()
	for _, l := range slimLacks {
		if strings.HasPrefix(file, l.prefix) {
			t.Skipf("tuios-slim leaves out %s (%s); see docs/SLIM.md", l.feature, file)
		}
	}
}

// testFile is the base name of the file that holds the running Test function,
// found on the call stack.
func testFile() string {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if i := strings.LastIndex(f.Function, ".Test"); i >= 0 && strings.HasSuffix(f.File, "_test.go") {
			return filepath.Base(f.File)
		}
		if !more {
			return ""
		}
	}
}

// requireSlim skips a test of tuios-slim itself on the full build.
func requireSlim(t *testing.T) {
	t.Helper()
	if !slimBinary {
		t.Skip("needs tuios-slim: point TUIOS_E2E_BIN at a binary built with -tags slim")
	}
}

// TestSlimDroppedCommandSaysSo runs commands tuios-slim leaves out and checks
// each fails with the one line that names it and says how to get it.
func TestSlimDroppedCommandSaysSo(t *testing.T) {
	requireSlim(t)
	base := t.TempDir()
	for _, args := range [][]string{
		{"ssh", "--port", "2222"},
		{"mcp"},
		{"list-agents", "--json"},
		{"tape", "play", "demo.tape"},
	} {
		out, err := tuiosCLI(t, base, args...)
		if err == nil {
			t.Fatalf("tuios-slim %s succeeded:\n%s", strings.Join(args, " "), out)
		}
		want := args[0] + " is not in tuios-slim. Install the full tuios to use this command."
		if strings.TrimSpace(out) != want {
			t.Errorf("tuios-slim %s printed\n%q\nwant the one line\n%q", strings.Join(args, " "), out, want)
		}
	}
}

// TestSlimRunsAnInstalledExtension installs a stand-in extension binary in
// the extension directory and checks that tuios-slim runs it with the user's
// arguments, the daemon socket and the protocol version, and passes its exit
// status back. A stand-in built for another tuios version is refused.
func TestSlimRunsAnInstalledExtension(t *testing.T) {
	requireSlim(t)
	base := t.TempDir()
	version := slimVersion(t)
	extDir := filepath.Join(xdgDir(base, "XDG_DATA_HOME"), "tuios", "ext")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	info := func(v string) string {
		b, _ := json.Marshal(map[string]any{"protocol": 1, "name": "ssh", "tuios_version": v})
		return string(b)
	}
	write := func(v string) {
		script := "#!/bin/sh\n" +
			"if [ \"$1\" = --tuios-extension-info ]; then echo '" + info(v) + "'; exit 0; fi\n" +
			"echo \"ext args: $*\"\n" +
			"echo \"ext protocol: $TUIOS_EXT_PROTOCOL\"\n" +
			"echo \"ext socket set: ${TUIOS_SOCKET:+yes}\"\n" +
			"exit 7\n"
		if err := os.WriteFile(filepath.Join(extDir, "tuios-ssh"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	write(version)
	out, err := tuiosCLI(t, base, "ssh", "--port", "2222")
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 7 {
		t.Fatalf("tuios-slim ssh with the extension installed: err %v, want exit status 7\n%s", err, out)
	}
	for _, want := range []string{"ext args: --port 2222", "ext protocol: 1", "ext socket set: yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("the extension's output has no %q:\n%s", want, out)
		}
	}

	write("v0.0.1")
	out, err = tuiosCLI(t, base, "ssh")
	if err == nil || !strings.Contains(out, "is built for tuios v0.0.1") || !strings.Contains(out, "Update both to the same version.") {
		t.Fatalf("an extension for another version ran or was not refused clearly: err %v\n%s", err, out)
	}
	if strings.Contains(out, "ext args:") {
		t.Fatalf("the refused extension ran:\n%s", out)
	}
}

// slimVersion is the version the binary under test was built as, from the
// first word after "version" in its report.
func slimVersion(t *testing.T) string {
	t.Helper()
	out, err := exec.Command(tuiosBin, "--version").Output()
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "version" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	t.Fatalf("no version in %q", out)
	return ""
}

// TestFullCLIOnASlimDaemon runs the full tuios's commands against a daemon
// that tuios-slim started. The commands the slim daemon has work, and one it
// leaves out fails with the line that names it. TUIOS_E2E_FULL_BIN names the
// full binary; the slim job builds it.
func TestFullCLIOnASlimDaemon(t *testing.T) {
	requireSlim(t)
	full := os.Getenv("TUIOS_E2E_FULL_BIN")
	if full == "" {
		t.Skip("TUIOS_E2E_FULL_BIN is not set")
	}
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "mixed", "--detach"); err != nil {
		t.Fatalf("tuios-slim new: %v\n%s", err, out)
	}
	withFull := func(args ...string) (string, error) {
		var out string
		var err error
		prev := tuiosBin
		tuiosBin = full
		defer func() { tuiosBin = prev }()
		out, err = tuiosCLI(t, base, args...)
		return out, err
	}
	out, err := withFull("ls")
	if err != nil || !strings.Contains(out, "mixed") {
		t.Fatalf("full tuios ls on the slim daemon: %v\n%s", err, out)
	}
	if out, err := withFull("send-text", "-s", "mixed", "echo hi\n"); err != nil {
		t.Fatalf("full tuios send-text on the slim daemon: %v\n%s", err, out)
	}
	for _, c := range []struct {
		args    []string
		feature string
	}{
		{[]string{"list-agents", "-s", "mixed"}, "list-agents"},
		{[]string{"list-attention"}, "list-attention"},
		{[]string{"wait-for", "agent-state", "-s", "mixed", "--until", "idle", "--timeout", "1000"}, "wait-for agent-state"},
	} {
		out, err = withFull(c.args...)
		if err == nil {
			t.Fatalf("full tuios %s on the slim daemon succeeded:\n%s", strings.Join(c.args, " "), out)
		}
		for _, want := range []string{
			c.feature + " is not in tuios-slim. Install the full tuios to use it.",
			"the running daemon is tuios-slim, which leaves out " + c.feature + ".",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("full tuios %s on the slim daemon printed\n%s\nwant %q", strings.Join(c.args, " "), out, want)
			}
		}
	}
}

// TestSlimClientKeepsListeningAfterMail attaches tuios-slim to a full
// daemon, has the full CLI leave mail for the person, and then attaches a
// smaller second client. tuios-slim has no mail and drops the mail events.
// It must keep reading the client event channel, so it still follows the
// session to the smaller size. TUIOS_E2E_FULL_BIN names the full binary.
func TestSlimClientKeepsListeningAfterMail(t *testing.T) {
	requireSlim(t)
	full := os.Getenv("TUIOS_E2E_FULL_BIN")
	if full == "" {
		t.Skip("TUIOS_E2E_FULL_BIN is not set")
	}
	const name = "slim-mail"
	base := t.TempDir()
	withFull := func(f func()) {
		prev := tuiosBin
		tuiosBin = full
		defer func() { tuiosBin = prev }()
		f()
	}
	withFull(func() {
		killDaemon(t, base)
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("full tuios new: %v\n%s", err, out)
		}
	})
	term := attachIn(t, base, name, startOpts{})

	withFull(func() {
		for i := range 3 {
			if out, err := tuiosCLI(t, base, "send-agent-message", "-s", name, "-w", "human",
				"--subject", fmt.Sprintf("note %d", i), "text"); err != nil {
				t.Fatalf("full tuios send-agent-message: %v\n%s", err, out)
			}
		}
	})
	time.Sleep(500 * time.Millisecond)
	if strings.Contains(term.Screen().Text(), "to you: note") {
		t.Fatalf("tuios-slim showed mail it has no Inbox for:\n%s", term.Snapshot())
	}

	// A smaller second client makes the session smaller. The first client
	// hears that as a resize event on the same channel as the mail, and
	// draws its frame 100 columns wide.
	if w := frameWidth(term.Screen()); w <= 100 {
		t.Fatalf("fixture: the first client's frame is %d wide before the second client; it must be wider than 100", w)
	}
	_ = attachIn(t, base, name, startOpts{cols: 100, rows: 30})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return frameWidth(s) <= 100 }, uiTimeout); err != nil {
		t.Fatalf("tuios-slim stopped hearing client events after the mail: %v\n%s", err, term.Snapshot())
	}
}

// frameWidth is the width of the pane frame's top border on the first row.
func frameWidth(s tuitest.Screen) int {
	line, _, _ := strings.Cut(s.Text(), "\n")
	return utf8.RuneCountInString(strings.TrimRight(line, " "))
}
