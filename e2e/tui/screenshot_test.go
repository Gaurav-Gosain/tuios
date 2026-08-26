package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// End-to-end screenshot coverage against a real tuios in a real terminal.
//
// Unit tests can prove the renderer draws what it was asked to. Only this can
// prove the capture mode is reachable, that the preview panel appears on
// screen, and that a file lands where the config said. This project has been
// burned by unit-level proof of a visual feature before.

// shotDir points screenshot.directory at the test's own directory and returns
// it, so a capture cannot land in the developer's Pictures folder.
func shotDir(t *testing.T, base string) string {
	t.Helper()
	dir := filepath.Join(base, "shots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make shot dir: %v", err)
	}
	return dir
}

// setShotOption sets one screenshot option on the running session, through the
// same set-config path a person would use.
func setShotOption(t *testing.T, term *tuitest.Terminal, base, path, value string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "set-config", path, value); err != nil {
		t.Fatalf("set-config %s %s: %v\n%s", path, value, err, out)
	}
}

// shotFiles lists what has been written into dir so far.
func shotFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// TestScreenshotCaptureModeAndPreview drives the whole M2 surface on a plain
// terminal with no graphics support: the leader chord opens capture mode, the
// hint strip says what it does, enter takes the window, the preview panel
// comes up carrying the file, and enter closes it leaving the file behind.
//
// Negative control: removing the `if o.CaptureActive()` branch from
// HandleKeyPress made the hint strip never appear and this fail at the first
// WaitFor. Removing the ShotPreview.Open block from renderOverlays made it
// fail at the panel assertion with the file already written, which is the
// exact split between "it worked" and "it showed".
func TestScreenshotCaptureModeAndPreview(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "e2e-shot"}})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)

	setShotOption(t, term, base, "screenshot.directory", dir)
	// txt is the cheapest format to render and the easiest to read back, and
	// what this test is about is the interaction, not the raster.
	setShotOption(t, term, base, "screenshot.format", "txt")

	// The leader chord opens capture mode.
	if err := term.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
		t.Fatalf("send leader+C: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "capture window", "cancel")
	}, uiTimeout); err != nil {
		t.Fatalf("capture mode never showed its hints: %v\n%s", err, term.Snapshot())
	}

	// Enter takes the focused window.
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Screenshot", "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the preview panel never appeared: %v\n%s", err, term.Snapshot())
	}

	// The panel is up before the file is: the cells are in hand at capture
	// time and the artifacts catch up, which is the whole of the fix for
	// "it does not show the preview after". So the file is waited for here
	// rather than expected to already exist.
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return len(shotFiles(t, dir)) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("the capture wrote %v, want one file: %v\n%s", shotFiles(t, dir), err, term.Snapshot())
	}
	files := shotFiles(t, dir)
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	if len(body) == 0 {
		t.Errorf("%s is empty", files[0])
	}
	if !strings.HasSuffix(files[0], ".txt") {
		t.Errorf("wrote %s, want the configured .txt format", files[0])
	}

	// Enter closes the panel and keeps the file.
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the preview panel never closed: %v\n%s", err, term.Snapshot())
	}
	if _, err := os.Stat(files[0]); err != nil {
		t.Errorf("enter removed the file: %v", err)
	}
	alive(t, term, "after a capture")
}

// TestScreenshotEscapeDiscardsTheFile checks the panel's esc really removes
// what it wrote, on the real thing.
//
// Negative control: making CloseScreenshotPreview ignore its discard argument
// left the file on disk and failed the last assertion.
func TestScreenshotEscapeDiscardsTheFile(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "e2e-shot"}})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "txt")

	if err := term.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
		t.Fatalf("send leader+C: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "capture window")
	}, uiTimeout); err != nil {
		t.Fatalf("capture mode never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "discard")
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never appeared: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return len(shotFiles(t, dir)) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("the capture wrote %v, want one file: %v", shotFiles(t, dir), err)
	}

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return len(shotFiles(t, dir)) == 0
	}, uiTimeout); err != nil {
		t.Fatalf("esc left %v behind: %v\n%s", shotFiles(t, dir), err, term.Snapshot())
	}
	alive(t, term, "after discarding a capture")
}

