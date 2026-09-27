package tuie2e

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The glyphs follow the terminal when nobody chose them: ASCII under a locale
// that is not UTF-8, plain Unicode (no Nerd Font icons) on the Linux console,
// and an explicit glyph set always wins.
//
// Each run opens a pane and the command palette, so the frame carries a pane
// border, the dock's icons and a panel, and reads every cell of it.
//
// How this could pass wrongly, written down first:
//   - The frame could be read before the chrome is drawn: each run waits for
//     a pane and the palette's title first.
//   - "No Nerd Font glyph" passes trivially on an ASCII frame, so the console
//     run also requires box drawing on screen, which is what tells plain
//     Unicode from ASCII.
//   - The explicit set could win by accident if detection never ran: the
//     C-locale run without a set is the one that proves detection runs, and
//     the heavy run under the same locale has to draw non-ASCII.
//
// The frames are saved under artifactDir.
func TestGlyphsFollowTheTerminal(t *testing.T) {
	runs := []struct {
		name, config string
		env          []string
		check        func(r rune) string
		needBox      bool
		needNonASCII bool
	}{
		{name: "c-locale", env: []string{"LANG=C"}, check: func(r rune) string {
			if r > 0x7e {
				return "outside ASCII"
			}
			return ""
		}},
		{name: "lc-all-latin1", env: []string{"LC_ALL=en_US.ISO-8859-1", "LANG=en_US.UTF-8"}, check: func(r rune) string {
			if r > 0x7e {
				return "outside ASCII"
			}
			return ""
		}},
		{name: "linux-console", env: []string{"TERM=linux"}, needBox: true, check: func(r rune) string {
			if (r >= 0xe000 && r <= 0xf8ff) || r >= 0xf0000 {
				return "a private-use (Nerd Font) glyph"
			}
			return ""
		}},
		{name: "c-locale-heavy-chosen", env: []string{"LANG=C"}, config: "[appearance]\nglyphs = \"heavy\"\n", needNonASCII: true},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			base := t.TempDir()
			if r.config != "" {
				writeConfig(t, base, r.config)
			}
			term := startIn(t, base, startOpts{env: r.env})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "opening a pane")
			if err := term.SendKeys(legacyCtrlP); err != nil {
				t.Fatalf("open the palette: %v", err)
			}
			waitPaletteOpen(t, term, "for the glyph check")
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("screen never settled: %v", err)
			}
			saveArtifact(t, term, artifactDir(t), "palette")
			s := term.Screen()
			box, nonASCII := false, false
			cols, rows := s.Size()
			for y := range rows {
				for x := range cols {
					for _, ch := range s.Cell(x, y).Content {
						if r.check != nil {
							if why := r.check(ch); why != "" {
								t.Fatalf("cell (%d,%d) is %q (U+%04X), %s\n%s", x, y, string(ch), ch, why, term.Snapshot())
							}
						}
						box = box || (ch >= 0x2500 && ch <= 0x257f)
						nonASCII = nonASCII || ch > 0x7e
					}
				}
			}
			if r.needBox && !box {
				t.Fatalf("no box drawing on the console frame; plain Unicode should keep it\n%s", term.Snapshot())
			}
			if r.needNonASCII && !nonASCII {
				t.Fatalf("a chosen glyph set was overruled by the locale\n%s", term.Snapshot())
			}
		})
	}
}

// TestOldAnimationSwitchMigrates starts a client whose config still says
// animations_enabled, the on/off switch appearance.motion replaced, and reads
// the result off the screen: false is the none level, so the palette comes up
// at its final colour on the first frame; true is the shipped full level, so
// it fades.
//
// How this could pass wrongly, written down first:
//   - A fade can be too fast to catch on a loaded machine, which would make
//     "true" look like "none". The true run opens the palette up to three
//     times and needs one fade; the false run needs none in three.
func TestOldAnimationSwitchMigrates(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprintf("animations_enabled=%v", on), func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			useShippedLooks(base)
			writeConfig(t, base, fmt.Sprintf("[startup]\ntiled = true\n[appearance]\nanimations_enabled = %v\n", on))
			if out, err := tuiosCLI(t, base, "new", "e2e-migrate", "--detach"); err != nil {
				t.Fatalf("create session: %v\n%s", err, out)
			}
			env := []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
			term := startIn(t, base, startOpts{args: []string{"attach", "e2e-migrate"}, shippedLooks: true, env: env, animations: true})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			windowManagementMode(t, term)
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("screen never settled: %v", err)
			}
			faded := false
			for range 3 {
				faded = len(sampleFade(t, term)) > 1
				closeAndHold(t, term)
				if faded {
					break
				}
			}
			saveArtifact(t, term, artifactDir(t), "closed")
			if faded != on {
				t.Fatalf("animations_enabled = %v: the palette faded = %v", on, faded)
			}
		})
	}
}
