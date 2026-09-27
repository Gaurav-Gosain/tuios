package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The screenshot chord over an open overlay. The Inbox and the review own the
// keyboard while they are up, and they used to own the leader with it, so
// ctrl+b C did nothing and neither screen could be captured.
//
// How these could pass wrongly, written down first:
//   - the capture might be of the screen after the overlay closed, so the file
//     is checked for the overlay's own text;
//   - capture mode's hint strip might be in the file, so the file is checked
//     for its absence;
//   - the chord might work by closing the overlay first, so the overlay is
//     checked to still be open after the preview closes;
//   - holding the leader for the chord might take the leader, or the key after
//     it, away from the overlay, so a leader followed by an ordinary key is
//     checked to still reach the overlay (the positive half).

// captureFullScreenOver opens capture mode with the leader chord while an
// overlay is up, takes the full screen with key (f or enter), waits for the
// file and returns what it holds. Capture mode must not offer a pane: the panes
// are under the overlay. The preview panel is closed with enter, which keeps the file.
//
// The host stream up to capture mode is saved as well, so the screen as tuios
// drew it can be rendered to an image and looked at.
func captureFullScreenOver(t *testing.T, term *tuitest.Terminal, stream *hostStream, dir, what string, key any) string {
	t.Helper()
	before := len(shotFiles(t, dir))
	openCaptureMode(t, term)
	saveArtifact(t, term, artifactDir(t), what+"-capture-mode")
	if err := os.WriteFile(filepath.Join(artifactDir(t), what+"-capture-mode.ansi"), stream.bytes(), 0o644); err != nil {
		t.Errorf("save the host stream: %v", err)
	}
	if strings.Contains(term.Screen().Text(), "capture window") {
		t.Errorf("capture mode over the %s offers a pane, which is under it\n%s", what, term.Snapshot())
	}
	if err := term.SendKeys(key); err != nil {
		t.Fatalf("send %v: %v", key, err)
	}
	files := waitForShot(t, term, dir, before+1)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Screenshot", "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never appeared over the %s: %v\n%s", what, err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), what+"-preview")
	sendKeys(t, term, tuitest.Enter)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never closed: %v\n%s", err, term.Snapshot())
	}
	last := files[len(files)-1]
	body, err := os.ReadFile(last)
	if err != nil {
		t.Fatalf("read %s: %v", last, err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), what+"-capture.txt"), body, 0o644); err != nil {
		t.Errorf("save the capture: %v", err)
	}
	if strings.Contains(string(body), "full screen") {
		t.Errorf("the capture of the %s holds capture mode's own hint strip:\n%s", what, body)
	}
	return string(body)
}

// TestScreenshotOverTheInbox takes the whole screen with the Inbox open, and
// the file is the Inbox as it was drawn. It runs in terminal mode, which is
// where an attached client starts and where the Inbox took the leader: in
// window-management mode the leader was already checked ahead of the Inbox.
//
// Negative control: with the overlay chord's call in HandleKeyPress removed,
// ctrl+b C goes to the Inbox, capture mode never opens, and the test fails at
// openCaptureMode.
func TestScreenshotOverTheInbox(t *testing.T) {
	stream := &hostStream{}
	term, base := start(t, startOpts{args: []string{"new", "e2e-shot-inbox"}, out: stream})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "txt")
	fillPane(t, term, "ROW")
	enterTerminalMode(t, term)

	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitScreen(t, term, "the Inbox never opened", "Inbox", "Nothing is waiting for you.")

	body := captureFullScreenOver(t, term, stream, dir, "inbox", tuitest.Enter)
	for _, want := range []string{"Inbox", "Nothing is waiting for you.", "ROW1-"} {
		if !strings.Contains(body, want) {
			t.Errorf("the capture over the Inbox does not hold %q:\n%s", want, body)
		}
	}
	waitScreen(t, term, "the Inbox closed when the capture was taken", "Nothing is waiting for you.")

	// The positive half: a leader followed by an ordinary key still reaches
	// the Inbox. f cycles its filter, which retitles it.
	sendKeys(t, term, tuitest.Ctrl('b'), "f")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Inbox: ")
	}, uiTimeout); err != nil {
		t.Fatalf("f after the leader never reached the Inbox: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Inbox")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close the Inbox after the captures: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after a capture over the Inbox")
}

// TestScreenshotOverTheReview takes the whole screen with the review open,
// and a region dragged inside it, which the review used to eat as a click.
//
// Negative controls: with the overlay chord's call removed, capture mode never
// opens over the review. With the review's mouse guard put back ahead of
// capture mode's, the drag selects nothing and no second file lands.
func TestScreenshotOverTheReview(t *testing.T) {
	base, repo := fanFixture(t)
	session := reviewFan(t, base, repo, "shot")
	dir := shotDir(t, base)

	stream := &hostStream{}
	term := attachIn(t, base, session, startOpts{out: stream})
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "txt")
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "M README", "retry three times", "esc close")

	body := captureFullScreenOver(t, term, stream, dir, "review", "f")
	for _, want := range []string{"Review", "M README", "retry three times"} {
		if !strings.Contains(body, want) {
			t.Errorf("the capture over the review does not hold %q:\n%s", want, body)
		}
	}
	waitScreen(t, term, "the review closed when the capture was taken", "retry three times", "esc close")

	// A region: drag across the line the review shows, inside the overlay.
	row := -1
	for y, line := range strings.Split(term.Screen().Text(), "\n") {
		if strings.Contains(line, "retry three times") {
			row = y
			break
		}
	}
	if row < 0 {
		t.Fatalf("the review line is not on screen\n%s", term.Snapshot())
	}
	before := len(shotFiles(t, dir))
	openCaptureMode(t, term)
	mouseDrag(t, term, 0, row, 60, row+1, tuitest.MouseLeft, 0)
	files := waitForShot(t, term, dir, before+1)
	region, err := os.ReadFile(files[len(files)-1])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "review-region.txt"), region, 0o644); err != nil {
		t.Errorf("save the region: %v", err)
	}
	if !strings.Contains(string(region), "retry three times") {
		t.Errorf("the region dragged over the review does not hold its line:\n%s", region)
	}
}
