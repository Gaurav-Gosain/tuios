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
