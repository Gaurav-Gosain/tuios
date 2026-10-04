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

// These tests run real tapes with tuios tape exec against a real daemon and a
// real attached client, and read the result off the client's screen and the
// command's exit status.
//
// Every marker a tape waits for is computed by the shell ($((40+2))), so the
// command line typed into the pane cannot satisfy the wait: only output can.
//
// The frame each test ends on is saved under artifactDir.
//
// Negative controls (NEGATIVE_CONTROLS.md):
//   - TestTapeExecPlaysActionsAndWaits: the CommandTypeWaitFor case cut from
//     the player's tick (update.go): WaitFor reaches Execute, which refuses it,
//     and exec exits 1 at line 2. The `case CommandTypeAction` cut from
//     Execute: exec exits 1 at the Action line.
//   - TestTapeExecStopsAtTheFailedLine: the failScript call cut from
//     checkScriptWait's timeout: the wait is dropped, the line after it runs,
//     and exec exits 0. The Locate call cut from runTapeExec: the message
//     names line 3 of the sent text, not lib.tape line 2.
//   - TestRunCommandRunsAnyAction: the IsActionName fallback cut from
//     resolveCommandName: run-command open_settings fails with "unknown
//     command".
//   - TestTapeRecordingReplaysActions: the Action branch cut from
//     Recorder.RecordAction: the saved tape holds no Action line.
//   - TestTapeSnapFullscreenFillsTheScreen: the tree before the fix: the
//     window is a quarter of the screen.

// tapeSession starts a daemon session named name with one pane and an
// attached client, and returns the client's terminal and the isolation root.
func tapeSession(t *testing.T, name string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", name}})
	waitWindowCount(t, term, 1, "first pane")
	return term, base
}

