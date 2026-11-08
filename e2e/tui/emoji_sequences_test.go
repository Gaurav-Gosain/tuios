package tuie2e

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	shotcapture "github.com/Gaurav-Gosain/tuios/internal/capture"
	"tuios.dev/shot"
)

// emojiSequences are the clusters a colour emoji font draws as one glyph made
// of several code points, one of each kind: a flag (two regional indicators),
// a ZWJ sequence, a keycap and a skin tone.
var emojiSequences = []struct{ name, cluster string }{
	{"flag", "\U0001F1EF\U0001F1F5"},                             // JP
	{"zwj family", "\U0001F468\u200D\U0001F469\u200D\U0001F467"}, // man, woman, girl
	{"zwj technologist", "\U0001F9D1\u200D\U0001F4BB"},           // person, laptop
	{"keycap", "1\uFE0F\u20E3"},
	{"skin tone", "\U0001F44D\U0001F3FD"}, // thumbs up, medium
}

// TestScreenshotDrawsEmojiSequences draws each sequence in the fonts tuios
// screenshot finds on this machine (JetBrainsMono, then the system's colour
// emoji font by fontconfig or the macOS fallback), and holds it to the
// glyph the font has for the whole sequence: it must cover the cell the way
// an emoji picture does (Apple and Noto cover about half of it or more, grey
// silhouettes included), and differ from its first code point drawn alone. A renderer that draws a
// cluster's first rune draws a flag as a letter, a keycap as a bare digit, a
// family as its first person and a skin tone as the yellow hand.
//
// How it could pass wrongly: the first code point alone could be the box a
// font draws for a glyph it lacks, which any drawing differs from, so the
// sequence must also cover a quarter of the cell, which no letter, digit or
// box does. The PNG of every sequence is the
// artifact.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestScreenshotDrawsEmojiSequences).
func TestScreenshotDrawsEmojiSequences(t *testing.T) {
	regular, bold := frameFontPaths()
	if regular == "" {
		t.Skip("JetBrainsMono Nerd Font Mono is not installed")
	}
	reg, err1 := os.ReadFile(regular)
	bol, err2 := os.ReadFile(bold)
	if err1 != nil || err2 != nil {
		t.Skipf("read the fonts: %v %v", err1, err2)
	}
	p := shot.XTermPalette()
	frame := func() *shot.Frame {
		return &shot.Frame{Mode: shot.FrameNone, Font: shot.Font{Data: reg}, BoldFont: shot.Font{Data: bol}}
	}
	// One grid of every sequence, for the artifact and for the fallbacks.
	all := shot.NewGrid(3*len(emojiSequences), 1, p.FG, p.BG)
	for i, s := range emojiSequences {
		all.Cells[0][3*i] = shot.Cell{Cluster: s.cluster, Width: 2, FG: p.FG, BG: p.BG}
	}
	f := frame()
	shotcapture.FillFallbacks(all, f)
	if missing, err := shot.MissingGlyphs(all, f); err != nil || len(missing) > 0 {
		t.Skipf("this machine has no colour emoji font for %q (%v)", string(missing), err)
	}
	if data, err := shot.Render(shot.FormatPNG, all, f); err == nil {
		_ = os.WriteFile(filepath.Join(artifactDir(t), "emoji-sequences.png"), data, 0o644)
	}
	draw := func(cluster string) *image.RGBA {
		one := shot.NewGrid(2, 1, p.FG, p.BG)
		one.Cells[0][0] = shot.Cell{Cluster: cluster, Width: 2, FG: p.FG, BG: p.BG}
		img, err := shot.Raster(one, &shot.Frame{Mode: shot.FrameNone, Font: f.Font, BoldFont: f.BoldFont, Fallback: f.Fallback})
		if err != nil {
			t.Fatalf("draw %q: %v", cluster, err)
		}
		return img
	}
	for _, s := range emojiSequences {
		t.Run(s.name, func(t *testing.T) {
			img := draw(s.cluster)
			r, _ := utf8.DecodeRuneInString(s.cluster)
			first := draw(string(r))
			b := img.Bounds()
			ink, differ := 0, 0
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c, o := img.RGBAAt(x, y), first.RGBAAt(x, y)
					if absDiff(int(c.R), int(p.BG.R))+absDiff(int(c.G), int(p.BG.G))+absDiff(int(c.B), int(p.BG.B)) > 96 {
						ink++
					}
					if absDiff(int(c.R), int(o.R))+absDiff(int(c.G), int(o.G))+absDiff(int(c.B), int(o.B)) > 96 {
						differ++
					}
				}
			}
			area := b.Dx() * b.Dy()
			if ink < area/4 {
				t.Errorf("ASSERTION: %s %q draws ink on %d pixels of %d: a letter, a digit or a box, not the sequence's emoji", s.name, s.cluster, ink, area)
			}
			if differ < area/20 {
				t.Errorf("ASSERTION: %s %q draws as its first code point alone (%d pixels of %d differ)", s.name, s.cluster, differ, area)
			}
		})
	}
}
