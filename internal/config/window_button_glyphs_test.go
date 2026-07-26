package config

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The window control pill is drawn into a fixed grid of cells, and every
// single-width grapheme in it gets exactly one cell. A glyph the font does not
// cover falls back to some proportional system font whose advance overruns that
// cell, and the overrun is then clipped or overpainted by whatever comes next,
// which is how the close button lost the right half of its X. These tests pin
// both halves of that contract: the chrome runes must measure one cell, and the
// font tuios ships must actually have them.

// windowChromeRunes lists every rune tuios paints into window decorations
// through the Nerd Font path. Each must be covered by the bundled font.
func windowChromeRunes(t *testing.T) map[rune]string {
	t.Helper()
	out := map[rune]string{}
	add := func(name, s string) {
		for _, r := range s {
			if r == ' ' {
				continue
			}
			out[r] = name
		}
	}
	add("WindowButtonClose", WindowButtonClose)
	add("WindowButtonMinimize", "-")
	add("WindowButtonMaximize", "□")
	add("WindowPillLeft", WindowPillLeft)
	add("WindowPillRight", WindowPillRight)
	add("WindowBorderTopLeft", WindowBorderTopLeft)
	add("WindowBorderTopRight", WindowBorderTopRight)
	add("WindowBorderBottomLeft", WindowBorderBottomLeft)
	add("WindowBorderBottomRight", WindowBorderBottomRight)
	add("WindowBorderHorizontal", WindowBorderHorizontal)
	add("WindowBorderVertical", WindowBorderVertical)
	return out
}

func TestWindowChromeRunesAreSingleWidth(t *testing.T) {
	for r, name := range windowChromeRunes(t) {
		if w := ansi.StringWidth(string(r)); w != 1 {
			t.Errorf("%s: U+%04X measures %d cells, want 1", name, r, w)
		}
	}

	// The pill contents are what the mouse hit-test offsets are measured
	// against, so their total width is load-bearing, not cosmetic.
	if w := ansi.StringWidth(WindowButtonClose); w != 3 {
		t.Errorf("WindowButtonClose measures %d cells, want 3", w)
	}
	if w := ansi.StringWidth(WindowButtonCloseASCII); w != 3 {
		t.Errorf("WindowButtonCloseASCII measures %d cells, want 3", w)
	}
}

// TestWindowChromeRunesAreInBundledFont is the direct guard against the defect:
// U+292B was used for the close button but is absent from JetBrainsMono Nerd
// Font, so it was drawn from a fallback font whose glyph was wider than a cell
// and got clipped. The check needs font files to read, so it skips where none
// are present rather than passing vacuously.
func TestWindowChromeRunesAreInBundledFont(t *testing.T) {
	fonts := bundledFontPaths(t)
	if len(fonts) == 0 {
		t.Skip("bundled web fonts not found; nothing to check")
	}

	runes := windowChromeRunes(t)
	for _, path := range fonts {
		cmap, err := readTrueTypeCmap(path)
		if err != nil {
			t.Fatalf("reading %s: %v", filepath.Base(path), err)
		}
		if len(cmap) == 0 {
			t.Fatalf("%s: parsed an empty cmap", filepath.Base(path))
		}
		for r, name := range runes {
			if !cmap[r] {
				t.Errorf("%s: %s U+%04X is not in %s; the renderer will fall "+
					"back to a proportional font and clip the glyph to one cell",
					filepath.Base(path), name, r, filepath.Base(path))
			}
		}
	}
}

// bundledFontPaths locates web/fonts relative to the repository root.
func bundledFontPaths(t *testing.T) []string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	for range 6 {
		candidate := filepath.Join(dir, "web", "fonts")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			matches, err := filepath.Glob(filepath.Join(candidate, "*.ttf"))
			if err != nil {
				return nil
			}
			return matches
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}

// readTrueTypeCmap returns the set of Unicode code points a TrueType file maps
// to a glyph. Only the two subtable formats the bundled fonts use (4 and 12)
// are decoded; a file that offered neither would come back empty and fail the
// caller's sanity check rather than pass vacuously.
func readTrueTypeCmap(path string) (map[rune]bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // test-only, path is repo-local
	if err != nil {
		return nil, err
	}
	if len(data) < 12 {
		return nil, errShortFont
	}

	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	cmapOff := 0
	for i := range numTables {
		rec := 12 + i*16
		if rec+16 > len(data) {
			return nil, errShortFont
		}
		if string(data[rec:rec+4]) == "cmap" {
			cmapOff = int(binary.BigEndian.Uint32(data[rec+8 : rec+12]))
			break
		}
	}
	if cmapOff == 0 || cmapOff+4 > len(data) {
		return nil, errShortFont
	}

	out := map[rune]bool{}
	numSub := int(binary.BigEndian.Uint16(data[cmapOff+2 : cmapOff+4]))
	for i := range numSub {
		rec := cmapOff + 4 + i*8
		if rec+8 > len(data) {
			return nil, errShortFont
		}
		platform := binary.BigEndian.Uint16(data[rec : rec+2])
		encoding := binary.BigEndian.Uint16(data[rec+2 : rec+4])
		// Unicode platform, or Windows BMP/full-repertoire.
		if platform != 0 && !(platform == 3 && (encoding == 1 || encoding == 10)) {
			continue
		}
		sub := cmapOff + int(binary.BigEndian.Uint32(data[rec+4:rec+8]))
		if sub+4 > len(data) {
			continue
		}
		switch binary.BigEndian.Uint16(data[sub : sub+2]) {
		case 4:
			parseCmapFormat4(data, sub, out)
		case 12:
			parseCmapFormat12(data, sub, out)
		}
	}
	return out, nil
}

func parseCmapFormat4(data []byte, sub int, out map[rune]bool) {
	if sub+14 > len(data) {
		return
	}
	segX2 := int(binary.BigEndian.Uint16(data[sub+6 : sub+8]))
	segs := segX2 / 2
	endOff := sub + 14
	startOff := endOff + segX2 + 2
	if startOff+segX2 > len(data) {
		return
	}
	for s := range segs {
		end := binary.BigEndian.Uint16(data[endOff+s*2 : endOff+s*2+2])
		start := binary.BigEndian.Uint16(data[startOff+s*2 : startOff+s*2+2])
		if start > end || end == 0xFFFF {
			continue
		}
		// The glyph id lookup is irrelevant here: presence in a segment is
		// enough to say the font claims the code point.
		for c := rune(start); c <= rune(end); c++ {
			out[c] = true
		}
	}
}

func parseCmapFormat12(data []byte, sub int, out map[rune]bool) {
	if sub+16 > len(data) {
		return
	}
	nGroups := int(binary.BigEndian.Uint32(data[sub+12 : sub+16]))
	for g := range nGroups {
		rec := sub + 16 + g*12
		if rec+12 > len(data) {
			return
		}
		start := rune(binary.BigEndian.Uint32(data[rec : rec+4]))
		end := rune(binary.BigEndian.Uint32(data[rec+4 : rec+8]))
		if start > end || end-start > 0x10000 {
			// Guard against a pathological range blowing up the map; the
			// chrome runes all sit in small, ordinary ranges.
			end = start
		}
		for c := start; c <= end; c++ {
			out[c] = true
		}
	}
}

type fontErr string

func (e fontErr) Error() string { return string(e) }

const errShortFont = fontErr("truncated or malformed font file")