// writeTape writes a tape file under base and returns its path.
func writeTape(t *testing.T, base, name, body string) string {
	t.Helper()
	path := filepath.Join(base, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTapeExecPlaysActionsAndWaits: a tape that runs a registry action by
// name, waits on a condition instead of a sleep, and checks state with Expect
// runs to the end, and exec returns only when it has.
func TestTapeExecPlaysActionsAndWaits(t *testing.T) {
	term, base := tapeSession(t, "tapes")
	path := writeTape(t, base, "build.tape", `Run "echo first-$((40+2))"
WaitFor text "first-42" 15s
Action new_window
WaitFor panes 2
Run "echo second-$((40+3))"
WaitFor text "second-43" 15s
RenameWindow "two"
Expect focus "two"
Expect panes 2
Expect mode window
`)
	start := time.Now()
	out, errOut, err := splitCLI(t, base, "tape", "exec", "-s", "tapes", path)
	if err != nil {
		t.Fatalf("tape exec: %v\nstdout: %s\nstderr: %s\n%s", err, out, errOut, term.Snapshot())
	}
	if !strings.Contains(out, "Tape finished") {
		t.Errorf("tape exec printed %q, want it to report the tape finished", out)
	}
	// exec answers when the tape ends: by then the second pane exists and
	// shows the second marker, with no wait on the test's side.
	if err := term.WaitForText("second-43", time.Second); err != nil {
		t.Errorf("tape exec returned after %s, before the tape's last output was on screen: %v\n%s", time.Since(start), err, term.Snapshot())
	}
	waitWindowCount(t, term, 2, "after the tape")
	saveArtifact(t, term, artifactDir(t), "tape-actions")
}

// TestTapeExecStopsAtTheFailedLine: a WaitFor that times out fails the tape
// at its line, the rest of the tape does not run, and exec exits non-zero
// with the file and line, here inside a file the tape includes with Source.
func TestTapeExecStopsAtTheFailedLine(t *testing.T) {
	term, base := tapeSession(t, "tapefail")
	writeTape(t, base, "lib.tape", `# included
WaitFor text "never-printed-xyz" 1s
`)
	path := writeTape(t, base, "main.tape", `Run "echo before-$((1+1))"
Source "lib.tape"
Run "echo AFTER-$((1+1))"
`)
	out, errOut, err := splitCLI(t, base, "tape", "exec", "-s", "tapefail", path)
	if err == nil {
		t.Fatalf("tape exec succeeded on a tape whose WaitFor cannot hold\nstdout: %s\n%s", out, term.Snapshot())
	}
	for _, want := range []string{"lib.tape line 2, column 1", "timed out after 1s"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("tape exec stderr = %q, want it to contain %q", errOut, want)
		}
	}
	// The positive half: the line before the failure ran.
	if err := term.WaitForText("before-2", shellTimeout); err != nil {
		t.Fatalf("the line before the failed wait never ran: %v\n%s", err, term.Snapshot())
	}
	// The negative half: the line after it never runs. The shell answers an
	// echo in well under this, and the first line's output proves it is up.
	time.Sleep(2 * time.Second)
	if strings.Contains(term.Screen().Text(), "AFTER-2") {
		t.Errorf("the tape went on past the failed WaitFor\n%s", term.Snapshot())
	}
	if err := term.WaitForText("FAILED", uiTimeout); err != nil {
		t.Errorf("the client never showed that the tape failed: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "tape-failed")
}

// TestRunCommandRunsAnyAction: run-command takes any keybinding action by
// its config name, and Press reaches the open dialog the way a key does.
func TestRunCommandRunsAnyAction(t *testing.T) {
	term, base := tapeSession(t, "actions")
	if out, err := tuiosCLI(t, base, "run-command", "-s", "actions", "open_settings"); err != nil {
		t.Fatalf("run-command open_settings: %v\n%s", err, out)
	}
	if err := term.WaitForText("Settings", uiTimeout); err != nil {
		t.Fatalf("open_settings did not open the settings panel: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "settings-open")
	if out, err := tuiosCLI(t, base, "run-command", "-s", "actions", "Press", "esc"); err != nil {
		t.Fatalf("run-command Press esc: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Settings")
	}, uiTimeout); err != nil {
		t.Fatalf("Press esc did not close the settings panel: %v\n%s", err, term.Snapshot())
	}
	// A name that is neither a command nor an action is refused, and the
	// message says where the names are.
	out, err := tuiosCLI(t, base, "run-command", "-s", "actions", "no_such_action")
	if err == nil || !strings.Contains(out, "tuios keybinds list") {
		t.Errorf("run-command no_such_action: err %v, output %q, want a refusal naming tuios keybinds list", err, out)
	}
}

// TestTapeRecordingReplaysActions: an action pressed while recording is saved
// as Action and its name, and the saved tape plays it back. A recording used
// to keep ten actions and drop the rest.
func TestTapeRecordingReplaysActions(t *testing.T) {
	term, base := tapeSession(t, "record")
	leader := tuitest.Ctrl('b')
	// leader T r opens the naming dialog with a default name; Enter starts.
	for _, k := range []any{leader, "T", "r"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.WaitForText("recording_", uiTimeout); err != nil {
		t.Fatalf("the recording name dialog never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Recording started", uiTimeout); err != nil {
		t.Fatalf("the recording never started: %v\n%s", err, term.Snapshot())
	}
	// Starting a recording puts the pane in terminal mode. In window
	// management mode "," is open_settings rather than a character for the
	// shell.
	leaveTerminalMode(t, term)
	// "," is open_settings, an action the recorder used to drop.
	if err := term.SendKeys(","); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Settings", uiTimeout); err != nil {
		t.Fatalf("',' did not open the settings panel: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Settings")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close the settings panel: %v\n%s", err, term.Snapshot())
	}
	for _, k := range []any{leader, "T", "s"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.WaitForText("Recording saved", uiTimeout); err != nil {
		t.Fatalf("the recording was never saved: %v\n%s", err, term.Snapshot())
	}

	out, _, err := splitCLI(t, base, "tape", "list", "--json")
	if err != nil {
		t.Fatalf("tape list --json: %v\n%s", err, out)
	}
	var list struct {
		Tapes []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"tapes"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Tapes) != 1 {
		t.Fatalf("tape list --json = %s (%v), want one recording", out, err)
	}
	saved, err := os.ReadFile(list.Tapes[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "Action open_settings") {
		t.Fatalf("the recording does not hold the action:\n%s", saved)
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "recording.tape"), saved, 0o600); err != nil {
		t.Fatal(err)
	}

	// Played back, the recording opens the panel again.
	if out, errOut, err := splitCLI(t, base, "tape", "exec", "-s", "record", list.Tapes[0].Path); err != nil {
		t.Fatalf("tape exec of the recording: %v\n%s%s", err, out, errOut)
	}
	if err := term.WaitForText("Settings", uiTimeout); err != nil {
		t.Fatalf("playing the recording did not open the settings panel: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "recording-replayed")
}

// TestExampleTapeRuns: examples/actions_and_waits.tape, the example the tape
// docs point to, plays to the end in a real session and leaves the layout it
// describes.
func TestExampleTapeRuns(t *testing.T) {
	term, base := tapeSession(t, "example")
	example, err := filepath.Abs(filepath.Join("..", "..", "examples", "actions_and_waits.tape"))
	if err != nil {
		t.Fatal(err)
	}
	if out, errOut, err := splitCLI(t, base, "tape", "exec", "-s", "example", example); err != nil {
		t.Fatalf("tape exec %s: %v\n%s%s\n%s", example, err, out, errOut, term.Snapshot())
	}
	for _, want := range []string{"ready-42", "serving-42", "editor", "server"} {
		if err := term.WaitForText(want, uiTimeout); err != nil {
			t.Errorf("the example's layout does not show %q: %v\n%s", want, err, term.Snapshot())
		}
	}
	waitWindowCount(t, term, 2, "after the example")
	saveArtifact(t, term, artifactDir(t), "example")
}

// TestTapeSnapFullscreenFillsTheScreen: SnapFullscreen in a tape snapped the
// window to the top-left quarter, unlike the key's snap_fullscreen.
func TestTapeSnapFullscreenFillsTheScreen(t *testing.T) {
	term, base := tapeSession(t, "snap")
	path := writeTape(t, base, "snap.tape", "DisableTiling\nSnapFullscreen\n")
	if out, errOut, err := splitCLI(t, base, "tape", "exec", "-s", "snap", path); err != nil {
		t.Fatalf("tape exec: %v\n%s%s\n%s", err, out, errOut, term.Snapshot())
	}
	var rects []winRect
	if err := term.WaitFor(func(tuitest.Screen) bool {
		rects = waitForSettledGeometryIn(t, base, "snap", 1)
		return rects[0].Width > 100 && rects[0].Height > 30
	}, uiTimeout); err != nil {
		t.Errorf("after SnapFullscreen the window is %dx%d of a 120x40 screen, want it to fill the screen\n%s",
			rects[0].Width, rects[0].Height, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "snap-fullscreen")
}
