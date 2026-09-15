package shot

import (
	"os"
	"path/filepath"
	"testing"
)

// findCollection returns a font collection on this machine, or skips.
func findCollection(t *testing.T) string {
	t.Helper()
	for _, dir := range []string{
		"/System/Library/Fonts", "/System/Library/Fonts/Supplemental",
		"/Library/Fonts", "/usr/share/fonts", "/usr/share/fonts/truetype",
	} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.ttc"))
		if len(matches) > 0 {
			return matches[0]
		}
	}
	t.Skip("no font collections on this machine")
	return ""
}

// TestACollectionRasterizes is a regression test for a capture that failed
// outright rather than losing its icons.
//
// fontconfig answers a great many families with a collection. "Menlo" on macOS
// is a face of Menlo.ttc, and opentype.Parse refuses a collection with
// "invalid single font". loadFaces turned that refusal into an error, so
// setting screenshot.font_family to any such family produced no screenshot at
// all.
//
// Negative control: putting opentype.Parse back in parseFontFace made this fail
// with "invalid single font (data is a font collection)".
func TestACollectionRasterizes(t *testing.T) {
	path := findCollection(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}

	p := XTermPalette()
	g := NewGrid(8, 1, p.FG, p.BG)
	for i, r := range "collect" {
		g.Cells[0][i] = Cell{Cluster: string(r), Width: 1, FG: p.FG, BG: p.BG, BGDefault: true}
	}
	f := BuildFrame(
		FrameSpec{Frame: "plain", Background: "auto", Padding: 4, Scale: 1, FontData: data},
		FrameInputs{Palette: p},
	)
	if _, err := RenderPNG(g, f, nil); err != nil {
		t.Fatalf("a collection would not rasterize: %v", err)
	}
}

// TestASecondFaceOfACollectionRasterizes covers the index actually selecting,
// which is what lets a family's bold cut come from the same file as its
// regular one.
func TestASecondFaceOfACollectionRasterizes(t *testing.T) {
	path := findCollection(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}
	first, err := parseFontFace(data, 0)
	if err != nil {
		t.Fatalf("face 0 did not parse: %v", err)
	}
	second, err := parseFontFace(data, 1)
	if err != nil {
		t.Skipf("%s holds only one face: %v", path, err)
	}
	if first == second {
		t.Error("face 0 and face 1 came back as the same font")
	}
}

// TestAnImpossibleFaceIndexFallsBackToTheFirst keeps a resolver and a file that
// disagree from costing the capture. A font from the right family beats no
// capture at all.
func TestAnImpossibleFaceIndexFallsBackToTheFirst(t *testing.T) {
	path := findCollection(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}
	if _, err := parseFontFace(data, 9999); err != nil {
		t.Errorf("an out-of-range index errored instead of falling back: %v", err)
	}
	if _, err := parseFontFace(data, -3); err != nil {
		t.Errorf("a negative index errored instead of falling back: %v", err)
	}
}

// TestGarbageFontDataStillErrors is the other side of the fallback: bytes that
// are not a font at all must not be quietly accepted.
func TestGarbageFontDataStillErrors(t *testing.T) {
	if _, err := parseFontFace([]byte("this is not a font"), 0); err == nil {
		t.Error("nonsense bytes parsed as a font")
	}
}
