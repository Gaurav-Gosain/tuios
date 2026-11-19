package tuie2e

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// quickLookReadmeOpen opens the repository's own README.md in quick look at
// full size, with the assets it names beside it, in a client started with
// env. out takes what the client writes.
func quickLookReadmeOpen(t *testing.T, env []string, out *lockedBuffer) *tuitest.Terminal {
	t.Helper()
	term, dir := quickLookListed(t, append([]string{"TUIOS_KITTY_GRAPHICS=0"}, env...), out)
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), readme, 0o600); err != nil {
		t.Fatal(err)
	}
	assets, err := filepath.Abs(filepath.Join("..", "..", "assets"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(assets, filepath.Join(dir, "assets")); err != nil {
		t.Fatal(err)
	}
	railLists(t, term, "the rail did not list README.md", []string{"README.md"}, nil)
	quickLookFocusRail(t, term, "README.md")
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show README.md", []string{"Terminal UI Operating System"}, nil)
	sendKeys(t, term, tuitest.Tab, "f")
	return term
}

// quickLookPageTo presses J until want is on screen.
func quickLookPageTo(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	for range 60 {
		if strings.Contains(term.Screen().Text(), want) {
			_ = term.WaitStable(uiTimeout)
			return
		}
		sendEach(t, term, "J")
	}
	t.Fatalf("%q never came on screen\n%s", want, term.Snapshot())
}

// TestQuickLookReadsTheReadme opens the tuios README.md in quick look. The
// badges, each an image in a link, are one link each with the image's words,
// and no address or bracket of the link is on screen. The banner, a <picture>
// with a source for each colour scheme, is drawn. The shots in the feature
// table are on the web, and each is one short line of its words, and so is
// the contributors SVG beside the file. No heading
// goes to the host as OSC 66 text sizing. The artifacts are the frames
// readme-top, readme-install and readme-features.
func TestQuickLookReadsTheReadme(t *testing.T) {
	out := &lockedBuffer{}
	term := quickLookReadmeOpen(t, nil, out)
	waitFor(t, term, "ASSERTION: the banner is drawn", func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "▀")+strings.Count(s.Text(), "▄") > 20 && !strings.Contains(s.Text(), "Reading the image")
	})
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "readme-top")
	railLists(t, term, "ASSERTION: the badges", []string{"[Image: Latest Release]", "[Image: GoDoc]"}, []string{"img.shields.io", "](https", "Image: Latest Release\n"})
	quickLookPageTo(t, term, "Package Managers")
	saveCockpitFrames(t, term, "readme-install")
	quickLookPageTo(t, term, "Panes and the rail")
	saveCockpitFrames(t, term, "readme-features")
	railLists(t, term, "ASSERTION: a shot on the web is one short line", []string{"[Image: tuios with four tiled panes"}, []string{"test output and a build", "tuios.dev/shots", "](https", "\\["})
	// The contributors picture is an SVG beside the file: one line of its
	// words, not a picture that could not be read.
	quickLookPageTo(t, term, "spelled out in")
	railLists(t, term, "ASSERTION: an SVG is one short line", []string{"[Image: The word TUIOS spelled out in"}, []string{"not a picture", "could not be read"})
	if osc66Text.MatchString(out.String()) {
		t.Fatalf("ASSERTION: a heading went to the host as OSC 66\n%s", term.Snapshot())
	}
	alive(t, term, "after the README")
}

// osc66Text is an OSC 66 that sizes text. The startup probe's own, a single
// space, is not one.
var osc66Text = regexp.MustCompile("\x1b\\]66;[^;\x07]*;[^ \x07]")

// quickLookPictureDoc has a <picture> whose dark source is beside it and whose
// light source is not, and a small WebP image, which quick look does not draw.
const quickLookPictureDoc = `# Pictures

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./pic.png">
  <source media="(prefers-color-scheme: light)" srcset="./nolight.png">
  <img alt="the logo" src="./nolight.png">
</picture>

![a dot](dot.webp)

The end.
`

// quickLookDotWebP is a 1 by 1 lossless WebP.
const quickLookDotWebP = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

// TestQuickLookPicksThePictureForItsGround opens a document with a <picture>
// on the dark ground the tests draw on: the dark source, which is beside the
// file, is drawn, and the light one, which is not there, is never read. A
// WebP image beside the file is one line of its words.
func TestQuickLookPicksThePictureForItsGround(t *testing.T) {
	term, dir := quickLookListed(t, []string{"TUIOS_KITTY_GRAPHICS=0"}, nil)
	dot, err := base64.StdEncoding.DecodeString(quickLookDotWebP)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dot.webp"), dot, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "zpicture.md"), []byte(quickLookPictureDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	railLists(t, term, "the rail did not list zpicture.md", []string{"zpicture.md"}, nil)
	quickLookFocusRail(t, term, "zpicture.md")
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show zpicture.md", []string{"The end."}, nil)
	sendKeys(t, term, tuitest.Tab, "f")
	railLists(t, term, "ASSERTION: the dark source is drawn and the WebP is its words", []string{"the logo", "Image: a dot"},
		[]string{"could not be read", "Reading the image", "Image: the logo"})
	saveCockpitFrames(t, term, "picture-for-the-ground")
	alive(t, term, "after the picture")
}
