package tuie2e

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// An image paste types a path. The paste preview shows the image itself in a
// card at the pane's cursor, until the next key or a few seconds pass.
//
// The clipboard is the fake from paste_image_test.go, holding a real PNG so
// the card can read its size. The harness's terminal has no kitty graphics,
// so the card draws the picture as glyphs; the line under it, which names the
// size, the type and the file, is drawn the same in every mode, and it is
// what these tests read.

// previewPNG is a small picture with a size no other text on screen has.
func previewPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 97, 61))
	for y := range 61 {
		for x := range 97 {
			img.Set(x, y, color.RGBA{uint8(x * 2), uint8(y * 4), 180, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const previewTitle = "pasted image"

// showsPreviewCard reports whether the card is on screen: its title and the
// line that names the pasted picture.
func showsPreviewCard(s string) bool {
	return strings.Contains(s, previewTitle) && strings.Contains(s, "97×61 png")
}

// startPreviewClient starts a client with the fake clipboard and one pane in
// terminal mode, and returns it with its isolation root and environment.
func startPreviewClient(t *testing.T, config string, host *lockedBuffer, extraEnv ...string) (term *tuitest.Terminal, base string, env []string) {
	t.Helper()
	base = t.TempDir()
	if config != "" {
		writeConfig(t, base, config)
	}
	img := previewPNG(t)
	env = append(fakeClipboardEnv(t, img), extraEnv...)
	o := startOpts{args: []string{"new", "home"}, env: env}
	if host != nil {
		o.out = host
	}
	term = startIn(t, base, o)
	waitBoot(t, term)
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "near", "-s", "home"); err != nil {
		t.Fatalf("create a window: %v\n%s", err, out)
	}
	if err := term.WaitForText("near", uiTimeout); err != nil {
		t.Fatalf("the window never appeared: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	if strings.Contains(term.Screen().Text(), previewTitle) {
		t.Fatalf("a card is up before any paste; the waits below prove nothing\n%s", term.Snapshot())
	}
	if err := term.SendKeys("true "); err != nil {
		t.Fatal(err)
	}
	return term, base, env
}

// previewPlaceRE is the kitty placement of a picture, at the row it is put
// on.
var previewPlaceRE = regexp.MustCompile(`\x1b\[(\d+);\d+H\x1b_Ga=p,i=(\d+),p=(\d+),`)

// previewCardRow is the screen row of the card's top border, or -1.
func previewCardRow(s string) int {
	for i, line := range strings.Split(s, "\n") {
		if strings.Contains(line, previewTitle) {
			return i
		}
	}
	return -1
}

// TestAnImagePasteShowsTheImageInACard pastes an image with the paste_image
// action on a host with kitty graphics. The card comes up and the picture is
// placed on the host in the card's first row. The next key takes the card
// down, takes the picture off the host, and still reaches the pane.
//
// The artifact is the frame as the host shows it, the picture drawn in, under
// the test's artifact directory.
//
// Negative controls, each failing here: the call that puts the card up, in
// applyImagePasted, removed (no card); the card's clause in the fullscreen
// fast path removed (a lone pane never draws the card); the key's
// hidePastePreview in Update removed (the card outlives the key).
func TestAnImagePasteShowsTheImageInACard(t *testing.T) {
	host := &lockedBuffer{}
	term, _, _ := startPreviewClient(t, "", host)

	mark := len(host.String())
	if err := term.SendKeys(tuitest.Ctrl('b'), "V"); err != nil {
		t.Fatal(err)
	}
	var placed []string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		if !showsPreviewCard(text) || !showsPastedPath(text) {
			return false
		}
		row := previewCardRow(text)
		for _, m := range previewPlaceRE.FindAllStringSubmatch(host.String()[mark:], -1) {
			if m[1] == strconv.Itoa(row+2) {
				placed = m
			}
		}
		return placed != nil
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "card-missing", hostPictures(t, []byte(host.String()))...)
		t.Fatalf("no card, or no picture placed in it: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "card", hostPictures(t, []byte(host.String()))...)
	// The line under the picture keeps the end of the file name when it cuts
	// the line short, which is the part that tells two pastes apart.
	if !strings.Contains(term.Screen().Text(), ".png│") {
		t.Errorf("the card's line lost the end of the file name\n%s", term.Snapshot())
	}

	// The card takes no key: the next one takes it down, takes its picture
	// off the host, and still reaches the pane.
	mark = len(host.String())
	if err := term.SendKeys("#"); err != nil {
		t.Fatal(err)
	}
	unplace := "\x1b_Ga=d,d=i,i=" + placed[2] + ",p=" + placed[3] + ","
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), previewTitle) &&
			strings.Contains(unwrapped(paneBorderRows(s.Text())), ".png#") &&
			strings.Contains(host.String()[mark:], unplace)
		// Well inside the card's own six seconds, so the timer cannot pass
		// this for the key.
	}, 3*time.Second); err != nil {
		t.Fatalf("the card or its picture outlived the next key, or the key did not reach the pane: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "after-key", hostPictures(t, []byte(host.String()))...)
}

// TestAPasteWithoutGraphicsDrawsThePictureInGlyphs pastes on a host with no
// kitty graphics: the picture is drawn into the card's cells as block glyphs.
func TestAPasteWithoutGraphicsDrawsThePictureInGlyphs(t *testing.T) {
	term, _, _ := startPreviewClient(t, "", nil, "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0")

	if err := term.SendKeys(tuitest.Ctrl('b'), "V"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		row := previewCardRow(text)
		if !showsPreviewCard(text) || row < 0 {
			return false
		}
		// The row under the title is the picture's first: glyphs, not blanks.
		lines := strings.Split(text, "\n")
		return strings.ContainsAny(lines[row+1], "▀▄█▌▐▖▗▘▝▚▞▙▛▜▟")
	}, uiTimeout); err != nil {
		t.Fatalf("no picture drawn in the card's cells: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "card-glyphs")
}

// TestPastePreviewCanBeTurnedOff pastes twice in one client: with the card
// on, which puts it up, and after appearance.paste_preview is set to false,
// which pastes the path alone.
//
// Negative control: the setting's test in showPastePreview removed. The card
// comes up on the second paste.
func TestPastePreviewCanBeTurnedOff(t *testing.T) {
	term, base, env := startPreviewClient(t, "", nil)
	paste := func() {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('b'), "V"); err != nil {
			t.Fatal(err)
		}
	}
	pastes := func(s tuitest.Screen) int {
		return strings.Count(unwrapped(paneBorderRows(s.Text())), "tuios-paste-")
	}

	// On, the default: the card comes with the path.
	paste()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return pastes(s) == 1 && showsPreviewCard(s.Text())
	}, uiTimeout); err != nil {
		t.Fatalf("with the card on, the first paste put none up: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(" "); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), previewTitle)
	}, 3*time.Second); err != nil {
		t.Fatalf("the card outlived the next key: %v\n%s", err, term.Snapshot())
	}

	// Off: the path alone.
	if out, err := tuiosCLIEnv(t, base, env, "set-config", "appearance.paste_preview", "false", "-s", "home"); err != nil {
		t.Fatalf("turn the card off: %v\n%s", err, out)
	}
	paste()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return pastes(s) == 2
	}, uiTimeout); err != nil {
		t.Fatalf("the second paste pasted no path: %v\n%s", err, term.Snapshot())
	}
	// The card comes with the path when it comes at all, and stays for
	// seconds, so a wait shorter than that would see it.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), previewTitle)
	}, 2*time.Second); err == nil {
		t.Fatalf("paste_preview = false still put a card up\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "off-no-card")
}
