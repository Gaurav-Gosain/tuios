package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The settings page's Agents tab and the integration notice, on a real daemon
// and a real client, with a temporary home holding a Claude Code integration
// written by an older tuios and a stand-in claude on PATH.
//
// How these could pass wrongly, written down first:
//   - A tab that drew "out of date" for every harness would pass the first
//     check. So the same frame must show a harness that has never run here as
//     not installed, and the check reads the Claude Code row itself.
//   - An update that only redrew the row would pass a screen check. So the
//     file on disk is read for the new version marker, and the CLI's own
//     status is read too.
//   - An uninstall that left the entries would pass the row's "not installed"
//     if the row read something else. The file is read for the hook command.
//   - The notice test could pass with no notice at all if the detector never
//     recognised the stand-in. Its positive half waits for the toast, and the
//     daemon's listing is read for the harness on both panes before the
//     "not again" half counts.
//   - A page that only opened from one place would hide a dead entry. The
//     prefix key opens it in one test and the palette in the other.

// fakeClaude is a stand-in for Claude Code: the detector takes it for one by
// its process name, claude. It draws a line and waits on its terminal.
const fakeClaude = `#!/bin/sh
printf 'fake claude ready>\n'
while read -r line; do :; done
`

// agentsPageFixture writes a home with an out of date Claude Code integration
// and a stand-in claude on PATH, the config, and a detached session "agents".
// It returns the isolation root and the integration's settings file.
func agentsPageFixture(t *testing.T, off bool) (string, string) {
	t.Helper()
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
	// The system directories and nothing else of the person's own, so the
	// harnesses on the machine running the suite stay out of the page.
	t.Setenv("PATH", strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))

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

	// The integration as this tuios writes it, then marked as one an older
	// tuios wrote: the marker's version is what status compares.
	home := xdgDir(base, "HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "integration", "install", "claude-code"); err != nil {
		t.Fatalf("install the integration: %v\n%s", err, out)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	cur := claudeIntegrationVersion(t, base)
	if cur.Version < 2 || !cur.Current {
		t.Fatalf("the fresh install is not current: %+v", cur)
	}
	old := strings.ReplaceAll(string(data), "--integration "+itoa(cur.Version), "--integration "+itoa(cur.Version-1))
	if old == string(data) {
		t.Fatalf("no version marker to age in %s:\n%s", settings, data)
	}
	if err := os.WriteFile(settings, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := claudeIntegrationVersion(t, base); !st.Installed || st.Current || st.Version != cur.Version-1 {
		t.Fatalf("the aged file does not read as out of date: %+v", st)
	}

	if out, err := tuiosCLI(t, base, "new", "agents", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	return base, settings
}

// claudeStatus is what tuios integration status says about Claude Code.
type claudeStatus struct {
	Installed   bool `json:"installed"`
	Current     bool `json:"current"`
	Version     int  `json:"version"`
	WantVersion int  `json:"want_version"`
}

// claudeIntegrationVersion reads Claude Code's integration through the CLI.
func claudeIntegrationVersion(t *testing.T, base string) claudeStatus {
	t.Helper()
	out, err := tuiosCLI(t, base, "integration", "status", "claude-code", "--json")
	if err != nil {
		t.Fatalf("integration status: %v\n%s", err, out)
	}
	var sts []claudeStatus
	if err := json.Unmarshal([]byte(out), &sts); err != nil || len(sts) != 1 {
		t.Fatalf("integration status output: %v\n%s", err, out)
	}
	return sts[0]
}

// agentsRow is the Agents tab's line for label, "" when the panel draws none.
func agentsRow(s tuitest.Screen, label string) string {
	row := findRow(s, label)
	if row < 0 {
		return ""
	}
	return s.Line(row)
}

// waitAgentsRow waits for the row label to carry every one of want.
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

// clickAgentsRow clicks the middle of the row labelled label.
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

// openAgentsTab opens the page with the prefix key and waits for the rows.
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
	base, settings := agentsPageFixture(t, false)
	term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
	want := claudeIntegrationVersion(t, base).WantVersion

	openAgentsTab(t, term)
	waitAgentsRow(t, term, "Claude Code", "the tab does not say the integration is out of date", "out of date", "on PATH")
	// The positive half of "out of date": a harness that has never run here
	// reads as not installed, in the same frame.
	waitAgentsRow(t, term, "Codex", "the tab does not say Codex is not installed", "not installed", "not on PATH")
	dir := artifactDir(t)
	railShot(t, term, "agents-settings-out-of-date")

	// A click opens the row's actions. Each names the file it changes.
	clickAgentsRow(t, term, "Claude Code")
	waitAgentsRow(t, term, "Update Claude Code", "a click on the row opened no update action", "settings.json")
	waitAgentsRow(t, term, "Uninstall Claude Code", "a click on the row opened no uninstall action", "settings.json")
	if err := term.WaitForText(".claude/settings.json", uiTimeout); err != nil {
		t.Fatalf("the update action does not name the file it changes: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "agents-settings-actions")

	// Esc leaves the actions for the list, and changes nothing.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	waitAgentsRow(t, term, "Claude Code", "esc did not go back to the list", "out of date")
	if st := claudeIntegrationVersion(t, base); st.Current {
		t.Fatalf("ASSERTION: going back changed the integration: %+v", st)
	}

	// Update: the click opens the actions with the cursor on Update, and
	// enter is the confirmation.
	clickAgentsRow(t, term, "Claude Code")
	waitAgentsRow(t, term, "› Update Claude Code", "the cursor is not on the update action")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Claude Code integration is updated in", uiTimeout); err != nil {
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

	// Uninstall: the row now opens on Uninstall, as there is nothing to update.
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

// paneHarness is the harness the daemon holds for each pane, by window id.
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

// waitClaudePanes waits for n panes the daemon takes for Claude Code.
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

// integrationNotice is the toast for the aged Claude Code integration.
const integrationNotice = "Claude Code integration is out of date."

// TestAgentsIntegrationNoticeOncePerRun starts the stand-in claude in a pane
// and waits for the toast that names the fix. It dismisses the toast, starts a
// second claude pane, and watches for the toast for some seconds: it does not
// come back. The palette entry then opens the tab the toast names.
func TestAgentsIntegrationNoticeOncePerRun(t *testing.T) {
	base, _ := agentsPageFixture(t, false)
	term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})

	if out, err := tuiosCLI(t, base, "send-text", "-s", "agents", "claude\n"); err != nil {
		t.Fatalf("start the stand-in: %v\n%s", err, out)
	}
	waitClaudePanes(t, base, 1)
	if err := term.WaitForText(integrationNotice, uiTimeout); err != nil {
		t.Fatalf("no toast for the out of date integration: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "Open Settings, Agents") {
		// The dock may cut a long message. The full text is in the log.
		t.Logf("the toast is cut on screen:\n%s", term.Snapshot())
	}
	railShot(t, term, "agents-notice")

	// The doctor's footer names the same pane. It used to say every pane had
	// its integration whenever one was installed at all, current or not.
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

	// Dismiss it, then run a second claude.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), integrationNotice) }, uiTimeout); err != nil {
		t.Fatalf("esc did not dismiss the toast: %v\n%s", err, term.Snapshot())
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
		if strings.Contains(term.Screen().Text(), integrationNotice) {
			t.Fatalf("ASSERTION: the toast came back for a second Claude Code pane\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}

	// The palette entry opens the tab the toast names.
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
}

// TestAgentsSettingsHiddenWithAgentsOff: with [agents] enabled = false the
// prefix key says the features are off and the settings page has no Agents
// tab. The positive half is TestAgentsSettingsUpdatesAndUninstalls, the same
// fixture with the switch on.
func TestAgentsSettingsHiddenWithAgentsOff(t *testing.T) {
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
	// The Agents tab sits between Hosts and Tape, the last tab. Back from the
	// first tab wraps to Tape, and one more back is Hosts when there is no
	// Agents tab.
	if err := term.SendKeys("[", "["); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Add a host", uiTimeout); err != nil {
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
	base, _ := agentsPageFixture(t, false)
	term := attachIn(t, base, "agents", startOpts{cols: 120, rows: 40, shippedLooks: true})
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
