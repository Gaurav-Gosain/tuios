package tuie2e

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

const xtermJSVersion = "xterm.js(6.1.0-beta.292)"

const kittyVersion = "kitty(0.49.2)"

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
	lateDA1 := []string{"-da1", "?62;4;9;22c", "-da1-delay", "500ms"}
	for _, tc := range []struct {
		name    string
		version string
		daemon  bool
		host    []string
	}{
		{"xtermjs-standalone", xtermJSVersion, false, nil},
		{"xtermjs-daemon", xtermJSVersion, true, nil},
		{"xtermjs-late-da1", xtermJSVersion, false, lateDA1},
		{"xtermjs-late-both", xtermJSVersion, false, append([]string{"-xtversion-delay", "500ms"}, lateDA1...)},
		{"xtermjs-late-both-daemon", xtermJSVersion, true, append([]string{"-xtversion-delay", "500ms"}, lateDA1...)},
		{"kitty-standalone", kittyVersion, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xtermJS := tc.version == xtermJSVersion
			host := newSixelHost(true, true)
			term, base := start(t, startOpts{
				cols: 120, rows: 40,
				out:           host,
				daemonDefault: tc.daemon,
				env:           []string{"TUIOS_CELL_SIZE=10x20"},
				wrap:          append(append([]string{hostterm, "run", "-xtversion", tc.version}, tc.host...), "--"),
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
			runToExit(t, term, `printf '\033_Ga=T,q=2,f=100;`+cellBoundPNG(t)+`\033\\'; seq 3`)
			f := writeSixelFixture(t, dir, 20, 4)
			origin := showFixture(t, term, f, "IMGTOP")
			out := host.bytes()[before:]
			s := term.Screen()
			paintedIn := func(s tuitest.Screen) int {
				n := 0
				for r := range f.rows {
					for c := range f.cols {
						if s.Cell(origin.X+c, origin.Y+r).Bg.Kind != tuitest.ColorDefault {
							n++
						}
					}
				}
				return n
			}
			painted := paintedIn(s)
			for deadline := time.Now().Add(5 * time.Second); xtermJS && painted != f.cols*f.rows && time.Now().Before(deadline); {
				time.Sleep(50 * time.Millisecond)
				s = term.Screen()
				painted = paintedIn(s)
			}
			savePNG(t, s, shot.XTermPalette(), artifactDir(t), "after")

			kitty := bytes.Contains(out, []byte("\x1b_G"))
			sixel := sixelDCS.Match(out)
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
