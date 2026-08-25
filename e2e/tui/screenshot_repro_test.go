package tuie2e

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The four field reports about the screenshot feature, each reproduced against
// a real client before it was fixed.
//
// Everything here asserts on the host stream or on the file that was written,
// never on a helper's return value. A capture is a picture, and the only way to
// know a picture is right is to look at the bytes that were sent for it.

// shotImageID is the reserved kitty image id for the preview picture. It is
// spelled out here rather than imported so the test fails if the constant in
// the client ever moves without this being reconsidered.
const shotImageID = 0xF100_0000

// shotCellW and shotCellH are the cell size the tests force on the client with
// TUIOS_CELL_SIZE, so an assertion about a placement box is arithmetic and not
// a guess about what the host answered.
const (
	shotCellW = 10
	shotCellH = 22
)

// shotGraphicsEnv is a client that believes it is on a kitty host with a known
// cell size. The preview's pixel tier only runs there.
func shotGraphicsEnv() []string {
	return []string{
		"TUIOS_KITTY_GRAPHICS=1",
		"TUIOS_SIXEL_GRAPHICS=0",
		fmt.Sprintf("TUIOS_CELL_SIZE=%dx%d", shotCellW, shotCellH),
		"PATH=/usr/bin:/bin",
	}
}

// shotTransmit is one complete picture the client uploaded under the preview's
// image id, reassembled from its m= chunks.
type shotTransmit struct {
	png []byte
}

// shotPlacement is one a=p of the preview picture: the cell box it was drawn
// into.
type shotPlacement struct {
	cols, rows int
}

// parseShotGraphics replays the kitty graphics conversation and returns the
// preview pictures that were uploaded and the boxes they were placed into.
//
// Transmissions are reassembled rather than counted, because what the stale
// picture report is about is which bytes the host holds under the id, and a
// chunk count says nothing about that.
func parseShotGraphics(stream []byte) ([]shotTransmit, []shotPlacement) {
	var transmits []shotTransmit
	var placements []shotPlacement
	var pending []byte
	inTransmit := false

	for _, m := range apcRE.FindAllSubmatch(stream, -1) {
		p := kittyParams(string(m[1]))
		payload := m[2]
		id := atoiU32(p["i"])
		switch {
		case p["a"] == "t" && id == shotImageID:
			inTransmit = true
			pending = append(pending[:0], payload...)
			if p["m"] != "1" {
				transmits = append(transmits, shotTransmit{png: decodeShotPNG(pending)})
				inTransmit = false
			}
		case p["a"] == "" && p["m"] != "" && inTransmit:
			pending = append(pending, payload...)
			if p["m"] != "1" {
				transmits = append(transmits, shotTransmit{png: decodeShotPNG(pending)})
				inTransmit = false
			}
		case p["a"] == "p" && id == shotImageID:
			c, _ := strconv.Atoi(p["c"])
			r, _ := strconv.Atoi(p["r"])
			placements = append(placements, shotPlacement{cols: c, rows: r})
		}
	}
	return transmits, placements
}

func decodeShotPNG(b64 []byte) []byte {
	raw, err := base64.StdEncoding.DecodeString(string(b64))
	if err != nil {
		return nil
	}
	return raw
}

// pngSize reads a PNG's own dimensions out of its IHDR.
func pngSize(raw []byte) (w, h int) {
	if len(raw) < 24 || !bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")) {
		return 0, 0
	}
	return int(binary.BigEndian.Uint32(raw[16:20])), int(binary.BigEndian.Uint32(raw[20:24]))
}

// cropLine cuts columns [x0,x1) out of a screen row and trims the trailing
// blanks, the way the text backend writes a captured row.
//
// It counts cells, not bytes. Every border glyph on the screen is three bytes
// of UTF-8, so slicing the string would cut a different rectangle than the one
// the drag selected and the assertion would be about the test.
func cropLine(line string, x0, x1 int) string {
	runes := []rune(line)
	for len(runes) < x1 {
		runes = append(runes, ' ')
	}
	return strings.TrimRight(string(runes[x0:x1]), " ")
}

