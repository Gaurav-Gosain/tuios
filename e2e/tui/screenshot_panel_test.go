package tuie2e

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The two reports about the preview panel after a capture: the picture does not
// survive the host terminal being resized, and it is not centred.
//
// Every expected value here is read off the drawn screen, from the panel's own
// footer rule and its own metadata line, and compared against the placement the
// client sent to the host. Nothing asks the client's geometry helpers what the
// answer should be. That mistake has been made twice on this feature: an
// assertion that gets its expected value from the call under test agrees with a
// wrong answer as readily as with a right one.

// shotPlaceRE matches one preview placement together with the cursor move that
// positions it, which is how appendKittyPlaceBox writes it.
var shotPlaceRE = regexp.MustCompile(`\x1b\[(\d+);(\d+)H\x1b_Ga=p,i=(\d+),p=(\d+),c=(\d+),r=(\d+)`)

// shotPlaceAt is one placement of the preview picture: where on the host screen
// it was drawn and how many cells it covers.
type shotPlaceAt struct{ x, y, cols, rows int }

// parseShotPlacesAt returns every preview placement in a host stream, in order.
func parseShotPlacesAt(stream []byte) []shotPlaceAt {
	var out []shotPlaceAt
	for _, m := range shotPlaceRE.FindAllSubmatch(stream, -1) {
		if id, _ := strconv.Atoi(string(m[3])); id != shotImageID {
			continue
		}
		y, _ := strconv.Atoi(string(m[1]))
		x, _ := strconv.Atoi(string(m[2]))
		c, _ := strconv.Atoi(string(m[5]))
		r, _ := strconv.Atoi(string(m[6]))
		// The escape is one-based and every other number here is not.
		out = append(out, shotPlaceAt{x: x - 1, y: y - 1, cols: c, rows: r})
	}
	return out
}

// panelRule finds the panel's footer rule at or below fromRow and returns the
// row it is on, the column it starts at and how many cells it spans.
//
// The rule is the panel's own drawing of its own width, which is why the
// assertions below measure against it: it comes from the overlay package, not
// from anything the picture's geometry is computed with. The dock draws a
// separator out of the same character but mixes a heavy rune into it and runs
// the whole screen width, so a run of nothing but the light rune, narrower than
// the screen, is the panel's.
func panelRule(t *testing.T, s tuitest.Screen, fromRow, screenWidth int) (row, col, width int) {
	t.Helper()
	for y := fromRow; y < fromRow+60; y++ {
		line := strings.TrimRight(s.Line(y), " ")
		if line == "" {
			continue
		}
		trimmed := strings.TrimLeft(line, " ")
		if strings.Count(trimmed, "─") != len([]rune(trimmed)) {
			continue
		}
		w := len([]rune(trimmed))
		if w < 20 || w >= screenWidth {
			continue
		}
		return y, len([]rune(line)) - w, w
	}
	t.Fatalf("no panel footer rule at or below row %d\n%s", fromRow, s.Text())
	return 0, 0, 0
}

