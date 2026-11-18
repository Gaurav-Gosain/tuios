package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// quickLookShowDoc is a document with two headings, a table too wide for the
// panel and two images: one beside it and one that is not there.
const quickLookShowDoc = `# Big title

## Second title

The table:

| Name | Description | Owner |
|------|-------------|-------|
| alpha | This description is far too long for its column at this width, so the cell has to wrap onto more lines and keep every word to the LASTWORD | team one |
| beta | short | team two |

![the gradient](pic.png)

![a missing picture](nothere.png)

The end of the notes.
`

// quickLookShowOpen opens zshow.md in quick look at full size, in a client
// started with env.
func quickLookShowOpen(t *testing.T, env []string, out *lockedBuffer) *tuitest.Terminal {
	t.Helper()
	term, dir := quickLookListed(t, append([]string{"TUIOS_KITTY_GRAPHICS=0"}, env...), out)
	if err := os.WriteFile(filepath.Join(dir, "zshow.md"), []byte(quickLookShowDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	railLists(t, term, "the rail did not list zshow.md", []string{"zshow.md"}, nil)
	quickLookFocusRail(t, term, "zshow.md")
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show zshow.md", []string{"The table:"}, nil)
	// f gives the panel the whole screen, so the picture and the table fit.
	sendKeys(t, term, tuitest.Tab, "f")
	return term
}

// TestQuickLookDrawsPicturesTablesAndHeadings opens a Markdown file in quick
// look. The table keeps every cell: its long cell wraps in its column, so the
// last word is on screen. The image beside the file is drawn as a picture
// with its words under it, and the one that is not there says so. The test
// terminal does not move the cursor for an OSC 66 space, so the probe finds no
// text sizing: the headings are plain bold and no heading goes out as OSC 66.
// With TUIOS_TEXT_SIZING=1 the two top headings go to the host as OSC 66, the
// first at twice the size and the second at one and a half. The emulator here
// blanks the rows of an OSC 66 instead of drawing it, so the sized panel is
// not saved as a frame. The artifact is the frame quick-look-markdown-rich.
func TestQuickLookDrawsPicturesTablesAndHeadings(t *testing.T) {
	out := &lockedBuffer{}
	// No override: the probe finds that this host does not draw OSC 66.
	term := quickLookShowOpen(t, nil, out)
	railLists(t, term, "the table cut its long cell", []string{"LASTWORD", "team one", "team two"}, nil)
	railLists(t, term, "the images", []string{"the gradient", "Image: a missing picture", "could not be read"}, []string{"[Image:"})
	waitFor(t, term, "ASSERTION: the picture is drawn in blocks", func(s tuitest.Screen) bool {
		return strings.ContainsAny(s.Text(), "▀▄")
	})
	railLists(t, term, "the headings in bold", []string{"Big title", "Second title"}, nil)
	saveCockpitFrames(t, term, "quick-look-markdown-rich")
	if strings.Contains(out.String(), "Big title\x07") {
		t.Fatalf("ASSERTION: OSC 66 went to a host with no text sizing\n%s", term.Snapshot())
	}

	sized := &lockedBuffer{}
	term = quickLookShowOpen(t, []string{"TUIOS_TEXT_SIZING=1"}, sized)
	railLists(t, term, "the sized document", []string{"LASTWORD"}, nil)
	waitUntil(t, "the headings go to the host as OSC 66", uiTimeout, func() (bool, string) {
		s := sized.String()
		return strings.Contains(s, "\x1b]66;s=2;Big title\x07") && strings.Contains(s, "\x1b]66;s=2:n=3:d=4;Second title\x07"), term.Snapshot()
	})
	alive(t, term, "after the rich Markdown")
}