// TestScreenshotCaptureModeCancels checks esc in capture mode leaves nothing
// behind: no file, no panel, and the session still usable.
//
// Negative control: making the esc arm of HandleCaptureKey fall through left
// the mode up and the hint strip on screen, failing the WaitFor.
func TestScreenshotCaptureModeCancels(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "e2e-shot"}})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)

	if err := term.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
		t.Fatalf("send leader+C: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "capture window")
	}, uiTimeout); err != nil {
		t.Fatalf("capture mode never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "capture window")
	}, uiTimeout); err != nil {
		t.Fatalf("capture mode never closed: %v\n%s", err, term.Snapshot())
	}
	if files := shotFiles(t, dir); len(files) != 0 {
		t.Errorf("a cancelled capture wrote %v", files)
	}
	// The session still takes a window, so the mode gave the keyboard back.
	newWindow(t, term)
	alive(t, term, "after cancelling a capture")
}

// TestScreenshotPanelBeatsTheFile is the preview-first claim, on screen and on
// disk at once: the panel with the captured cells is up while the file it names
// does not exist yet.
//
// The whole of report 2 was that the panel arrived seconds after the gesture,
// or not at all, because it waited for a render, a write and a clipboard helper
// that could block without a bound. The cells are in hand at capture time, so
// the panel waits for none of them.
//
// A png at scale 4 of the whole screen is used because a slow render is what
// makes the claim observable: the same assertion on a txt capture would be a
// race between two microsecond events.
//
// Negative control: removing the openPendingPreview call from renderScreenshot
// made the panel wait for the result, so it never showed the pending status and
// the first WaitFor timed out.
func TestScreenshotPanelBeatsTheFile(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "e2e-shot"}})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "png")
	setShotOption(t, term, base, "screenshot.scale", "4")

	if err := term.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
		t.Fatalf("send leader+C: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "full screen")
	}, uiTimeout); err != nil {
		t.Fatalf("capture mode never opened: %v\n%s", err, term.Snapshot())
	}
	// f takes the whole screen, which is the most pixels this terminal can ask
	// for and so the slowest render available here.
	if err := term.SendKeys("f"); err != nil {
		t.Fatalf("send f: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Screenshot", "Saving the image")
	}, uiTimeout); err != nil {
		t.Fatalf("the panel never showed the pending capture: %v\n%s", err, term.Snapshot())
	}
	if files := shotFiles(t, dir); len(files) != 0 {
		t.Errorf("the panel waited for the file after all: %v", files)
	}

	if err := term.WaitFor(func(tuitest.Screen) bool {
		return len(shotFiles(t, dir)) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("the pending capture never landed: %v\n%s", err, term.Snapshot())
	}
	// And the panel then names the file instead of still saying it is saving.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, ".png") && !screenHas(s, "Saving the image")
	}, uiTimeout); err != nil {
		t.Fatalf("the panel never named the file it wrote: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after a slow capture")
}

// TestScreenshotVerbWritesAFile drives the CLI verb against a live daemon and
// checks every format lands a real file with the right extension.
//
// Negative control: making capture.Save a no-op left every path missing and
// failed each subtest.
func TestScreenshotVerbWritesAFile(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "e2e-shot"}})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	for _, format := range []string{"png", "svg", "ansi", "html", "txt"} {
		t.Run(format, func(t *testing.T) {
			out := filepath.Join(dir, "verb."+format)
			if cliOut, err := tuiosCLI(t, base, "screenshot",
				"--format", format, "--out", out, "--no-copy"); err != nil {
				t.Fatalf("tuios screenshot --format %s: %v\n%s", format, err, cliOut)
			}
			info, err := os.Stat(out)
			if err != nil {
				t.Fatalf("the verb wrote no file at %s: %v", out, err)
			}
			if info.Size() == 0 {
				t.Errorf("%s is empty", out)
			}
		})
	}
	alive(t, term, "after the verb")
}

// TestScreenshotRunCommandCapturesTheFocusedWindow drives the tape command,
// which is what a keybinding with no verb of its own reaches and what makes a
// capture recordable in a tape. It routes to the attached client rather than
// running daemon-side, because the preview panel it opens is a client thing.
//
// Negative control: removing the CommandTypeScreenshot arm from
// tape.CommandExecutor.Execute made run-command report an unknown command and
// this fail at the CLI call.
func TestScreenshotRunCommandCapturesTheFocusedWindow(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "e2e-shot"}})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "txt")

	if out, err := tuiosCLI(t, base, "run-command", "Screenshot"); err != nil {
		t.Fatalf("run-command Screenshot: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return len(shotFiles(t, dir)) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("run-command Screenshot wrote %v: %v\n%s",
			shotFiles(t, dir), err, term.Snapshot())
	}
	alive(t, term, "after run-command Screenshot")
}
