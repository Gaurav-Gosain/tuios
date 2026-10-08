package tuie2e

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuitest"
)

// Issue 567: chafa -f kitty in a pane, in Netcatty, leaves the picture's
// bottom rows behind each time the pane scrolls, and a grey checker where the
// picture was after clear.
//
// Netcatty is xterm.js with its image addon. The addon writes a kitty image
// into the cells under it, the way it draws a sixel: a second placement with
// the same ids is a second picture, text does not erase one, and deleting one
// leaves its cells drawing the addon's placeholder checker. tuios moves a
// pane's picture by placing it again and clears it by deleting it, so every
// move left a row behind and every clear left the checker. tuios now reads the
// host's XTVERSION answer and, for xterm.js, turns both image protocols off and
// draws the picture as block glyphs.
//
// How this could pass wrongly, written down first:
//   - The probe could not read the XTVERSION answer at all, and the host be
//     taken for one without graphics for some other reason. The positive half
//     answers as kitty with the same probe and must get the picture as kitty
//     graphics.
//   - The host could be sent nothing because the picture never reached tuios.
//     The xterm.js host must show the sixel picture as glyphs, and the pane
//     must be told sixel, so a tool that asks gets a picture.
//   - A later DA1 answer could turn sixel back on. The pane is asked for DA1
//     after the probe and the picture is drawn after that.

// xtermJSVersion is what Netcatty's xterm.js answers to XTVERSION.
const xtermJSVersion = "xterm.js(6.1.0-beta.292)"

// kittyVersion is what kitty answers.
const kittyVersion = "kitty(0.49.2)"

// cellBoundPNG is a small opaque picture, sent as a kitty a=T transmission.
func cellBoundPNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8*cellW, 3*cellH))
	for y := range img.Bounds().Dy() {
		for x := range img.Bounds().Dx() {
			img.Set(x, y, color.RGBA{200, uint8(x), uint8(y), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestCellBoundImageHostGetsGlyphs(t *testing.T) {
	hostterm := buildHostTerm(t)
	for _, tc := range []struct {
		name    string
		version string
		daemon  bool
	}{
		{"xtermjs-standalone", xtermJSVersion, false},
		{"xtermjs-daemon", xtermJSVersion, true},
		{"kitty-standalone", kittyVersion, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xtermJS := tc.version == xtermJSVersion
			// tuitest's emulator answers the kitty query OK and lists
			// sixel in DA1, the way Netcatty does with both protocols on.
			// hostterm sits in front of it and names the host.
			host := newSixelHost(true, true)
			term, base := start(t, startOpts{
				cols: 120, rows: 40,
				out:           host,
				daemonDefault: tc.daemon,
				env:           []string{"TUIOS_CELL_SIZE=10x20"},
				wrap:          []string{hostterm, "run", "-xtversion", tc.version, "--"},
			})
			if tc.daemon {
				t.Cleanup(func() { killDaemon(t, base) })
			}
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, "echo RE''ADY", "READY", shellTimeout)

			dir := t.TempDir()
			if da1 := paneDA1(t, term, dir); !strings.Contains(";"+da1+";", ";4;") {
				t.Fatalf("pane DA1 = %q: sixel not listed, so programs would not send a picture", da1)
			}

			before := len(host.bytes())
			// The reporter's case: a kitty picture, then the pane scrolls.
			runToExit(t, term, `printf '\033_Ga=T,q=2,f=100;`+cellBoundPNG(t)+`\033\\'; seq 3`)
			f := writeSixelFixture(t, dir, 20, 4)
			origin := showFixture(t, term, f, "IMGTOP")
			out := host.bytes()[before:]
			s := term.Screen()
			savePNG(t, s, shot.XTermPalette(), artifactDir(t), "after")

			kitty := bytes.Contains(out, []byte("\x1b_G"))
			sixel := sixelDCS.Match(out)
			painted := 0
			for r := range f.rows {
				for c := range f.cols {
					if s.Cell(origin.X+c, origin.Y+r).Bg.Kind != tuitest.ColorDefault {
						painted++
					}
				}
			}
			t.Logf("%s: kitty sent %v, sixel sent %v, %d of %d picture cells painted",
				tc.name, kitty, sixel, painted, f.cols*f.rows)

			if xtermJS {
				if kitty {
					t.Errorf("an xterm.js host was sent kitty graphics, which it writes into its cells and never clears")
				}
				if sixel {
					t.Errorf("an xterm.js host was sent a sixel, which it writes into its cells and never clears")
				}
				if painted != f.cols*f.rows {
					t.Errorf("the sixel picture is not drawn as glyphs: %d of %d cells painted\n%s", painted, f.cols*f.rows, s.Text())
				}
				return
			}
			if !kitty {
				t.Errorf("a kitty host was sent no kitty graphics: the probe did not read the host")
			}
			if painted != 0 {
				t.Errorf("a kitty host got the picture as glyphs: %d cells painted", painted)
			}
		})
	}
}