// fillPane types a block of numbered lines into the focused pane, so a capture
// of any rectangle of the screen has content that says where it came from.
func fillPane(t *testing.T, term *tuitest.Terminal, tag string) {
	t.Helper()
	enterTerminalMode(t, term)
	cmd := fmt.Sprintf("i=1; while [ $i -le 12 ]; do echo \"%s$i-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789\"; i=$((i+1)); done", tag)
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("type filler: %v", err)
	}
	if err := term.WaitForText(tag+"12-ABCDEF", shellTimeout); err != nil {
		t.Fatalf("filler never landed: %v\n%s", err, term.Snapshot())
	}
	leaveTerminalMode(t, term)
}

// openCaptureMode enters capture mode and waits for its hint strip.
func openCaptureMode(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
		t.Fatalf("send leader+C: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "cancel") && screenHas(s, "full screen")
	}, uiTimeout); err != nil {
		t.Fatalf("capture mode never opened: %v\n%s", err, term.Snapshot())
	}
}

// waitForShot blocks until one more file has landed in dir and returns it.
func waitForShot(t *testing.T, term *tuitest.Terminal, dir string, want int) []string {
	t.Helper()
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return len(shotFiles(t, dir)) >= want
	}, uiTimeout); err != nil {
		t.Fatalf("only %d files landed in %s, want %d: %v\n%s",
			len(shotFiles(t, dir)), dir, want, err, term.Snapshot())
	}
	return shotFiles(t, dir)
}

// TestScreenshotRegionDragCapturesTheDraggedRectangle is report 2: "even if i
// drag and screenshot it doesnt properly capture it".
//
// The drag selects a rectangle of the composed screen, so the file has to be
// that rectangle of the screen and nothing else. The screen is read before the
// gesture starts and cropped to the same rectangle, which is the only
// comparison that can tell a wrong rectangle from a right one.
//
// This one passes on the unfixed tree too, and that is its finding: the
// rectangle a drag selects was always the rectangle that was captured. It is
// kept because it is what rules the mapping out, and because "the drag does not
// capture what I dragged" needs an answer that is a measurement and not an
// opinion. Its own control is the arithmetic: shifting x0 by one column here
// fails every row, so it is reading the rectangle and not just the file.
func TestScreenshotRegionDragCapturesTheDraggedRectangle(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "e2e-shot"}})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "txt")
	fillPane(t, term, "ROW")

	// A rectangle in the middle of the pane, well clear of the dock and of the
	// hint strip capture mode draws along the top.
	const x0, y0, x1, y1 = 12, 8, 72, 20

	before := term.Screen()
	want := make([]string, 0, y1-y0)
	for y := y0; y < y1; y++ {
		want = append(want, cropLine(before.Line(y), x0, x1))
	}

	openCaptureMode(t, term)
	mouseDrag(t, term, x0, y0, x1-1, y1-1, tuitest.MouseLeft, 0)

	files := waitForShot(t, term, dir, 1)
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}
	got := strings.Split(strings.TrimRight(string(body), "\n"), "\n")

	if len(got) != len(want) {
		t.Fatalf("the drag selected %d rows and the capture has %d\nfile:\n%s\nwanted:\n%s",
			len(want), len(got), strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d of the selection (screen row %d)\n got %q\nwant %q",
				i, y0+i, got[i], want[i])
		}
	}
	if t.Failed() {
		t.Logf("whole file:\n%s\n\nwhole selection:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	alive(t, term, "after a region drag")
}

// TestScreenshotRegionDragOpensThePreview is the second half of report 2:
// "also doesnt show the preview after".
//
// This passes on the unfixed tree too: on a terminal with no graphics the panel
// always came up. It is kept as the control that separates the plain host from
// the graphics host, where the same gesture did go wrong; without it the
// graphics test below cannot say which half of the panel was at fault.
func TestScreenshotRegionDragOpensThePreview(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "e2e-shot"}})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "txt")
	fillPane(t, term, "ROW")

	openCaptureMode(t, term)
	mouseDrag(t, term, 12, 8, 71, 19, tuitest.MouseLeft, 0)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Screenshot", "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never appeared after a region drag: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after a region drag preview")
}

