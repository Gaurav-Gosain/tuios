package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
	"tuios.dev/vt"
)

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

const quickLookBannerDoc = `# Banner

![the banner](banner.png)

![the gradient](pic.png)

The end of the notes.
`

func quickLookOnScreen(t *testing.T, host *lockedBuffer, s tuitest.Screen) []shot.Image {
	t.Helper()
	cols, rows := s.Size()
	stream := host.String()
	var placed, virtual []shot.Image
	for _, p := range hostPictures(t, []byte(stream)) {
		if p.Col < 0 {
			virtual = append(virtual, p)
		} else {
			placed = append(placed, p)
		}
	}
	if len(virtual) == 0 {
		return placed
	}
	em := vt.NewEmulator(cols, rows)
	em.SetKittyPlaceholderMode(vt.KittyPlaceholdersKeep)
	_, _ = em.Write(kittyAPC.ReplaceAll([]byte(stream), nil))
	type box struct{ x0, y0, x1, y1 int }
	var boxes []box
	for y := range rows {
		for x := 0; x < cols; x++ {
			if c := em.CellAt(x, y); c == nil || !vt.IsKittyPlaceholder(c.Content) {
				continue
			}
			x0 := x
			for x+1 < cols {
				c := em.CellAt(x+1, y)
				if c == nil || !vt.IsKittyPlaceholder(c.Content) {
					break
				}
				x++
			}
			joined := false
			for i := range boxes {
				if b := &boxes[i]; b.x0 == x0 && b.x1 == x && b.y1 == y-1 {
					b.y1, joined = y, true
					break
				}
			}
			if !joined {
				boxes = append(boxes, box{x0: x0, y0: y, x1: x, y1: y})
			}
		}
	}
	for _, b := range boxes {
		for _, v := range virtual {
			if v.Cols == b.x1-b.x0+1 && v.Rows == b.y1-b.y0+1 {
				v.Col, v.Row = b.x0, b.y0
				placed = append(placed, v)
				break
			}
		}
	}
	return placed
}

var kittyAPC = regexp.MustCompile("\x1b_G[^\x1b]*\x1b\\\\")

func quickLookBanner(t *testing.T, term *tuitest.Terminal, host *lockedBuffer, why, frame string, ok func(cols int) bool) shot.Image {
	t.Helper()
	var pics []shot.Image
	var banner shot.Image
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		pics = quickLookOnScreen(t, host, s)
		for _, p := range pics {
			if p.Cols > 20 && ok(p.Cols) {
				banner = p
				return true
			}
		}
		return false
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, frame+"-missing", pics...)
		t.Fatalf("ASSERTION: %s: the host shows no banner at the width asked for: %+v: %v\n%s", why, pics, err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, frame, quickLookOnScreen(t, host, term.Screen())...)
	return banner
}

// TestQuickLookKeepsPicturesAtFullSize opens a Markdown file with a wide
// picture on a wide screen, on a host with kitty graphics, and presses f
// twice. Full size makes the panel wider, so the picture gets a wider box,
// and the host shows it at that box. The second f gives the panel back its
// own size, and the picture its first box. It runs on a host that takes
// placements, and on one that draws placeholder cells, which is what kitty
// and ghostty do. The artifacts are the frames NAME-panel, NAME-full and
// NAME-back.
//
// NEGATIVE CONTROL: with the ready check in quickLookMDImage removed, the
// placeholder cells at full size name a picture that is never prepared, so
// the host holds no placement for them and the full size wait times out.
func TestQuickLookKeepsPicturesAtFullSize(t *testing.T) {
	for _, tc := range []struct{ name, cfg string }{
		{"placed", "[appearance]\nkitty_placeholders = \"off\"\n"},
		{"placeholders", "[appearance]\nkitty_placeholders = \"on\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &lockedBuffer{}
			term, dir, _ := quickLookListedIn(t, tc.cfg, startOpts{cols: 240, rows: 50, out: host})
			if err := os.WriteFile(filepath.Join(dir, "banner.png"), gradientPNG(t, 4000, 200), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "zbanner.md"), []byte(quickLookBannerDoc), 0o600); err != nil {
				t.Fatal(err)
			}
			railLists(t, term, "the rail did not list zbanner.md", []string{"zbanner.md"}, nil)
			quickLookFocusRail(t, term, "zbanner.md")
			sendKeys(t, term, " ")
			railLists(t, term, "space did not show zbanner.md", []string{"The end of the notes."}, nil)
			panel := quickLookBanner(t, term, host, "the panel", tc.name+"-panel", func(int) bool { return true })

			sendKeys(t, term, tuitest.Tab, "f")
			full := quickLookBanner(t, term, host, "f did not show the banner wider", tc.name+"-full", func(cols int) bool { return cols > panel.Cols })
			if full.Rows < panel.Rows {
				t.Fatalf("ASSERTION: the banner lost rows at full size: %d, want %d at least", full.Rows, panel.Rows)
			}

			sendKeys(t, term, "f")
			quickLookBanner(t, term, host, "the second f did not give the banner its first box", tc.name+"-back", func(cols int) bool { return cols == panel.Cols })
			alive(t, term, "after full size and back")
		})
	}
}
