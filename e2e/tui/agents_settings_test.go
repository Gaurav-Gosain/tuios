package tuie2e

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const fakeClaude = `#!/bin/sh
printf 'fake claude ready>\n'
while read -r line; do :; done
`

func agentsPageFixture(t *testing.T, off bool) (string, string) {
	t.Helper()
	return agentsFixtureWith(t, agentsFixture{off: off, aged: true})
}

type agentsFixture struct {
	off     bool
	command string
	aged    bool
	none    bool
}

func agentsFixtureWith(t *testing.T, o agentsFixture) (string, string) {
	t.Helper()
	off := o.off
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+agentFreePath("/usr/bin", "/bin"))

	cfg := configPathIn(base)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[daemon]\nagent_detect_seconds = 1\n"
	if off {
		body += "\n[agents]\nenabled = false\n"
	}
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	home := xdgDir(base, "HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if o.none {
		if out, err := tuiosCLI(t, base, "new", "agents", "--detach"); err != nil {
			t.Fatalf("create the session: %v\n%s", err, out)
		}
		return base, settings
	}
	install := []string{"integration", "install", "claude-code"}
	if o.command != "" {
		install = append(install, "--command", o.command)
	}
	if out, err := tuiosCLI(t, base, install...); err != nil {
		t.Fatalf("install the integration: %v\n%s", err, out)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	cur := claudeIntegrationStatus(t, base, o.command)
	if cur.Path != settings {
		t.Fatalf("the integration is at %s, not in the test's home %s", cur.Path, settings)
	}
	if cur.Version < 2 || !cur.Current {
		t.Fatalf("the fresh install is not current: %+v", cur)
	}
	if o.aged {
		old := strings.ReplaceAll(string(data), "--integration "+itoa(cur.Version), "--integration "+itoa(cur.Version-1))
		if old == string(data) {
			t.Fatalf("no version marker to age in %s:\n%s", settings, data)
		}
		if err := os.WriteFile(settings, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		if st := claudeIntegrationStatus(t, base, o.command); !st.Installed || st.Current || st.Version != cur.Version-1 {
			t.Fatalf("the aged file does not read as out of date: %+v", st)
		}
	}

	if out, err := tuiosCLI(t, base, "new", "agents", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	return base, settings
}

type claudeStatus struct {
	Path         string `json:"path"`
	Installed    bool   `json:"installed"`
	Current      bool   `json:"current"`
	Version      int    `json:"version"`
	WantVersion  int    `json:"want_version"`
	Program      string `json:"program"`
	OtherProgram bool   `json:"other_program"`
}

func claudeIntegrationVersion(t *testing.T, base string) claudeStatus {
	t.Helper()
	return claudeIntegrationStatus(t, base, "")
}

func claudeIntegrationStatus(t *testing.T, base, command string) claudeStatus {
	t.Helper()
	args := []string{"integration", "status", "claude-code", "--json"}
	if command != "" {
		args = append(args, "--command", command)
	}
	out, err := tuiosCLI(t, base, args...)
	if err != nil {
		t.Fatalf("integration status: %v\n%s", err, out)
	}
	var sts []claudeStatus
	if err := json.Unmarshal([]byte(out), &sts); err != nil || len(sts) != 1 {
		t.Fatalf("integration status output: %v\n%s", err, out)
	}
	return sts[0]
}

func agentsRow(s tuitest.Screen, label string) string {
	row := findRow(s, label)
	if row < 0 {
		return ""
	}
	return s.Line(row)
}

func waitAgentsRow(t *testing.T, term *tuitest.Terminal, label, why string, want ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		line := agentsRow(s, label)
		if line == "" {
			return false
		}
		for _, w := range want {
			if !strings.Contains(line, w) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("%s: row %q never showed %q: %v\n%s", why, label, want, err, term.Snapshot())
	}
}

func clickAgentsRow(t *testing.T, term *tuitest.Terminal, label string) {
	t.Helper()
	s := term.Screen()
	row := findRow(s, label)
	if row < 0 {
		t.Fatalf("the panel drew no row %q\n%s", label, term.Snapshot())
	}
	col := strings.Index(s.Line(row), label)
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
}

func openAgentsTab(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "A"); err != nil {
		t.Fatal(err)
	}
	waitAgentsRow(t, term, "Claude Code", "the prefix key opened no Agents tab")
}

// TestAgentsSettingsUpdatesAndUninstalls opens the Agents tab with the prefix
// key, reads the out of date Claude Code integration off it, updates it with a
// click and enter, and then uninstalls it, reading the file on disk after each.
func TestAgentsSettingsUpdatesAndUninstalls(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	base, settings := agentsPageFixture(t, false)
	term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
	want := claudeIntegrationVersion(t, base).WantVersion

	openAgentsTab(t, term)
	waitAgentsRow(t, term, "Claude Code", "the tab does not say the integration is out of date", "out of date", "on PATH")
	waitAgentsRow(t, term, "Codex", "the tab does not say Codex is not installed", "not installed", "not on PATH")
	dir := artifactDir(t)
	railShot(t, term, "agents-settings-out-of-date")

	clickAgentsRow(t, term, "Claude Code")
	waitAgentsRow(t, term, "Update Claude Code", "a click on the row opened no update action", "settings.json")
	waitAgentsRow(t, term, "Uninstall Claude Code", "a click on the row opened no uninstall action", "settings.json")
	if err := term.WaitForText(".claude/settings.json", uiTimeout); err != nil {
		t.Fatalf("the update action does not name the file it changes: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "agents-settings-actions")

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	waitAgentsRow(t, term, "Claude Code", "esc did not go back to the list", "out of date")
	if st := claudeIntegrationVersion(t, base); st.Current {
		t.Fatalf("ASSERTION: going back changed the integration: %+v", st)
	}

	clickAgentsRow(t, term, "Claude Code")
	waitAgentsRow(t, term, "› Update Claude Code", "the cursor is not on the update action")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Updated in ~/.claude/settings.json.", uiTimeout); err != nil {
		t.Fatalf("the update said nothing: %v\n%s", err, term.Snapshot())
	}
	waitAgentsRow(t, term, "Claude Code", "the row does not say installed after the update", "installed")
	saveArtifact(t, term, dir, "agents-settings-updated")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "--integration "+itoa(want)) || strings.Contains(string(data), "--integration "+itoa(want-1)) {
		t.Fatalf("ASSERTION: the update did not rewrite the file to v%d:\n%s", want, data)
	}
	if st := claudeIntegrationVersion(t, base); !st.Installed || !st.Current {
		t.Fatalf("ASSERTION: the CLI does not read the updated integration as current: %+v", st)
	}

	clickAgentsRow(t, term, "Claude Code")
	waitAgentsRow(t, term, "› Uninstall Claude Code", "the cursor is not on the uninstall action")
	if strings.Contains(term.Screen().Text(), "Update Claude Code") {
		t.Fatalf("ASSERTION: a current integration offers an update\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Claude Code integration is removed from", uiTimeout); err != nil {
		t.Fatalf("the uninstall said nothing: %v\n%s", err, term.Snapshot())
	}
	waitAgentsRow(t, term, "Claude Code", "the row does not say not installed after the uninstall", "not installed")
	saveArtifact(t, term, dir, "agents-settings-uninstalled")
	data, err = os.ReadFile(settings)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "agent-hook") {
		t.Fatalf("ASSERTION: the uninstall left tuios's hooks in the file:\n%s", data)
	}
	if st := claudeIntegrationVersion(t, base); st.Installed {
		t.Fatalf("ASSERTION: the CLI still reads an integration: %+v", st)
	}
	alive(t, term, "after the uninstall")
}

func paneHarness(t *testing.T, base, session string) map[string]string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-agents", "-s", session, "--json")
	if err != nil {
		t.Fatalf("list-agents: %v\n%s", err, out)
	}
	var listing struct {
		Agents []struct {
			ID      string `json:"window_id"`
			Harness string `json:"harness_id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("list-agents JSON: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, w := range listing.Agents {
		got[w.ID] = w.Harness
	}
	return got
}

func waitClaudePanes(t *testing.T, base string, n int) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		count := 0
		for _, h := range paneHarness(t, base, "agents") {
			if h == "claude-code" {
				count++
			}
		}
		if count >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon took %d panes for Claude Code, want %d", count, n)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

const integrationNotice = "Claude Code integration is out of date."

// TestAgentsIntegrationNoticeOncePerRun starts the stand-in claude in a pane
// and waits for the toast that names the fix. It dismisses the toast, starts a
// second claude pane, and watches for the toast for some seconds: it does not
// come back. The palette entry then opens the Agents tab. Last, in a home with
// no integration, the toast opens a question that names the file: esc writes
// nothing, Install writes the hooks, and the toast does not come back for a
// second pane.
func TestAgentsIntegrationNoticeOncePerRun(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	base, _ := agentsPageFixture(t, false)
	term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})

	if out, err := tuiosCLI(t, base, "send-text", "-s", "agents", "claude\n"); err != nil {
		t.Fatalf("start the stand-in: %v\n%s", err, out)
	}
	waitClaudePanes(t, base, 1)
	if err := term.WaitForText(integrationNotice, uiTimeout); err != nil {
		t.Fatalf("no toast for the out of date integration: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), integrationNotice+" ctrl+b j updates it") {
		t.Logf("the toast is cut on screen:\n%s", term.Snapshot())
	}
	railShot(t, term, "agents-notice")

	out, err := tuiosCLI(t, base, "doctor", "agents")
	if err != nil {
		t.Fatalf("doctor agents: %v\n%s", err, out)
	}
	if strings.Contains(out, "Every agent pane") || !strings.Contains(out, "Agent panes whose integration is out of date:") ||
		!strings.Contains(out, "runs claude-code") || !strings.Contains(out, "Update it with: tuios integration install claude-code") {
		t.Fatalf("ASSERTION: the doctor does not name the pane with the out of date integration:\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "doctor-agents.txt"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}

	clickNoticeDismiss(t, term, integrationNotice)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), integrationNotice) }, uiTimeout); err != nil {
		t.Fatalf("the click did not dismiss the toast: %v\n%s", err, term.Snapshot())
	}
	if !waitDismissalStored(base, true) {
		data, _ := os.ReadFile(sidebarStatePath(base))
		t.Fatalf("ASSERTION: the dismissal was never stored:\n%s", data)
	}

	second := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(second.Screen().Text(), integrationNotice) {
			t.Fatalf("ASSERTION: a new client showed the dismissed toast again\n%s", second.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := second.Close(); err != nil {
		t.Logf("close the second client: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "agents"); err != nil {
		t.Fatalf("new window: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "agents", "claude\n"); err != nil {
		t.Fatalf("start the second stand-in: %v\n%s", err, out)
	}
	waitClaudePanes(t, base, 2)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(term.Screen().Text(), integrationNotice) {
			t.Fatalf("ASSERTION: the toast came back for a second Claude Code pane\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "P"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("the palette did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("install and update integrations"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Agents: settings", uiTimeout); err != nil {
		t.Fatalf("the palette has no Agents settings entry: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitAgentsRow(t, term, "Claude Code", "the palette entry did not open the tab", "out of date")

	installFromTheNotice(t)
}

const noticeNotInstalled = "Claude Code integration is not installed."

const (
	installQuestion = "Install the Claude Code integration?"
	installEffect   = "This adds hooks to ~/.claude/settings.json."
)

func waitInstallNotice(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.WaitForText(noticeNotInstalled, uiTimeout); err != nil {
		t.Fatalf("no toast for the missing integration: %v\n%s", err, term.Snapshot())
	}
	if s := term.Screen(); !strings.Contains(s.Line(findRow(s, noticeNotInstalled)), noticeNotInstalled+" ctrl+b j") {
		t.Fatalf("ASSERTION: the toast does not name the key that installs\n%s", term.Snapshot())
	}
}

func noHooksIn(t *testing.T, settings, why string) {
	t.Helper()
	data, err := os.ReadFile(settings)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "agent-hook") {
		t.Fatalf("ASSERTION: %s:\n%s", why, data)
	}
}

func installFromTheNotice(t *testing.T) {
	base, settings := agentsFixtureWith(t, agentsFixture{none: true})
	if st := claudeIntegrationVersion(t, base); st.Installed {
		t.Fatalf("the fixture has an integration: %+v", st)
	}
	dir := artifactDir(t)
	first := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
	if out, err := tuiosCLI(t, base, "send-text", "-s", "agents", "claude\n"); err != nil {
		t.Fatalf("start the stand-in: %v\n%s", err, out)
	}
	waitClaudePanes(t, base, 1)
	waitInstallNotice(t, first)
	railShot(t, first, "agents-notice-install")

	if err := first.SendKeys(tuitest.Ctrl('b'), "j", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := first.WaitForText(installQuestion, uiTimeout); err != nil {
		noHooksIn(t, settings, "an enter sent before the question was drawn installed the integration")
		t.Fatalf("ASSERTION: ctrl+b j on the toast opened no question: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(time.Second)
	if !strings.Contains(first.Screen().Text(), installEffect) {
		t.Fatalf("ASSERTION: the question does not name the file, or the enter in flight closed it\n%s", first.Snapshot())
	}
	noHooksIn(t, settings, "an enter sent before the question was drawn installed the integration")
	railShot(t, first, "agents-notice-ask")
	if err := first.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := first.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), installQuestion) }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the question: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(time.Second)
	noHooksIn(t, settings, "esc on the question wrote the file")
	if st := claudeIntegrationVersion(t, base); st.Installed {
		t.Fatalf("ASSERTION: esc on the question installed the integration: %+v", st)
	}
	if err := first.Close(); err != nil {
		t.Logf("close the first client: %v", err)
	}

	term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
	waitInstallNotice(t, term)
	s := term.Screen()
	row := findRow(s, noticeNotInstalled)
	mouseClick(t, term, strings.Index(s.Line(row), noticeNotInstalled)+2, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText(installEffect, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the click on the toast opened no question: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	noHooksIn(t, settings, "the click on the toast wrote the file before the question was answered")
	s = term.Screen()
	row = -1
	for r := findRow(s, installEffect) + 1; r > 0 && r < 40; r++ {
		if strings.Contains(s.Line(r), "Install") {
			row = r
			break
		}
	}
	if row < 0 {
		t.Fatalf("the question has no Install row\n%s", term.Snapshot())
	}
	mouseClick(t, term, strings.Index(s.Line(row), "Install")+2, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Installed in ~/.claude/settings.json.", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: Install on the question installed nothing: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "agents-notice-installed")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("ASSERTION: the install wrote no %s: %v", settings, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings-after-install.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	st := claudeIntegrationVersion(t, base)
	if !strings.Contains(string(data), "agent-hook") || !strings.Contains(string(data), "--integration "+itoa(st.WantVersion)) {
		t.Fatalf("ASSERTION: the file does not hold the v%d hooks:\n%s", st.WantVersion, data)
	}
	if !st.Installed || !st.Current || st.Path != settings {
		t.Fatalf("ASSERTION: the CLI does not read the installed integration as current: %+v", st)
	}

	if out, err := tuiosCLI(t, base, "new-window", "-s", "agents"); err != nil {
		t.Fatalf("new window: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "agents", "claude\n"); err != nil {
		t.Fatalf("start the second stand-in: %v\n%s", err, out)
	}
	waitClaudePanes(t, base, 2)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(term.Screen().Text(), noticeNotInstalled) {
			t.Fatalf("ASSERTION: the toast came back after the install\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	alive(t, term, "after the install from the toast")
}

// TestAgentsSettingsHiddenWithAgentsOff: with [agents] enabled = false the
// prefix key says the features are off and the settings page has no Agents
// tab. The positive half is TestAgentsSettingsUpdatesAndUninstalls, the same
// fixture with the switch on.
func TestAgentsSettingsHiddenWithAgentsOff(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	base, _ := agentsPageFixture(t, true)
	term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
	if err := term.SendKeys(tuitest.Ctrl('b'), "A"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Agent features are off", uiTimeout); err != nil {
		t.Fatalf("the prefix key did not say the features are off: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc, tuitest.Ctrl('b'), ","); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Agent features", uiTimeout); err != nil {
		t.Fatalf("the settings page did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("[", "["); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Add a machine", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the tab before Tape is not Hosts with the features off: %v\n%s", err, term.Snapshot())
	}
	if s := term.Screen(); findRow(s, "Claude Code") >= 0 {
		t.Fatalf("ASSERTION: the settings page shows the Agents rows with the features off\n%s", term.Snapshot())
	}
}

// TestAgentsSettingsTabBeforeTape is the positive half of
// TestAgentsSettingsHiddenWithAgentsOff: with the features on, the same two
// steps back from the first tab land on the Agents tab.
func TestAgentsSettingsTabBeforeTape(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	base, _ := agentsPageFixture(t, false)
	term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
	enterTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
		t.Fatalf("the prefix menu never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("Agents settings", uiTimeout); err != nil {
		t.Fatalf("the prefix menu does not list the Agents settings key: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Toggle tiling") }, uiTimeout); err != nil {
		t.Fatalf("the prefix menu did not close: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), ","); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Agent features", uiTimeout); err != nil {
		t.Fatalf("the settings page did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("[", "["); err != nil {
		t.Fatal(err)
	}
	waitAgentsRow(t, term, "Claude Code", "the tab before Tape is not Agents", "out of date")
}

// TestAgentsSettingsKeepsTheCommandPath installs the integration with
// --command and the full path of the binary under test.
//
// Current: the tab says installed, names the program, and a Claude Code pane
// gets no notice. Before the fix the tab read it as out of date ("v3 is
// installed and this tuios installs v3") and offered an update.
//
// Aged: the tab says out of date, and the update keeps the full path. Before
// the fix the update wrote a bare "tuios", which need not be on the
// harness's PATH.
func TestAgentsSettingsKeepsTheCommandPath(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	t.Run("current", func(t *testing.T) {
		base, _ := agentsFixtureWith(t, agentsFixture{command: tuiosBin})
		term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
		openAgentsTab(t, term)
		waitAgentsRow(t, term, "Claude Code", "the row does not say installed for an install with --command", "installed")
		if line := agentsRow(term.Screen(), "Claude Code"); strings.Contains(line, "out of date") || strings.Contains(line, "not installed") {
			t.Fatalf("ASSERTION: an install with --command reads as %q\n%s", line, term.Snapshot())
		}
		if err := term.WaitForText("The hooks run", uiTimeout); err != nil {
			t.Fatalf("the row does not name the program the hooks run: %v\n%s", err, term.Snapshot())
		}
		railShot(t, term, "agents-settings-command-path")
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatal(err)
		}
		if out, err := tuiosCLI(t, base, "send-text", "-s", "agents", "claude\n"); err != nil {
			t.Fatalf("start the stand-in: %v\n%s", err, out)
		}
		waitClaudePanes(t, base, 1)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(term.Screen().Text(), "Claude Code integration is") {
				t.Fatalf("ASSERTION: a current install with --command got a notice\n%s", term.Snapshot())
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
	t.Run("aged", func(t *testing.T) {
		base, settings := agentsFixtureWith(t, agentsFixture{command: tuiosBin, aged: true})
		term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
		openAgentsTab(t, term)
		waitAgentsRow(t, term, "Claude Code", "the aged install with --command does not read as out of date", "out of date")
		clickAgentsRow(t, term, "Claude Code")
		waitAgentsRow(t, term, "› Update Claude Code", "the cursor is not on the update action")
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitForText("Updated in ~/.claude/settings.json.", uiTimeout); err != nil {
			t.Fatalf("the update said nothing: %v\n%s", err, term.Snapshot())
		}
		data, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		want := claudeIntegrationStatus(t, base, tuiosBin)
		if !strings.Contains(string(data), tuiosBin+" agent-hook claude-code --integration "+itoa(want.WantVersion)) {
			t.Fatalf("ASSERTION: the update did not keep %s as the program:\n%s", tuiosBin, data)
		}
		if !want.Current {
			t.Fatalf("ASSERTION: the CLI does not read the update as current for %s: %+v", tuiosBin, want)
		}
	})
}

func sshTUI(t *testing.T, base string) *tuitest.Terminal {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()
	env := append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	srv := exec.Command(tuiosBin, "ssh", "--host", "127.0.0.1", "--port", port, "--no-auth",
		"--key-path", filepath.Join(base, "hostkey"), "--default-session", "agents")
	srv.Env = env
	srv.Dir = workDirIn(t, base)
	logf, err := os.Create(filepath.Join(t.TempDir(), "ssh-server.log"))
	if err != nil {
		t.Fatal(err)
	}
	srv.Stdout, srv.Stderr = logf, logf
	if err := srv.Start(); err != nil {
		t.Fatalf("start tuios ssh: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_ = srv.Wait()
		_ = logf.Close()
	})
	deadline := time.Now().Add(bootTimeout)
	for {
		c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", port))
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(logf.Name())
			t.Fatalf("tuios ssh never listened on %s:\n%s", port, data)
		}
		time.Sleep(100 * time.Millisecond)
	}
	home := xdgDir(base, "HOME")
	argv := []string{"ssh", "-tt", "-p", port,
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", "-o", "BatchMode=yes", "-F", "/dev/null",
		"agents@127.0.0.1"}
	return tuitest.StartT(t, argv,
		tuitest.WithSize(120, 40),
		tuitest.WithTerm("xterm-256color"),
		tuitest.WithEnv("HOME="+home),
		tuitest.WithDir(workDirIn(t, base)))
}

// TestAgentsSettingsAbsentOverSSH attaches over tuios ssh to the same fixture
// the local tests use. The person at the far end is not on this machine, so
// the integrations here are not theirs to change: the prefix menu does not
// list the key, the key opens no tab, the palette has no entry, and the tab
// before Tape is Hosts. The positive halves are TestAgentsSettingsTabBeforeTape
// (menu and tab) and TestAgentsIntegrationNoticeOncePerRun (palette), with a
// local client on the same fixture.
func TestAgentsSettingsAbsentOverSSH(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh client")
	}
	base, _ := agentsPageFixture(t, false)
	term := sshTUI(t, base)
	if err := term.WaitForText("1:1", bootTimeout); err != nil {
		t.Fatalf("the ssh client never drew the session: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)

	if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
		t.Fatalf("the prefix menu never opened over ssh: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Oldest waiting", uiTimeout); err != nil {
		t.Fatalf("the prefix menu over ssh has no Agents section to read: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "Agents settings") {
		t.Fatalf("ASSERTION: the prefix menu lists the Agents settings key over ssh\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "agents-ssh-menu")
	if err := term.SendKeys("A"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if findRow(term.Screen(), "Claude Code") >= 0 {
			t.Fatalf("ASSERTION: the prefix key opened the Agents tab over ssh\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if strings.Contains(term.Screen().Text(), "Toggle tiling") {
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatal(err)
		}
		waitGone(t, term, "the prefix menu after esc", "Toggle tiling")
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "P"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("the palette did not open over ssh: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("install and update integrations"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("integrations", uiTimeout); err != nil {
		t.Fatalf("the palette query never showed: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(500 * time.Millisecond)
	if strings.Contains(term.Screen().Text(), "Agents: settings") {
		t.Fatalf("ASSERTION: the palette offers the Agents settings over ssh\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	waitGone(t, term, "the palette after esc", paletteTitle)

	if err := term.SendKeys(tuitest.Ctrl('b'), ","); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Agent features", uiTimeout); err != nil {
		t.Fatalf("the settings page did not open over ssh: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("[", "["); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Add a machine", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the tab before Tape is not Hosts over ssh: %v\n%s", err, term.Snapshot())
	}
	if findRow(term.Screen(), "Claude Code") >= 0 {
		t.Fatalf("ASSERTION: the settings page shows the Agents rows over ssh\n%s", term.Snapshot())
	}
}

func sidebarStatePath(base string) string {
	return filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "sidebar.json")
}

func waitDismissalStored(base string, want bool) bool {
	deadline := time.Now().Add(uiTimeout)
	if !want {
		deadline = time.Now().Add(3 * time.Second)
	}
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(sidebarStatePath(base))
		stored := err == nil && strings.Contains(string(data), "agent_notices_dismissed")
		if stored && want {
			return true
		}
		if stored && !want {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !want
}

func clickNoticeDismiss(t *testing.T, term *tuitest.Terminal, text string) {
	t.Helper()
	if err := term.WaitForText(text, uiTimeout); err != nil {
		t.Fatalf("no message %q to dismiss: %v\n%s", text, err, term.Snapshot())
	}
	s := term.Screen()
	row := findRow(s, text)
	cols, _ := s.Size()
	for x := 0; x+3 < cols; x++ {
		if s.Cell(x, row).Content == "m" && s.Cell(x+1, row).Content == "o" &&
			s.Cell(x+2, row).Content == "r" && s.Cell(x+3, row).Content == "e" {
			mouseClick(t, term, x+5, row, tuitest.MouseLeft, 0)
			return
		}
	}
	t.Fatalf("the message %q has no \"more\" to find its dismiss end by:\n%s", text, term.Snapshot())
}

// TestAgentsNoticeOutlastsAnEsc: a person in a pane presses esc for the pane,
// to interrupt the agent. Esc also clears the dock, the toast with it, but
// only for this run. A client that attaches later shows the toast again.
// This is also the positive half of the stored dismissal in
// TestAgentsIntegrationNoticeOncePerRun: a new client shows the toast when no
// click stored its dismissal.
func TestAgentsNoticeOutlastsAnEsc(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	base, _ := agentsPageFixture(t, false)
	term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
	enterTerminalMode(t, term)
	if out, err := tuiosCLI(t, base, "send-text", "-s", "agents", "claude\n"); err != nil {
		t.Fatalf("start the stand-in: %v\n%s", err, out)
	}
	waitClaudePanes(t, base, 1)
	if err := term.WaitForText(integrationNotice, uiTimeout); err != nil {
		t.Fatalf("no toast for the out of date integration: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), integrationNotice) }, uiTimeout); err != nil {
		t.Fatalf("esc did not clear the dock: %v\n%s", err, term.Snapshot())
	}
	if !waitDismissalStored(base, false) {
		data, _ := os.ReadFile(sidebarStatePath(base))
		t.Fatalf("ASSERTION: an esc stored a lasting dismissal:\n%s", data)
	}
	if err := term.Close(); err != nil {
		t.Logf("close the first client: %v", err)
	}
	again := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
	if err := again.WaitForText(integrationNotice, 15*time.Second); err != nil {
		t.Fatalf("ASSERTION: the toast did not come back after an esc and a new attach: %v\n%s", err, again.Snapshot())
	}
}
