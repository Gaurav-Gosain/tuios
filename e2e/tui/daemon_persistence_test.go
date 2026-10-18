package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// The suite already drives daemon mode through 'tuios new' and 'tuios attach',
// so subscribe/resubscribe (workspace switching) and two clients on one session
// have real coverage. These two cover what nothing here reached: a client
// leaving and coming back to the same session, and a session outliving the
// daemon that held it. Both are the paths a user exercises the moment something
// goes wrong, and both were only ever tested below the daemon boundary.

// TestDetachAndReattachKeepsLayoutAndPaneContent is the detach the docs promise
// costs nothing: the daemon holds the PTYs and keeps an emulator fed, so a new
// client asks for each window's screen and repaints it.
//
// The marker is counted rather than merely found. A reattach resubscribes every
// pane, and answering a resubscribe with the pane's whole history is exactly how
// the workspace-switch bug painted a pane's output twice; the same mistake on
// this path would show here as a second copy.
func TestDetachAndReattachKeepsLayoutAndPaneContent(t *testing.T) {
	const marker = "REATTACHMARK-3182"

	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-reattach", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	first := startIn(t, base, startOpts{args: []string{"attach", "e2e-reattach"}})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	// Printed by the shell, so the typed command line cannot be mistaken for the
	// output and every hit on screen is a real paint.
	if err := first.SendKeys(`printf '%s\n' "$(echo REATTACHMARK)-3182"`, tuitest.Enter); err != nil {
		t.Fatalf("write the marker: %v", err)
	}
	if err := first.WaitForText(marker, shellTimeout); err != nil {
		t.Fatalf("the marker never appeared before the detach: %v\n%s", err, first.Snapshot())
	}

	// A second window, so the reattach has a layout to bring back and not just
	// one pane's contents.
	if err := first.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("to window mode: %v", err)
	}
	if err := first.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("never entered window management mode: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard)
	newWindow(t, first)

	if err := first.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, first, "after leader d")

	if !sessionListed(t, base, "e2e-reattach") {
		out, _ := tuiosCLI(t, base, "ls")
		t.Fatalf("the session did not survive the detach\nls:\n%s", out)
	}

	second := startIn(t, base, startOpts{args: []string{"attach", "e2e-reattach"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the reattached client never got both windows back (count %d): %v\n%s",
			countWindows(second.Screen()), err, second.Snapshot())
	}

	// Raise the pane that holds the marker before reading it off the screen.
	// Both windows are floating and the second one was opened on top, so the
	// marker is behind it. That is what a floating layout is, not a loss of
	// content. Without the raise, the marker is visible only when the
	// restored window has the wrong size (a client that attaches claiming a
	// placeholder 80x24 leaves it in an 80x24 box that sticks out from under
	// the newer window). With both windows at their right size, the newer one
	// covers it.
	windowManagementMode(t, second)
	if err := second.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("cycle focus to the pane holding the marker: %v", err)
	}

	if err := second.WaitForText(marker, shellTimeout); err != nil {
		t.Fatalf("pane content did not survive the detach: %v\n%s", err, second.Snapshot())
	}
	if err := second.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled after the reattach: %v", err)
	}
	if n := strings.Count(second.Screen().Text(), marker); n != 1 {
		t.Fatalf("the reattached pane shows its output %d times, want 1\n%s", n, second.Snapshot())
	}
	alive(t, second, "after reattach")
}

