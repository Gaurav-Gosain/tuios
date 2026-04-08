package ghostty

/*
#cgo CFLAGS: -I/home/gaurav/dev/ghostty/zig-out/include
#cgo LDFLAGS: -L/home/gaurav/dev/ghostty/zig-out/lib -lghostty-vt -lm -lpthread -lc

#include <ghostty/vt.h>
#include <stdlib.h>
*/
import "C"
import "unsafe"

// CellData holds the rendered data for a single terminal cell.
type CellData struct {
	Content  string // UTF-8 grapheme cluster
	FgR, FgG, FgB uint8
	FgSet    bool
	BgR, BgG, BgB uint8
	BgSet    bool
	Bold     bool
	Italic   bool
	Underline bool
	Strikethrough bool
	Wide     bool
}

// ScreenDiff contains only the changed rows since the last render.
type ScreenDiff struct {
	Rows     []RowDiff
	Cursor   CursorInfo
	Cols     int
	RowCount int
	FullRedraw bool
}

// RowDiff contains the cells for one changed row.
type RowDiff struct {
	Y     int
	Cells []CellData
}

// ReadDirtyRows reads all dirty rows from the render state and returns a ScreenDiff.
// Clears dirty flags after reading so the next call only returns new changes.
func (t *Terminal) ReadDirtyRows() *ScreenDiff {
	if t.render == nil {
		return nil
	}

	dirty := t.UpdateRenderState()
	if dirty == DirtyFalse {
		return nil
	}

	cols, rows := t.GetDimensions()
	diff := &ScreenDiff{
		Cursor:     t.GetCursor(),
		Cols:       cols,
		RowCount:   rows,
		FullRedraw: dirty == DirtyFull,
	}

	// Create row iterator
	var rowIter C.GhosttyRenderStateRowIterator
	if C.ghostty_render_state_row_iterator_new(nil, &rowIter) != 0 {
		return diff
	}
	defer C.ghostty_render_state_row_iterator_free(rowIter)

	// Populate iterator from render state
	C.ghostty_render_state_get(
		t.render,
		C.GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR,
		unsafe.Pointer(&rowIter),
	)

	// Create reusable cells iterator
	var cellIter C.GhosttyRenderStateRowCells
	if C.ghostty_render_state_row_cells_new(nil, &cellIter) != 0 {
		return diff
	}
	defer C.ghostty_render_state_row_cells_free(cellIter)

	// Grapheme buffer (reusable)
	graphemeBuf := make([]C.uint32_t, 32)

	y := 0
	for C.ghostty_render_state_row_iterator_next(rowIter) {
		// Check if this row is dirty
		var rowDirty C.bool
		C.ghostty_render_state_row_get(rowIter, C.GHOSTTY_RENDER_STATE_ROW_DATA_DIRTY, unsafe.Pointer(&rowDirty))

		if bool(rowDirty) || diff.FullRedraw {
			// Read cells for this row
			C.ghostty_render_state_row_get(rowIter, C.GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, unsafe.Pointer(&cellIter))

			rowData := RowDiff{Y: y, Cells: make([]CellData, 0, cols)}

			for C.ghostty_render_state_row_cells_next(cellIter) {
				var cell CellData

				// Get grapheme
				var graphemeLen C.uint32_t
				C.ghostty_render_state_row_cells_get(cellIter, C.GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_LEN, unsafe.Pointer(&graphemeLen))

				if graphemeLen > 0 {
					if int(graphemeLen) > len(graphemeBuf) {
						graphemeBuf = make([]C.uint32_t, graphemeLen)
					}
					C.ghostty_render_state_row_cells_get(cellIter, C.GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_BUF, unsafe.Pointer(&graphemeBuf[0]))

					// Convert codepoints to UTF-8
					runes := make([]rune, graphemeLen)
					for i := 0; i < int(graphemeLen); i++ {
						runes[i] = rune(graphemeBuf[i])
					}
					cell.Content = string(runes)
				}

				// Get foreground color
				var fgColor C.GhosttyColorRgb
				fgResult := C.ghostty_render_state_row_cells_get(cellIter, C.GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, unsafe.Pointer(&fgColor))
				if fgResult == 0 { // GHOSTTY_SUCCESS
					cell.FgR = uint8(fgColor.r)
					cell.FgG = uint8(fgColor.g)
					cell.FgB = uint8(fgColor.b)
					cell.FgSet = true
				}

				// Get background color
				var bgColor C.GhosttyColorRgb
				bgResult := C.ghostty_render_state_row_cells_get(cellIter, C.GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, unsafe.Pointer(&bgColor))
				if bgResult == 0 {
					cell.BgR = uint8(bgColor.r)
					cell.BgG = uint8(bgColor.g)
					cell.BgB = uint8(bgColor.b)
					cell.BgSet = true
				}

				// Get style (bold, italic, etc.)
				var style C.GhosttyStyle
				if C.ghostty_render_state_row_cells_get(cellIter, C.GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, unsafe.Pointer(&style)) == 0 {
					cell.Bold = bool(style.bold)
					cell.Italic = bool(style.italic)
					cell.Strikethrough = bool(style.strikethrough)
				}

				rowData.Cells = append(rowData.Cells, cell)
			}

			diff.Rows = append(diff.Rows, rowData)

			// Clear row dirty flag
			falseVal := C.bool(false)
			C.ghostty_render_state_row_set(rowIter, C.GHOSTTY_RENDER_STATE_ROW_OPTION_DIRTY, unsafe.Pointer(&falseVal))
		}

		y++
	}

	// Clear global dirty state
	cleanState := C.GhosttyRenderStateDirty(C.GHOSTTY_RENDER_STATE_DIRTY_FALSE)
	C.ghostty_render_state_set(t.render, C.GHOSTTY_RENDER_STATE_OPTION_DIRTY, unsafe.Pointer(&cleanState))

	return diff
}
