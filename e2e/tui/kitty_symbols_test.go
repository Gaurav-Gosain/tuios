package tuie2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
	"tuios.dev/vt"
)

// writeKittyPicture writes the test picture as kitty graphics: one a=T
// transmission of RGBA pixels in 4096-byte chunks, as chafa and timg send
// it, with id id. The cursor moves past the picture (C=0).
func writeKittyPicture(t *testing.T, dir string, id int) (*vt.SixelImage, string) {
	t.Helper()
	img := symbolPicture()
	rgba := make([]byte, 0, img.Width*img.Height*4)
	for _, p := range img.Pix {
		c := img.Palette[p-1]
		rgba = append(rgba, c.R, c.G, c.B, 255)
	}
	b64 := base64.StdEncoding.EncodeToString(rgba)
	var seq bytes.Buffer
	for off := 0; off < len(b64); off += 4096 {
		end := min(off+4096, len(b64))
		more := 1
		if end == len(b64) {
			more = 0
		}
		if off == 0 {
			fmt.Fprintf(&seq, "\x1b_Ga=T,f=32,s=%d,v=%d,i=%d,q=2,m=%d;%s\x1b\\", img.Width, img.Height, id, more, b64[off:end])
		} else {
			fmt.Fprintf(&seq, "\x1b_Gm=%d;%s\x1b\\", more, b64[off:end])
		}
	}
	path := filepath.Join(dir, fmt.Sprintf("picture-%d.kitty", id))
	if err := os.WriteFile(path, seq.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return img, path
}

// TestKittyImageSymbolsOnAHostWithoutGraphics draws a kitty picture in a
// pane on a host with no graphics, in both modes, and reads it back off the
// host's screen as block glyphs. It then scrolls the picture partly out of
// the pane, deletes it with a=d (as yazi and timg do before they draw the
// next one), draws it again and clears the screen. The host is sent no
// graphics at any step.
//
// How it could pass wrongly: glyphs of the sixel path could be on screen
// from another picture, so only the kitty picture is ever drawn; and a
// picture that is never deleted would still read as gone after the clear,
// so the delete is checked on its own, before any clear.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestKittyImageSymbolsOnAHostWithoutGraphics).
func TestKittyImageSymbolsOnAHostWithoutGraphics(t *testing.T) {
	const kind, ceiling = "octant", 10
	for _, daemon := range []bool{false, true} {
		name := map[bool]string{false: "standalone", true: "daemon"}[daemon]
		t.Run(name, func(t *testing.T) {
			host := newSixelHost(false, false)
			term, _ := startSymbolPane(t, host, kind, "truecolor", daemon)
			dir := t.TempDir()
			img, path := writeKittyPicture(t, dir, 7)
			before := len(host.bytes())
			typeLine(t, term, "clear; printf '%s\\n' 'IMG''TOP'; cat "+path+"; echo; echo SH''OWN")
			if err := term.WaitForText("SHOWN", shellTimeout); err != nil {
				t.Fatalf("the picture did not print: %v\n%s", err, term.Snapshot())
			}
			origin := imageOrigin(t, term, "IMGTOP")
			paneTop := origin.Y - 1
			var sc symbolScore
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				sc = scoreSymbols(s, img, origin, paneTop, kind)
				return len(sc.bad) == 0 && sc.err < ceiling && sc.cells > 0
			}, uiTimeout); err != nil {
				t.Errorf("ASSERTION: the kitty picture on the host: error %.1f (ceiling %d), %d cells; %s\n%s",
					sc.err, ceiling, sc.cells, strings.Join(sc.bad, "; "), term.Screen().Text())
			}
			t.Logf("%s: mean error %.2f over %d cells", name, sc.err, sc.cells)
			savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), "shown")

			_, rows := term.Screen().Size()
			shownAt := func(s tuitest.Screen) int {
				at := -1
				for y := range rows {
					if l := s.Line(y); strings.Contains(l, "SHOWN") && !strings.Contains(l, "echo") {
						at = y
					}
				}
				return at
			}
			// The cursor ends at column 0 under the picture (C=0), where it
			// ends on a host that draws kitty graphics (ReserveImageSpace),
			// so echo's newline leaves one blank row before SHOWN, as for a
			// sixel picture.
			gap := shownAt(term.Screen()) - origin.Y
			if gap != symPicRows+1 {
				t.Fatalf("ASSERTION: SHOWN is %d rows under the picture's top, want %d: the cursor did not move past the picture\n%s", gap, symPicRows+1, term.Screen().Text())
			}
			paneBottom := rows - 1
			for y := origin.Y; y < rows; y++ {
				if c := term.Screen().Cell(origin.X-1, y).Content; c == "╰" || c == "└" {
					paneBottom = y - 1
					break
				}
			}

			// Scroll it up until its top rows leave the pane.
			typeLine(t, term, fmt.Sprintf("for i in $(seq 1 %d); do echo L$i; done; echo SC''ROLLED", paneBottom-(origin.Y+gap+1)+5))
			if err := term.WaitForText("SCROLLED", shellTimeout); err != nil {
				t.Fatalf("the lines did not print: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
			s := term.Screen()
			shownY := shownAt(s)
			if shownY < 0 {
				t.Fatalf("SHOWN scrolled out of the pane\n%s", s.Text())
			}
			moved := image.Pt(origin.X, shownY-gap)
			if moved.Y >= paneTop {
				t.Fatalf("the picture's top did not leave the pane: origin %v, now %v", origin, moved)
			}
			sc = scoreSymbols(s, img, moved, paneTop, kind)
			if len(sc.bad) > 0 || sc.err >= ceiling || sc.cells == 0 {
				t.Errorf("ASSERTION: the scrolled kitty picture: error %.1f over %d cells; %s\n%s", sc.err, sc.cells, strings.Join(sc.bad, "; "), s.Text())
			}
			if n := imageGlyphCells(s, 0, paneTop); n > 0 {
				t.Errorf("ASSERTION: %d image cells drawn above the pane", n)
			}
			savePNG(t, s, shot.XTermPalette(), artifactDir(t), "scrolled")

			// a=d by id: the picture goes, with no clear.
			typeLine(t, term, "printf '\\033_Ga=d,d=I,i=7,q=2\\033\\\\'; echo DE''LETED")
			if err := term.WaitForText("DELETED", shellTimeout); err != nil {
				t.Fatalf("the delete did not run: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
			if n := imageGlyphCells(term.Screen(), 0, rows); n > 0 {
				t.Errorf("ASSERTION: %d image cells left after a=d,d=I,i=7\n%s", n, term.Screen().Text())
			}
			savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), "deleted")

			// Again, then a=d of all, then again and a clear.
			typeLine(t, term, "clear; cat "+path+"; echo; echo AG''AIN")
			if err := term.WaitForText("AGAIN", shellTimeout); err != nil {
				t.Fatalf("the picture did not print again: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool { return imageGlyphCells(s, 0, rows) > 20 }, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the picture is not on screen the second time\n%s", term.Screen().Text())
			}
			typeLine(t, term, "printf '\\033_Ga=d,d=A,q=2\\033\\\\'; echo AL''LGONE")
			if err := term.WaitForText("ALLGONE", shellTimeout); err != nil {
				t.Fatalf("the delete did not run: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
			if n := imageGlyphCells(term.Screen(), 0, rows); n > 0 {
				t.Errorf("ASSERTION: %d image cells left after a=d,d=A\n%s", n, term.Screen().Text())
			}
			typeLine(t, term, "clear; cat "+path+"; echo; echo THIRD")
			if err := term.WaitForText("THIRD", shellTimeout); err != nil {
				t.Fatalf("the picture did not print a third time: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool { return imageGlyphCells(s, 0, rows) > 20 }, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the picture is not on screen the third time\n%s", term.Screen().Text())
			}
			typeLine(t, term, "clear; echo CL''EARED")
			if err := term.WaitForText("CLEARED", shellTimeout); err != nil {
				t.Fatalf("clear did not run: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
			if n := imageGlyphCells(term.Screen(), 0, rows); n > 0 {
				t.Errorf("ASSERTION: %d image cells left after clear\n%s", n, term.Screen().Text())
			}

			out := host.bytes()[before:]
			if sixelDCS.Match(out) || bytes.Contains(out, []byte("\x1b_G")) {
				t.Errorf("ASSERTION: a host with no graphics was sent graphics")
			}
			if bytes.Contains(host.bytes(), []byte(vt.SixelMarkerLead)) {
				t.Errorf("ASSERTION: an image marker reached the host as text")
			}
		})
	}
}