// TestDetachAndReattachKeepsScrollback is the reattach twin of the pane-content
// case: a client that comes back gets a new emulator per pane, so the history
// behind the screen has to be rebuilt from the daemon as well as the screen
// itself. Counted from the oldest line the scrollback viewer can reach.
func TestDetachAndReattachKeepsScrollback(t *testing.T) {
	const last = 300

	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-rescroll", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	first := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "e2e-rescroll"}})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	runInShell(t, first, fmt.Sprintf("for i in $(seq 1 %d); do echo \"RS-$i-END\"; done", last),
		fmt.Sprintf("RS-%d-END", last), bulkTimeout)

	if err := first.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, first, "after leader d")

	second := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "e2e-rescroll"}})
	if err := second.WaitForText(fmt.Sprintf("RS-%d-END", last), bootTimeout); err != nil {
		t.Fatalf("the reattached pane never showed its screen: %v\n%s", err, second.Snapshot())
	}

	if err := second.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("to window mode: %v", err)
	}
	if err := second.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("never entered window management mode: %v\n%s", err, second.Snapshot())
	}
	time.Sleep(insertGuard)

	if err := second.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("enter copy mode: %v", err)
	}
	if err := second.WaitForText("y yank", uiTimeout); err != nil {
		t.Fatalf("copy mode never opened: %v\n%s", err, second.Snapshot())
	}
	if err := second.SendKeys("g", "g"); err != nil {
		t.Fatalf("jump to oldest: %v", err)
	}
	if err := second.WaitForText("RS-1-END", uiTimeout); err != nil {
		t.Fatalf("the pane's scrollback did not survive the reattach: %v\n%s",
			err, second.Snapshot())
	}
	alive(t, second, "after reattach scrollback")
}

// TestSessionSurvivesTheDaemonAndSaysItWasRestored is resurrection through the
// real binary: kill the server, start a fresh one, and the session is back.
//
// It also pins what a restored session now says about itself. The shells are
// new, and before the tag the only evidence of that was a per-pane banner that
// scrolls away, so a session that had lost every process looked exactly like one
// that had been running for days.
func TestSessionSurvivesTheDaemonAndSaysItWasRestored(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-restore", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	first := startIn(t, base, startOpts{args: []string{"attach", "e2e-restore"}})
	if err := first.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)
	if err := first.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("to window mode: %v", err)
	}
	if err := first.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("never entered window management mode: %v\n%s", err, first.Snapshot())
	}
	time.Sleep(insertGuard)
	newWindow(t, first)

	// kill-server is synchronous and saves every session on the way out.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	waitExit(t, first, "after kill-server")

	// Starting any session starts a daemon, and a daemon restores on start.
	if out, err := tuiosCLI(t, base, "new", "e2e-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v: %s", err, out)
	}

	restored := waitForSessionInfo(t, base, "e2e-restore")
	if restored.WindowCount != 2 {
		t.Errorf("the restored session has %d windows, want the 2 it had", restored.WindowCount)
	}
	if !restored.Restored {
		t.Error("the restored session is not marked restored, so nothing but a pane banner says its shells are new")
	}
	// A session nobody restored must not wear the tag.
	if trigger := waitForSessionInfo(t, base, "e2e-trigger"); trigger.Restored {
		t.Error("a freshly created session is marked restored")
	}

	// 'tuios ls' has to say it too, since that is where a user looks first.
	out, err := tuiosCLI(t, base, "ls")
	if err != nil {
		t.Fatalf("ls: %v: %s", err, out)
	}
	if !strings.Contains(out, "restored") {
		t.Errorf("'tuios ls' does not mention the restored session:\n%s", out)
	}

	// And the attach path, before it takes the screen.
	second, logPath := startInLogged(t, base, startOpts{args: []string{"attach", "e2e-restore"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the restored session did not bring its layout back (count %d): %v\n%s",
			countWindows(second.Screen()), err, second.Snapshot())
	}
	logBytes, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logBytes), "was restored") {
		t.Errorf("the attach said nothing about the session having been restored; PTY log:\n%s", string(logBytes))
	}

	// The mark is spent: attaching answered the question it exists to ask.
	if again := waitForSessionInfo(t, base, "e2e-restore"); again.Restored {
		t.Error("the restored mark survived the first attach")
	}
	alive(t, second, "after attaching to the restored session")
}

// sessionInfo is the part of 'tuios ls --json' these tests read.
type sessionInfo struct {
	Name        string `json:"name"`
	WindowCount int    `json:"window_count"`
	Restored    bool   `json:"restored"`
}

