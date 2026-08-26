package shot

import (
	"math"
	"testing"
)

// The picture's cell has to be the shape of the font's cell.
//
// The expected numbers here do not come from cellSize, and they do not come
// from anything cellSize calls. They are Go Mono's own hhea table, read out of
// the file with fontTools: unitsPerEm 2048, ascent 1935, descent -432,
// lineGap 0, and the advance of "M" 1229. That is a line box of 1.155762 em and
// an advance of 0.600098 em. Reading them back through the same face cellSize
// asks would have made both sides of the assertion agree with the bug, which is
// exactly how the last aspect fix passed while being wrong.
const (
	goMonoLineBoxEm = (1935.0 + 432.0) / 2048.0
	goMonoAdvanceEm = 1229.0 / 2048.0
)

// TestCellIsTheFontsOwnBox pins the cell against the font's metrics.
//
// Negative control: putting `ch = fs.size * 1.25` back gives 35 px against the
// font's 32.36 and fails the height by 2.6 px, and fails the ratio by 6.6
// percent. Confirmed on the unfixed tree.
func TestCellIsTheFontsOwnBox(t *testing.T) {
	const size = 28.0 // pngBaseFontSize at the default scale of 2
	faces, err := loadFaces(nil, size)
	if err != nil {
		t.Fatal(err)
	}
	cw, ch := faces.cellSize()

	// Full hinting quantizes both up to a whole pixel, so the tolerance is one
	// pixel and no more: anything larger would let the old hardcode through.
	wantW, wantH := goMonoAdvanceEm*size, goMonoLineBoxEm*size
	if math.Abs(cw-wantW) > 1 {
		t.Errorf("cell width %.2f px, want %.2f from the font's advance", cw, wantW)
	}
	if math.Abs(ch-wantH) > 1 {
		t.Errorf("cell height %.2f px, want %.2f from the font's line box", ch, wantH)
	}

	// The ratio is what the reports were about, so it is asserted in its own
	// right rather than left to follow from the two sizes.
	got, want := cw/ch, goMonoAdvanceEm/goMonoLineBoxEm
	if math.Abs(got-want)/want > 0.02 {
		t.Errorf("cell ratio %.4f, want %.4f from the font's own metrics", got, want)
	}
}

// TestCellSurvivesAFaceWithNoMetrics checks the fallback is a fallback and not
// a zero-height cell, which would collapse the whole canvas.
//
// Deliberately passes both ways: it guards the guard, and there is no version
// of the fix under which it fails.
func TestCellSurvivesAFaceWithNoMetrics(t *testing.T) {
	faces, err := loadFaces(nil, 0.001)
	if err != nil {
		t.Fatal(err)
	}
	if _, ch := faces.cellSize(); ch <= 0 {
		t.Errorf("a degenerate size gave a cell %v tall", ch)
	}
}
