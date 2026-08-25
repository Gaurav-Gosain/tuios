package app

import "testing"

// The preview's letterbox, pinned against a cell size the test states.
//
// The version of this test that shipped asked fitBoxToPicture for the cell size
// with the same call fitBoxToPicture made, so both sides of every assertion
// were wrong in the same direction and the test could not see it. The cell size
// is an argument here for exactly that reason.

// TestPreviewBoxKeepsThePicturesShape pins the letterbox. a=p scales an image
// to whatever cell box it is handed, so a box chosen from the panel alone
// stretched a wide capture tall and a tall one wide.
func TestPreviewBoxKeepsThePicturesShape(t *testing.T) {
	const cellW, cellH = 10, 20

	// A picture twice as wide as the box's proportions must lose rows, not
	// gain width it does not have.
	cols, rows := fitBoxToPicture(40, 40, 400, 100, cellW, cellH)
	if cols != 40 {
		t.Errorf("a wide picture gave up columns: %d, want the full 40", cols)
	}
	if rows >= 40 {
		t.Errorf("a wide picture kept %d rows; it should letterbox", rows)
	}

	// And the other way round.
	cols, rows = fitBoxToPicture(40, 40, 100, 400, cellW, cellH)
	if rows != 40 {
		t.Errorf("a tall picture gave up rows: %d, want the full 40", rows)
	}
	if cols >= 40 {
		t.Errorf("a tall picture kept %d columns; it should letterbox", cols)
	}

	// An unknown size is the old behaviour rather than a guess.
	if c, r := fitBoxToPicture(30, 12, 0, 0, cellW, cellH); c != 30 || r != 12 {
		t.Errorf("unknown picture size changed the box to %dx%d, want 30x12", c, r)
	}
}

// TestPreviewBoxIsTheRightSizeAndNotJustTheRightShape is the assertion the
// shipped test could not make: the box in host pixels has to have the picture's
// own proportions, to a cell.
//
// Negative control: passing the launcher's icon box (cellW*2) as the cell width
// -- which is what the caller used to do -- gives 25 columns here instead of
// 51, and this fails. Widening the tolerance to two cells still failed, so the
// assertion is about the arithmetic and not about rounding.
func TestPreviewBoxIsTheRightSizeAndNotJustTheRightShape(t *testing.T) {
	const cellW, cellH = 10, 22
	// The measurements a real capture produced on a 120x40 host: a 586x450
	// picture offered the whole 74x18 panel body.
	const picW, picH = 586, 450

	cols, rows := fitBoxToPicture(74, 18, picW, picH, cellW, cellH)
	if rows != 18 {
		t.Fatalf("the picture is wider than the body's proportions, so it should keep all 18 rows, got %d", rows)
	}
	want := picW * (rows * cellH) / picH / cellW
	if cols < want-1 || cols > want+1 {
		t.Fatalf("the picture is drawn %d cells wide, want %d: it is %.0f%% of the width it should be",
			cols, want, 100*float64(cols)/float64(want))
	}

	drawn := float64(cols*cellW) / float64(rows*cellH)
	own := float64(picW) / float64(picH)
	if drawn < own*0.97 || drawn > own*1.03 {
		t.Errorf("the picture is drawn at aspect %.3f, its own is %.3f", drawn, own)
	}
}

// TestPreviewBoxNeverOutgrowsTheBody pins the invariant that a picture is never
// drawn past the panel that owns it. A box taller than the body puts the
// capture on the footer and on whatever is under the panel.
//
// This is a deliberate passes-both-ways control: with the arithmetic as
// written, only one axis is ever reduced and the other is left exactly as
// offered, so the invariant holds by construction and no negative control makes
// this fail. It is here to catch a future change to the branch arithmetic,
// which is where the invariant would be lost.
func TestPreviewBoxNeverOutgrowsTheBody(t *testing.T) {
	const cellW, cellH = 10, 21
	for _, tc := range []struct {
		name       string
		cols, rows int
		picW, picH int
	}{
		{name: "tall picture in a wide body", cols: 40, rows: 12, picW: 100, picH: 900},
		{name: "wide picture in a tall body", cols: 12, rows: 40, picW: 900, picH: 100},
		{name: "picture matching the body", cols: 30, rows: 10, picW: 30 * cellW, picH: 10 * cellH},
		{name: "one cell of body", cols: 1, rows: 1, picW: 640, picH: 480},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows := fitBoxToPicture(tc.cols, tc.rows, tc.picW, tc.picH, cellW, cellH)
			if cols < 1 || rows < 1 {
				t.Fatalf("box collapsed to %dx%d", cols, rows)
			}
			if cols > tc.cols || rows > tc.rows {
				t.Fatalf("box %dx%d is larger than the %dx%d body it was offered",
					cols, rows, tc.cols, tc.rows)
			}
		})
	}
}