// waitForSessionInfo polls the daemon's listing until the named session appears,
// so a test never races the daemon's own start.
func waitForSessionInfo(t *testing.T, base, name string) sessionInfo {
	t.Helper()
	deadline := time.Now().Add(bootTimeout)
	var last string
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "ls", "--json")
		last = out
		if err == nil {
			var sessions []sessionInfo
			if json.Unmarshal([]byte(out), &sessions) == nil {
				for _, s := range sessions {
					if s.Name == name {
						return s
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("session %q never appeared in the listing; last ls --json:\n%s", name, last)
	return sessionInfo{}
}

// restartMidVersion stamps the build the client starts as in
// TestRestartTheDaemonFromTheVersionNotice: a third version, apart from the
// daemon's and from the one an update installs.
const restartMidVersion = "e2e-mid"

var (
	restartMidOnce sync.Once
	restartMidBin  string
	restartMidErr  error
)

// restartMidBinary builds the client's version once per run.
func restartMidBinary(t *testing.T) string {
	t.Helper()
	restartMidOnce.Do(func() {
		dir, err := os.MkdirTemp("", "tuios-e2e-restart-mid")
		if err != nil {
			restartMidErr = err
			return
		}
		restartMidBin = filepath.Join(dir, "tuios")
		build := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version="+restartMidVersion, "-o", restartMidBin, "./cmd/tuios")
		build.Dir = "../.."
		if out, err := build.CombinedOutput(); err != nil {
			restartMidErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if restartMidErr != nil {
		t.Fatalf("build the client's version: %v", restartMidErr)
	}
	return restartMidBin
}

// copyExecutable copies a binary to dst through a rename, the way an update
// puts a new build in place: a process that runs the old file keeps it.
func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil { //nolint:gosec // a binary the test runs
		t.Fatalf("write %s: %v", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		t.Fatalf("rename onto %s: %v", dst, err)
	}
}

// TestRestartTheDaemonFromTheVersionNotice is the restart from the app, end to
// end, against real builds.
//
// A daemon of this build holds a session with two panes: one printed numbered
// lines, the other runs sleep. A client of a second build attaches from a path
// of its own, so the dock shows the version notice. Then a third build is
// renamed onto the client's path, the way an update replaces a binary. A click
// on the notice opens the dialog, which has to name the program that stops.
// r restarts. The client has to come back, by itself, attached to the same
// session on a daemon of the third build, with both panes, their names and
// the first pane's history. A second client, attached beside it, has to
// reattach by itself in the same process and say so.
//
// The ways it could pass without testing that:
//   - The client could be a new process attached by the test. The client is
//     found by list-clients, and its pid has to be the pid of the process the
//     test started: the exec keeps it.
//   - The daemon could be the old one. Its version is read from the daemon,
//     and the old one is a different build.
//   - The new daemon could run /proc/self/exe, the client's old file. That
//     file is the second build, so the version check fails.
//   - The history could be the old client's frame. The divider that says the
//     history was restored is written only by a restore.
//
// Frames of the notice, the dialog and the success line are saved as PNG
// when TUIOS_E2E_FRAMES is set. See NEGATIVE_CONTROLS.md for the controls.
func TestRestartTheDaemonFromTheVersionNotice(t *testing.T) {
	const name = "e2e-restart-ui"
	const last = 40
	next := syncNextBinary(t)
	mid := restartMidBinary(t)
	frames := os.Getenv("TUIOS_E2E_FRAMES")
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", name, "--name", "history"); err != nil {
		t.Fatalf("name the first pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", name, fmt.Sprintf("i=1; while [ $i -le %d ]; do echo \"RHIST-$i-END\"; i=$((i+1)); done\n", last)); err != nil {
		t.Fatalf("print history: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", name, "sleeper"); err != nil {
		t.Fatalf("open the second pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", name, "-w", "sleeper", "sleep 300\n"); err != nil {
		t.Fatalf("start a program: %v\n%s", err, out)
	}

	// The client runs from a path of its own, which the update replaces.
	client := filepath.Join(base, "bin", "tuios")
	if err := os.MkdirAll(filepath.Dir(client), 0o755); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, mid, client)
	var term *tuitest.Terminal
	wire := &syncBuffer{}
	withBinary("", func() {
		tuiosBin = client
		term = startIn(t, base, startOpts{cols: 100, rows: 40, args: []string{"--debug", "new", name}, out: wire})
	})
	pid := term.Pid()
	// A second client, of the daemon's own build, attached beside it. It has
	// to come back by itself.
	other := attachIn(t, base, name, startOpts{cols: 100, rows: 40})
	otherPid := other.Pid()

	// The whole notice fits the dock of a 100-column terminal.
	notice := "The daemon runs dev. ctrl+b j restarts it."
	if err := term.WaitForText(notice, bootTimeout); err != nil {
		t.Fatalf("ASSERTION: the client never showed the whole version notice: %v\n%s", err, term.Snapshot())
	}
	if frames != "" {
		savePNG(t, term.Screen(), shot.XTermPalette(), frames, "restart-notice")
	}

	// The update.
	copyExecutable(t, next, client)

	row, col := findText(t, term, "The daemon runs dev.")
	leftClick(t, term, col+2, row)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Restart the daemon") && strings.Contains(text, "These stop") &&
			strings.Contains(text, `sleep in "sleeper" (session `+name+`)`)
	}, bootTimeout); err != nil {
		t.Fatalf("ASSERTION: a click on the notice did not open the dialog with the program that stops: %v\n%s", err, term.Snapshot())
	}
	for _, want := range []string{
		"Daemon now      dev",
		"After restart   " + syncNextVersion + "   (from ",
		"This window     " + restartMidVersion + "   (moves to " + syncNextVersion + " too)",
		"These come back", "Restart the daemon", "Cancel", "r restart", "esc cancel",
	} {
		if !strings.Contains(term.Screen().Text(), want) {
			t.Errorf("ASSERTION: the dialog does not say %q\n%s", want, term.Snapshot())
		}
	}
	if frames != "" {
		savePNG(t, term.Screen(), shot.XTermPalette(), frames, "restart-confirm")
	}

	// The notice and the dialog had to fit 100 columns. The success line is
	// longer than a 100-column dock, so the terminal widens.
	if err := term.Resize(130, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := other.Resize(130, 40); err != nil {
		t.Fatalf("resize the second client: %v", err)
	}
	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("answer the dialog: %v", err)
	}
	success := "The daemon restarted on " + syncNextVersion + ". Your session is back."
	if err := term.WaitForText(success, 2*bootTimeout); err != nil {
		t.Fatalf("ASSERTION: the client did not come back with the success line: %v\n%s", err, term.Snapshot())
	}
	if frames != "" {
		savePNG(t, term.Screen(), shot.XTermPalette(), frames, "restart-success")
	}

	// The old client gave the terminal back before the exec: the reset, the
	// cursor shown, and then the line that says it attaches again.
	out := wire.String()
	again := strings.Index(out, "tuios attaches to session")
	reset := -1
	if again >= 0 {
		reset = strings.LastIndex(out[:again], "\x1b[!p")
	}
	if again < 0 || reset < 0 || !strings.Contains(out[reset:again], "\x1b[?25h") {
		t.Errorf("ASSERTION: the client did not reset the terminal and show the cursor before it started again (reset at %d, line at %d)", reset, again)
	}

	// The same process, attached to the same session.
	var clients []struct {
		PID     int    `json:"pid"`
		Session string `json:"session"`
	}
	withBinary("", func() {
		tuiosBin = next
		out, err := tuiosCLI(t, base, "list-clients", "--json")
		if err != nil || json.Unmarshal([]byte(out), &clients) != nil {
			t.Fatalf("list-clients: %v\n%s", err, out)
		}
		plan, err := tuiosCLI(t, base, "restart-server", "--dry-run")
		if err != nil || !strings.Contains(plan, "Daemon now      "+syncNextVersion) {
			t.Errorf("ASSERTION: the daemon does not run the build the update installed: %v\n%s", err, plan)
		}
	})
	found := false
	for _, c := range clients {
		found = found || (c.PID == pid && c.Session == name)
	}
	if !found {
		t.Errorf("ASSERTION: the client (pid %d) is not attached to %q after the restart: %+v", pid, name, clients)
	}
	// The flags it started with went along, the root's --debug included. The
	// client was started by new, so the root's flags are not in attach's own
	// set when the restart reads them.
	if argv, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err != nil ||
		!strings.Contains(string(argv), "--debug=true") || !strings.Contains(string(argv), "--no-animations=true") {
		t.Errorf("ASSERTION: the client started again without its flags: %v %q", err, argv)
	}

	// The second client reattached by itself, in the same process.
	if err := other.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "The daemon restarted on "+syncNextVersion+". tuios reattached.") &&
			countWindows(s) == 2 && strings.Contains(text, fmt.Sprintf("RHIST-%d-END", last)) &&
			strings.Contains(text, "restored from")
	}, bootTimeout); err != nil {
		t.Errorf("ASSERTION: the second client did not reattach with the panes: %v\n%s", err, other.Snapshot())
	}
	if frames != "" {
		savePNG(t, other.Screen(), shot.XTermPalette(), frames, "restart-other-client")
	}
	if code, exited := other.ExitCode(); exited {
		t.Errorf("ASSERTION: the second client quit (code %d) instead of reattaching", code)
	}
	withBinary("", func() {
		tuiosBin = next
		out, err := tuiosCLI(t, base, "list-clients", "--json")
		if err != nil || json.Unmarshal([]byte(out), &clients) != nil {
			t.Fatalf("list-clients: %v\n%s", err, out)
		}
	})
	found = false
	for _, c := range clients {
		found = found || (c.PID == otherPid && c.Session == name)
	}
	if !found {
		t.Errorf("ASSERTION: the second client (pid %d) is not attached to %q after the restart: %+v", otherPid, name, clients)
	}
	alive(t, other, "after it reattached")

	// The layout and the history.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return countWindows(s) == 2 && strings.Contains(text, fmt.Sprintf("RHIST-%d-END", last)) &&
			strings.Contains(text, "restored from")
	}, uiTimeout); err != nil {
		t.Errorf("ASSERTION: the panes and the history did not come back: %v\n%s", err, term.Snapshot())
	}
	var windows struct {
		Windows []struct {
			Name string `json:"display_name"`
		} `json:"windows"`
	}
	out, err := tuiosCLI(t, base, "list-windows", "-s", name, "--json")
	if err != nil || json.Unmarshal([]byte(out), &windows) != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var names []string
	for _, w := range windows.Windows {
		names = append(names, w.Name)
	}
	if strings.Join(names, ",") != "history,sleeper" {
		t.Errorf("ASSERTION: the panes after the restart are %v, want history and sleeper", names)
	}
	alive(t, term, "after the restart")
}

// TestPlainKillServerEndsClientsAtOnce is the other half of the reattach: a
// daemon stopped with a plain kill-server says "stop", and its clients quit at
// once, as they always did. A new daemon is started right after, with the
// session restored, so a client that waited for one would find it and stay.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with the client deaf to
// the goodbye, it waits, attaches to the new daemon and does not quit.
func TestPlainKillServerEndsClientsAtOnce(t *testing.T) {
	const name = "e2e-plain-stop"
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	term := attachIn(t, base, name, startOpts{})
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	stopped := time.Now()
	if out, err := tuiosCLI(t, base, "start-server"); err != nil {
		t.Fatalf("start a new daemon: %v\n%s", err, out)
	}
	for {
		if _, exited := term.ExitCode(); exited {
			break
		}
		if time.Since(stopped) > time.Second {
			t.Fatalf("ASSERTION: the client did not quit within 1 s of a plain kill-server\n%s", term.Snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(term.Snapshot(), "tuios reattached") {
		t.Errorf("ASSERTION: the client reattached after a plain kill-server\n%s", term.Snapshot())
	}
}