// openShotPanel is the whole setup: a graphics client with a known cell size, a
// capture, and the panel up with its picture on the host.
func openShotPanel(t *testing.T, cols, rows int, capture func(*tuitest.Terminal)) (*tuitest.Terminal, *hostStream) {
	t.Helper()
	stream := &hostStream{}
	base := t.TempDir()
	term := startIn(t, base, startOpts{
		cols: cols, rows: rows,
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
	capture(term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Screenshot", "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never appeared: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return len(parseShotPlacesAt(stream.bytes())) >= 1
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never placed its picture: %v\n%s", err, term.Snapshot())
	}
	// The panel settles: the file lands, the status line grows a row and the
	// picture is placed again below it.
	time.Sleep(900 * time.Millisecond)
	return term, stream
}

// TestScreenshotPreviewSurvivesAHostResize is the first report: "the image
// preview thing after screenshot doesnt survive the host terminal getting
// resized".
//
// A resize of one row leaves a centred panel on exactly the cell it was already
// on, so every number the flush compares is equal and it drew nothing at all.
// The host meanwhile repainted its whole screen at the new size, and what it
// holds under the preview's image id afterwards is not the picture: driven
// through a real kitty, the panel came back with a black strip down the side of
// the body where the picture had been, and stayed that way through every later
// resize. So a resize has to make the client say the picture again.
//
// Negative control, measured on the unfixed tree at 120x40: one upload and one
// placement before the resize to 120x41, and one upload and one placement
// after. Resizing instead to 120x42, which does move the panel, gave two
// placements, which is what says the one-row case is the flush deciding it had
// nothing to do rather than the client missing the resize.
func TestScreenshotPreviewSurvivesAHostResize(t *testing.T) {
	term, stream := openShotPanel(t, 120, 40, func(term *tuitest.Terminal) {
		if err := term.SendKeys("f"); err != nil {
			t.Fatalf("send f: %v", err)
		}
	})

	uploadsBefore, _ := parseShotGraphics(stream.bytes())
	placesBefore := parseShotPlacesAt(stream.bytes())
	t.Logf("before the resize: %d uploads, %d placements, last %+v",
		len(uploadsBefore), len(placesBefore), placesBefore[len(placesBefore)-1])

	if err := term.Resize(120, 41); err != nil {
		t.Fatalf("resize the host: %v", err)
	}
	if err := term.WaitFor(func(tuitest.Screen) bool {
		up, _ := parseShotGraphics(stream.bytes())
		return len(up) > len(uploadsBefore) &&
			len(parseShotPlacesAt(stream.bytes())) > len(placesBefore)
	}, uiTimeout); err != nil {
		uploads, _ := parseShotGraphics(stream.bytes())
		places := parseShotPlacesAt(stream.bytes())
		t.Fatalf("the host was resized under an open preview and the client said nothing about "+
			"the picture: uploads %d -> %d, placements %d -> %d. Whatever the host has under that "+
			"id after repainting itself is what the user is left looking at.\n%s",
			len(uploadsBefore), len(uploads), len(placesBefore), len(places), term.Snapshot())
	}

	uploads, _ := parseShotGraphics(stream.bytes())
	places := parseShotPlacesAt(stream.bytes())
	t.Logf("after the resize: %d uploads, %d placements, last %+v",
		len(uploads), len(places), places[len(places)-1])
	// The picture that is put back has to be this capture's, not a fresh render
	// of something else.
	last := uploads[len(uploads)-1].png
	if len(last) == 0 {
		t.Fatalf("the upload after the resize is not a PNG")
	}
	for i := range uploads {
		if string(uploads[i].png) != string(last) {
			t.Errorf("upload %d differs from the one sent after the resize: the panel is showing "+
				"a different picture than it was before the host was resized", i)
		}
	}
	alive(t, term, "after a host resize under an open preview")
}

// TestScreenshotPreviewCentresThePicture is the second report: "its not centered
// properly i believe".
//
// The picture is letterboxed into the panel body, so a capture whose
// proportions are not the body's leaves spare columns. They used to all go to
// the right of the picture, which put the picture hard against the left edge of
// the panel with a blank third of a panel beside it.
//
// The margins are measured against the panel's own footer rule, which the
// overlay package draws at the body's left edge in the body's width. Nothing
// here asks the client where it thinks the body is.
//
// Negative control, measured on the unfixed tree at 120x40: a full-screen
// capture placed 53 columns into a 76 column panel with 2 columns to its left
// and 21 to its right, and this failed saying so.
func TestScreenshotPreviewCentresThePicture(t *testing.T) {
	term, stream := openShotPanel(t, 120, 40, func(term *tuitest.Terminal) {
		if err := term.SendKeys("f"); err != nil {
			t.Fatalf("send f: %v", err)
		}
	})

	places := parseShotPlacesAt(stream.bytes())
	pic := places[len(places)-1]
	ruleRow, ruleCol, ruleWidth := panelRule(t, term.Screen(), pic.y, 120)
	left := pic.x - ruleCol
	right := ruleCol + ruleWidth - (pic.x + pic.cols)
	t.Logf("picture %d cols at column %d; panel body starts at column %d and is %d wide "+
		"(rule on row %d); margins left %d right %d",
		pic.cols, pic.x, ruleCol, ruleWidth, ruleRow, left, right)

	if left < 0 || right < 0 {
		t.Fatalf("the picture is not inside the panel: margins left %d right %d\n%s",
			left, right, term.Snapshot())
	}
	// An odd number of spare columns cannot be split evenly, so one cell of
	// difference is all a cell box can promise.
	if diff := left - right; diff > 1 || diff < -1 {
		t.Errorf("the picture has %d columns to its left and %d to its right, so it is pushed to "+
			"the %s of the panel instead of sitting in the middle of it",
			left, right, map[bool]string{true: "left", false: "right"}[left < right])
	}
	alive(t, term, "after a centred preview")
}

// TestScreenshotPreviewBodyEndsWithThePicture is the other half of the same
// fault. A capture much wider than it is tall letterboxes to a few rows, and the
// body went on being as tall as the panel could afford: first a strip of the
// capture's own text under its own picture, and then, once the capture had no
// more rows to draw, a hole a dozen rows deep between the picture and the
// footer.
//
// The footer rule is the panel's own drawing of where its body ended, so the
// assertion is that the body ends with the picture: one blank row between the
// last row the picture covers and the rule, which is the same blank row the
// panel puts under a body of cells.
//
// Negative control, measured on the unfixed tree at 120x40: a 111x5 region
// letterboxed to 6 rows starting on row 13, and the rule was on row 32, which
// is 12 rows of nothing below the picture. This failed by those 12 rows.
func TestScreenshotPreviewBodyEndsWithThePicture(t *testing.T) {
	term, stream := openShotPanel(t, 120, 40, func(term *tuitest.Terminal) {
		// A wide, short rectangle: far wider than it is tall, so the picture
		// letterboxes to a handful of rows.
		mouseDrag(t, term, 4, 10, 114, 14, tuitest.MouseLeft, 0)
	})

	places := parseShotPlacesAt(stream.bytes())
	pic := places[len(places)-1]
	ruleRow, _, _ := panelRule(t, term.Screen(), pic.y, 120)
	wantRule := pic.y + pic.rows + 1
	t.Logf("picture covers rows %d to %d; the panel's footer rule is on row %d, want %d",
		pic.y, pic.y+pic.rows-1, ruleRow, wantRule)

	if ruleRow != wantRule {
		t.Errorf("the picture ends on row %d and the panel's body runs to row %d: "+
			"%d rows of the panel below the picture are neither picture nor anything else",
			pic.y+pic.rows-1, ruleRow-1, ruleRow-wantRule)
	}
	// Nothing of the capture is drawn beside the picture either.
	s := term.Screen()
	for y := pic.y; y < ruleRow; y++ {
		if line := strings.TrimSpace(s.Line(y)); line != "" {
			t.Errorf("row %d of the panel body carries %q: the capture is being shown as text "+
				"next to its own picture", y, line)
		}
	}
	alive(t, term, "after a wide short capture")
}

// TestScreenshotPictureHasTheTerminalsOwnAspect is the third report: "the
// screenshot preview needs to show it in the correct aspect ratio currently its
// not".
//
// The panel letterboxes the picture correctly and the picture itself was the
// wrong shape. The raster's cell took its width from the font's 'M' advance and
// its height from the font's line box, and a terminal's cell is neither: it is
// whatever the terminal picked. On the built-in face the raster's cell came out
// at a ratio of 0.400 against a real kitty's 0.450, so every column of the
// picture was eleven percent narrower than the column it pictured.
//
// The frame is turned off so the PNG is exactly the grid and nothing else, which
// makes the picture's own cell readable straight out of its IHDR. The two
// numbers compared are the cell this test told the client the host has, through
// the environment, and the cell the picture came out with. Neither is asked of
// the code under test, which is the mistake that let the last two rounds of this
// bug through.
//
// Negative control, measured on the unfixed tree with the same forced host cell
// of 10 by 22 (ratio 0.4545): a 120x40 capture uploaded a 960x680 PNG, a picture
// cell of 8.0 by 17.0 and a ratio of 0.4706, and this failed by 3.5 percent. The
// same build against a real kitty, whose cell is 9 by 20, was out by 11 percent,
// because the error is the difference between one font's line box and one
// terminal's cell and it is whatever those two happen to be. On the fixed tree
// the same capture uploads 960x704, a ratio of 0.4545 exactly.
func TestScreenshotPictureHasTheTerminalsOwnAspect(t *testing.T) {
	stream := &hostStream{}
	base := t.TempDir()
	term := startIn(t, base, startOpts{
		cols: 120, rows: 40,
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
	// No frame, so the picture is the grid and nothing else, and its own size
	// divided by the grid's is the cell the renderer drew with.
	setShotOption(t, term, base, "screenshot.frame", "none")
	fillPane(t, term, "ROW")

	openCaptureMode(t, term)
	if err := term.SendKeys("f"); err != nil {
		t.Fatalf("send f: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Screenshot", "retake")
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never appeared: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(tuitest.Screen) bool {
		up, _ := parseShotGraphics(stream.bytes())
		return len(up) >= 1
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never uploaded a picture: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(600 * time.Millisecond)

	// The capture's size in cells comes off the panel's own header line, which
	// the panel writes from the grid it captured.
	capCols, capRows := captureCells(t, term)
	uploads, _ := parseShotGraphics(stream.bytes())
	pngW, pngH := pngSize(uploads[len(uploads)-1].png)
	if pngW == 0 || pngH == 0 {
		t.Fatalf("the upload is not a PNG")
	}

	// The host's cell is what this test told the client it is, through the
	// environment, before the client ever started.
	want := float64(shotCellW) / float64(shotCellH)
	got := (float64(pngW) / float64(capCols)) / (float64(pngH) / float64(capRows))
	t.Logf("capture %dx%d cells; PNG %dx%d px; the picture's cell is %.3f by %.3f px, ratio %.4f; "+
		"the host's cell is %d by %d px, ratio %.4f; off by %+.1f%%",
		capCols, capRows, pngW, pngH,
		float64(pngW)/float64(capCols), float64(pngH)/float64(capRows), got,
		shotCellW, shotCellH, want, 100*(got-want)/want)

	// One percent covers the rounding of the raster to whole pixels and of the
	// preview downscale to whole pixels, which together are under a tenth of a
	// percent here. It does not cover a cell taken from a font instead of from
	// the terminal, which is 3.5 percent on this harness and 11 on a real kitty.
	if got < want*0.99 || got > want*1.01 {
		word := "narrower"
		if got > want {
			word = "wider"
		}
		t.Errorf("every column of the picture is %.1f%% %s than the column of the terminal it "+
			"pictures: the picture's cell ratio is %.4f and the host's is %.4f",
			math.Abs(100*(got-want)/want), word, got, want)
	}
	alive(t, term, "after an aspect check")
}

// captureCellsRE reads the capture's size in cells out of the panel's own header
// line, which is the panel reporting the grid it captured.
var captureCellsRE = regexp.MustCompile(`(\d+)x(\d+) cells`)

func captureCells(t *testing.T, term *tuitest.Terminal) (cols, rows int) {
	t.Helper()
	m := captureCellsRE.FindStringSubmatch(term.Screen().Text())
	if m == nil {
		t.Fatalf("the panel never said how big the capture is\n%s", term.Snapshot())
	}
	cols, _ = strconv.Atoi(m[1])
	rows, _ = strconv.Atoi(m[2])
	return cols, rows
}