// TestScreenshotRegionDragOnAGraphicsHostShowsThePicture is report 2 on the
// host the reporter is actually on: kitty, where the panel carries a picture as
// well as cells. A drag has to leave a panel on screen with its own capture
// placed in it.
//
// Negative control: on the unfixed tree the picture was placed into 35 columns
// of a 74 column panel body, at aspect 0.88 against its own 1.81, and this
// failed on the shape assertion. What the reporter saw was a third of the panel
// carrying a squeezed picture and the rest carrying the capture's raw text.
func TestScreenshotRegionDragOnAGraphicsHostShowsThePicture(t *testing.T) {
	stream := &hostStream{}
	base := t.TempDir()
	term := startIn(t, base, startOpts{
		args: []string{"new", "e2e-shot"},
		env:  shotGraphicsEnv(),
		out:  stream,
	})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "png")
	setShotOption(t, term, base, "screenshot.scale", "1")
	fillPane(t, term, "ROW")

	openCaptureMode(t, term)
	mouseDrag(t, term, 12, 8, 71, 19, tuitest.MouseLeft, 0)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Screenshot", "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never appeared after a region drag: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(900 * time.Millisecond)

	transmits, placements := parseShotGraphics(stream.bytes())
	if len(transmits) == 0 || len(placements) == 0 {
		t.Fatalf("the panel is up but carries no picture: %d uploads, %d placements\n%s",
			len(transmits), len(placements), term.Snapshot())
	}
	checkPictureShape(t, transmits[len(transmits)-1].png, placements[len(placements)-1])
	alive(t, term, "after a region drag on a graphics host")
}

// checkPictureShape fails when the picture is drawn at proportions that are not
// its own.
//
// a=p scales the picture to exactly fill the cell box it is handed, so what the
// viewer sees is the uploaded PNG resampled to cols*cellW by rows*cellH. The
// cell size is forced by the environment, so this is arithmetic.
func checkPictureShape(t *testing.T, raw []byte, box shotPlacement) {
	t.Helper()
	pw, ph := pngSize(raw)
	if pw == 0 || ph == 0 {
		t.Fatalf("the uploaded payload is not a PNG")
	}
	boxW, boxH := box.cols*shotCellW, box.rows*shotCellH
	if boxW == 0 || boxH == 0 {
		t.Fatalf("the picture was placed into an empty box %dx%d cells", box.cols, box.rows)
	}
	pic := float64(pw) / float64(ph)
	drawn := float64(boxW) / float64(boxH)
	t.Logf("picture %dx%d px (aspect %.2f), box %dx%d cells = %dx%d px (aspect %.2f)",
		pw, ph, pic, box.cols, box.rows, boxW, boxH, drawn)

	// One cell of rounding on either axis is all a cell box can promise.
	tol := pic * float64(shotCellW) / float64(boxW) * 1.5
	if drawn < pic-tol || drawn > pic+tol {
		word := "squeezed horizontally"
		if drawn > pic {
			word = "stretched horizontally"
		}
		t.Errorf("the picture is drawn at aspect %.2f but its own is %.2f: it is %s",
			drawn, pic, word)
	}
}

