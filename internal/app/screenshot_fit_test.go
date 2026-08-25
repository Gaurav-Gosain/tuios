package app

import "testing"

// TestPreviewBoxKeepsThePicturesShape pins the letterbox. a=p scales an image
// to whatever cell box it is handed, so a box chosen from the panel alone
// stretched a wide capture tall and a tall one wide.
func TestPreviewBoxKeepsThePicturesShape(t *testing.T) {
	cellW, cellH := iconCellSize()
	if cellW <= 0 || cellH <= 0 {
		t.Skip("no cell size on this host")
	}

	// A picture twice as wide as the box's proportions must lose rows, not
	// gain width it does not have.
	cols, rows := fitBoxToPicture(40, 40, 400, 100)
	if cols != 40 {
		t.Errorf("a wide picture gave up columns: %d, want the full 40", cols)
	}
	if rows >= 40 {
		t.Errorf("a wide picture kept %d rows; it should letterbox", rows)
	}

	// And the other way round.
	cols, rows = fitBoxToPicture(40, 40, 100, 400)
	if rows != 40 {
		t.Errorf("a tall picture gave up rows: %d, want the full 40", rows)
	}
	if cols >= 40 {
		t.Errorf("a tall picture kept %d columns; it should letterbox", cols)
	}

	// An unknown size is the old behaviour rather than a guess.
	if c, r := fitBoxToPicture(30, 12, 0, 0); c != 30 || r != 12 {
		t.Errorf("unknown picture size changed the box to %dx%d, want 30x12", c, r)
	}
}
