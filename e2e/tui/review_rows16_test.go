package tuie2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestReviewRowsAt16ColoursKeepTheTerminalColours opens the review at 16
// colours on a dark terminal and reads the cells the review paints itself,
// outside the diff: the file list's row for main.go and the header's session
// name. At this depth the palette leaves their ink and ground to the terminal,
// so both must be drawn in the terminal's own colours. A painter that wrote
// an unset colour as black would put black text on a black ground on every
// row.
//
// How this could pass wrongly, written down first:
//   - The cells could be read off the diff or a stale frame. Each is found by
//     text only the file list or the header shows, after the review is up.
//   - Default colours everywhere would also pass a client that paints
//     nothing. The file's status letter must carry the palette's 16-colour
//     slot, so the frame read is a painted one.
func TestReviewRowsAt16ColoursKeepTheTerminalColours(t *testing.T) {
	base, repo := fanFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte(reviewDepthBase), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "main.go")
	testutil.Git(t, repo, "commit", "-q", "-m", "main")
	session, path := reviewFanAt(t, base, repo, "r16")
	if err := os.WriteFile(filepath.Join(path, "main.go"), []byte(reviewDepthNew), 0o644); err != nil {
		t.Fatal(err)
	}

	term := attachIn(t, base, session, startOpts{cols: 160, rows: 40, env: chromeDepths[0].env})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "M main.go", `"good morning `)
	saveArtifact(t, term, artifactDir(t), "review16")

	s := term.Screen()
	cell := func(text string, off int) tuitest.Cell {
		row, col, ok := textAt(s, text, 0)
		if !ok {
			t.Fatalf("%q is not on screen\n%s", text, term.Snapshot())
		}
		return s.Cell(col+off, row)
	}
	black := func(c tuitest.Color) bool {
		return (c.Kind == tuitest.ColorIndexed && c.Index == 0) ||
			(c.Kind == tuitest.ColorRGB && c.R == 0 && c.G == 0 && c.B == 0)
	}

	for name, c := range map[string]tuitest.Cell{
		"file list row": cell("M main.go", len("M ")),
		"header":        cell("Review  "+session, len("Review  ")),
	} {
		if c.Bg.Kind != tuitest.ColorDefault {
			t.Errorf("the %s is on ground %+v at 16 colours, want the terminal's own\n%s", name, c.Bg, term.SnapshotStyled())
		}
		if black(c.Fg) {
			t.Errorf("the %s is drawn in black %+v at 16 colours, want the terminal's own ink\n%s", name, c.Fg, term.SnapshotStyled())
		}
	}

	if st := cell("M main.go", 0); st.Fg.Kind != tuitest.ColorIndexed || st.Fg.Index >= 16 {
		t.Errorf("the file's status letter is drawn in %+v, want one of the 16 colour slots\n%s", st.Fg, term.SnapshotStyled())
	}
}