// TestScreenshotRetakeSendsTheNewPicture is report 3: "it shows the previous
// screenshot when i capture my current one".
//
// Two captures a fraction of a second apart, of two windows with different
// content, so the two pictures cannot be the same bytes. The host holds one
// picture per image id, so the second capture has to upload its own; if it does
// not, what is on screen under that id is still the first capture.
//
// This passes on the unfixed tree too, and that is worth writing down: the
// retake path closes the panel first, and closing it takes the placement down
// and forgets the upload, so a retake always did send fresh pixels. The path
// that did not is the one below, which does not close the panel. Its own
// control is the tab: aim the second capture at the same window and both
// uploads carry the same bytes, so the last assertion fires.
func TestScreenshotRetakeSendsTheNewPicture(t *testing.T) {
	stream := &hostStream{}
	base := t.TempDir()
	term := startIn(t, base, startOpts{
		args: []string{"new", "e2e-shot"},
		env:  shotGraphicsEnv(),
		out:  stream,
	})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "png")
	setShotOption(t, term, base, "screenshot.scale", "1")
	fillPane(t, term, "ROW")
	// A second window, left empty, so the two captures differ by more than a
	// clock digit.
	newWindow(t, term)

	captureHovered := func(what string, tabs int) {
		t.Helper()
		openCaptureMode(t, term)
		for range tabs {
			if err := term.SendKeys(tuitest.Tab); err != nil {
				t.Fatalf("%s: send tab: %v", what, err)
			}
			time.Sleep(80 * time.Millisecond)
		}
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatalf("%s: send enter: %v", what, err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return screenHas(s, "Screenshot", "retake")
		}, uiTimeout); err != nil {
			t.Fatalf("%s: the preview never appeared: %v\n%s", what, err, term.Snapshot())
		}
	}

	captureHovered("first capture", 0)
	countAfterFirst := func() int {
		tx, _ := parseShotGraphics(stream.bytes())
		return len(tx)
	}
	if err := term.WaitFor(func(tuitest.Screen) bool { return countAfterFirst() >= 1 }, uiTimeout); err != nil {
		t.Fatalf("the first capture never uploaded a picture: %v\n%s", err, term.Snapshot())
	}

	// Retake, straight away, aimed at the other window.
	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("send r: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "capture window")
	}, uiTimeout); err != nil {
		t.Fatalf("retake never reopened capture mode: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("send tab: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Screenshot", "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the second preview never appeared: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(900 * time.Millisecond)

	transmits, placements := parseShotGraphics(stream.bytes())
	if len(placements) < 2 {
		t.Fatalf("the preview picture was placed %d times, want at least 2 (one per capture)\n%s",
			len(placements), term.Snapshot())
	}
	if len(transmits) < 2 {
		t.Fatalf("two captures uploaded %d pictures; the second capture draws the first one's bytes\n%s",
			len(transmits), term.Snapshot())
	}
	if bytes.Equal(transmits[0].png, transmits[len(transmits)-1].png) {
		t.Errorf("both uploads carried identical bytes, so the second capture is the first picture")
	}
	alive(t, term, "after a retake")
}

// TestScreenshotTwoCapturesInOneSecondKeepBothFiles is the file-level half of
// the same report. The name is stamped to the second, so two captures inside
// one second used to resolve to one path and the second silently overwrote the
// first.
//
// Negative control: on the unfixed tree four captures left one or two files,
// never four.
//
// Four rather than two so the result does not turn on where the second boundary
// falls. Four captures take well under a second here, so at most one boundary
// can be crossed and the unfixed tree can produce at most two names; the fixed
// tree produces four whatever the clock does.
func TestScreenshotTwoCapturesInOneSecondKeepBothFiles(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "e2e-shot"}})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "txt")

	const captures = 4
	for i := range captures {
		openCaptureMode(t, term)
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatalf("send enter %d: %v", i, err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return screenHas(s, "Screenshot", "retake")
		}, uiTimeout); err != nil {
			t.Fatalf("preview %d never appeared: %v\n%s", i, err, term.Snapshot())
		}
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatalf("close preview %d: %v", i, err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return !strings.Contains(s.Text(), "retake")
		}, uiTimeout); err != nil {
			t.Fatalf("preview %d never closed: %v\n%s", i, err, term.Snapshot())
		}
	}

	files := shotFiles(t, dir)
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = filepath.Base(f)
	}
	t.Logf("%d captures left %v", captures, names)
	if len(files) != captures {
		t.Fatalf("%d captures left %d files (%v); they overwrote each other",
			captures, len(files), names)
	}
	alive(t, term, "after several captures in one second")
}

