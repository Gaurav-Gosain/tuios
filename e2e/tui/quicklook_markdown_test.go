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
// with its words under it, and the one that is not there says so. The
// headings are bold, and none goes to the host as OSC 66 text sizing. The
// artifact is the frame quick-look-markdown-rich.
func TestQuickLookDrawsPicturesTablesAndHeadings(t *testing.T) {
	out := &lockedBuffer{}
	term := quickLookShowOpen(t, nil, out)
	railLists(t, term, "the table cut its long cell", []string{"LASTWORD", "team one", "team two"}, nil)
	railLists(t, term, "the images", []string{"the gradient", "Image: a missing picture", "could not be read"}, []string{"[Image:"})
	waitFor(t, term, "ASSERTION: the picture is drawn in blocks", func(s tuitest.Screen) bool {
		return strings.ContainsAny(s.Text(), "▀▄")
	})
	railLists(t, term, "the headings in bold", []string{"Big title", "Second title"}, nil)
	saveCockpitFrames(t, term, "quick-look-markdown-rich")
	if osc66Text.MatchString(out.String()) {
		t.Fatalf("ASSERTION: a heading went to the host as OSC 66\n%s", term.Snapshot())
	}
	alive(t, term, "after the rich Markdown")
}