// TestScreenshotPreviewKeepsThePicturesShape is reports 1 and 4: "looks
// stretched out", "horizontally stretched".
//
// a=p scales the picture to exactly fill the cell box it is handed, so the box
// the client asks for has to have the picture's own proportions in host pixels.
// The cell size is forced by the environment, so this is arithmetic.
//
// Negative control: on the unfixed tree the box was 25x18 cells for a 586x450
// picture, an aspect of 0.63 against the picture's 1.30, and this failed saying
// so. Removing the ratio check makes it pass either way, so the ratio is what
// carries it.
func TestScreenshotPreviewKeepsThePicturesShape(t *testing.T) {
	stream := &hostStream{}
	base := t.TempDir()
	term := startIn(t, base, startOpts{
		args: []string{"new", "e2e-shot"},
		env:  shotGraphicsEnv(),
		out:  stream,
	})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "png")
	setShotOption(t, term, base, "screenshot.scale", "1")
	fillPane(t, term, "ROW")

	openCaptureMode(t, term)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Screenshot", "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never appeared: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(900 * time.Millisecond)

	transmits, placements := parseShotGraphics(stream.bytes())
	if len(transmits) == 0 || len(placements) == 0 {
		t.Fatalf("the pixel tier never ran: %d uploads, %d placements\n%s",
			len(transmits), len(placements), term.Snapshot())
	}
	// TUIOS_SHOT_DUMP writes the picture the client actually uploaded to a file,
	// which is how the shape is checked by eye rather than only by arithmetic.
	if dump := os.Getenv("TUIOS_SHOT_DUMP"); dump != "" {
		_ = os.WriteFile(dump, transmits[len(transmits)-1].png, 0o644)
		t.Logf("dumped the uploaded picture to %s", dump)
	}
	checkPictureShape(t, transmits[len(transmits)-1].png, placements[len(placements)-1])
	alive(t, term, "after a png preview")
}

// TestScreenshotOverAnOpenPreviewSendsTheNewPicture is report 3 on the path
// that does not close the panel first: a capture arriving while the preview is
// already up.
//
// run-command is the reachable spelling of it, and the tape executor lands in
// the same place. The panel stays open, so nothing resets the picture the host
// holds, and the new capture has to say that its bytes are different.
//
// Negative control: on the unfixed tree the second capture uploaded nothing and
// this failed with one transmission against two placements.
func TestScreenshotOverAnOpenPreviewSendsTheNewPicture(t *testing.T) {
	stream := &hostStream{}
	base := t.TempDir()
	term := startIn(t, base, startOpts{
		args: []string{"new", "e2e-shot"},
		env:  shotGraphicsEnv(),
		out:  stream,
	})
	killDaemon(t, base)
	waitBoot(t, term)
	dir := shotDir(t, base)
	newWindow(t, term)
	setShotOption(t, term, base, "screenshot.directory", dir)
	setShotOption(t, term, base, "screenshot.format", "png")
	setShotOption(t, term, base, "screenshot.scale", "1")
	fillPane(t, term, "ROW")

	if out, err := tuiosCLI(t, base, "run-command", "Screenshot"); err != nil {
		t.Fatalf("first run-command Screenshot: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Screenshot", "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the first preview never appeared: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(tuitest.Screen) bool {
		tx, _ := parseShotGraphics(stream.bytes())
		return len(tx) >= 1
	}, uiTimeout); err != nil {
		t.Fatalf("the first capture never uploaded a picture: %v\n%s", err, term.Snapshot())
	}

	// The panel is still up. Capture again over the top of it.
	if out, err := tuiosCLI(t, base, "run-command", "Screenshot"); err != nil {
		t.Fatalf("second run-command Screenshot: %v\n%s", err, out)
	}
	time.Sleep(1200 * time.Millisecond)

	transmits, placements := parseShotGraphics(stream.bytes())
	t.Logf("%d uploads, %d placements", len(transmits), len(placements))
	if len(transmits) < 2 {
		t.Fatalf("a capture over an open preview uploaded %d pictures in %d placements; "+
			"the panel is still drawing the first capture\n%s",
			len(transmits), len(placements), term.Snapshot())
	}
	alive(t, term, "after capturing over an open preview")
}
