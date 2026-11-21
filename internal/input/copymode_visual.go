package input

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

func enterVisualChar(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	cm.State = terminal.CopyModeVisualChar
	absY := getAbsoluteY(cm, grid)
	cm.VisualStart = terminal.Position{X: cm.CursorX, Y: absY}
	cm.VisualEnd = cm.VisualStart
}

func enterVisualLine(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	cm.State = terminal.CopyModeVisualLine
	absY := getAbsoluteY(cm, grid)

	_, endX := getLineContentBounds(cm, grid, absY)

	cm.VisualStart = terminal.Position{X: 0, Y: absY}
	cm.VisualEnd = terminal.Position{X: endX, Y: absY}
}

func enterVisualBlock(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	cm.State = terminal.CopyModeVisualBlock
	cm.VisualStart = terminal.Position{X: cm.CursorX, Y: getAbsoluteY(cm, grid)}
	cm.VisualEnd = cm.VisualStart
}

func updateVisualEnd(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	absY := getAbsoluteY(cm, grid)

	switch cm.State {
	case terminal.CopyModeVisualChar, terminal.CopyModeVisualBlock:
		cm.VisualEnd = terminal.Position{X: cm.CursorX, Y: absY}
	case terminal.CopyModeVisualLine:
		cm.VisualEnd.Y = absY

		endY := max(cm.VisualStart.Y, cm.VisualEnd.Y)

		const startLineStartX = 0
		_, endLineEndX := getLineContentBounds(cm, grid, endY)

		if absY < cm.VisualStart.Y {
			cm.VisualEnd.X = startLineStartX
			cm.VisualStart.X = endLineEndX
		} else {
			cm.VisualStart.X = startLineStartX
			cm.VisualEnd.X = endLineEndX
		}
	}
}

func extractVisualText(cm *terminal.CopyMode, grid terminal.CopyGrid) string {
	if cm.State == terminal.CopyModeVisualBlock {
		return extractBlockText(cm, grid)
	}
	start, end := cm.VisualStart, cm.VisualEnd

	if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
		start, end = end, start
	}

	var text strings.Builder
	for y := start.Y; y <= end.Y; y++ {
		cells := selectionRowCells(grid, y)
		wraps := y < end.Y && selectionRowWraps(grid, y)
		if wraps && selectionRowPadded(grid, y) && len(cells) > 0 {
			cells = cells[:len(cells)-1]
		}

		lo, hi := 0, len(cells)-1
		if y == start.Y {
			lo = start.X
		}
		if y == end.Y {
			hi = min(hi, end.X)
		}
		if !wraps {
			hi = min(hi, lastPrintedCell(cells))
		}
		writeSelectionCells(&text, cells, lo, hi)

		if y < end.Y && !wraps {
			text.WriteByte('\n')
		}
	}

	return strings.TrimRight(text.String(), "\n")
}

func extractBlockText(cm *terminal.CopyMode, grid terminal.CopyGrid) string {
	topLeft, bottomRight := terminal.BlockCorners(cm.VisualStart, cm.VisualEnd)
	var text strings.Builder
	for y := topLeft.Y; y <= bottomRight.Y; y++ {
		cells := selectionRowCells(grid, y)
		lo, hi := terminal.BlockRowSpan(topLeft.X, bottomRight.X, func(x int) *uv.Cell {
			if x < 0 || x >= len(cells) {
				return nil
			}
			return &cells[x]
		})
		writeSelectionCells(&text, cells, lo, min(hi, lastPrintedCell(cells)))
		if y < bottomRight.Y {
			text.WriteByte('\n')
		}
	}
	return strings.TrimRight(text.String(), "\n")
}

func selectionRowCells(grid terminal.CopyGrid, y int) []uv.Cell {
	scrollbackLen := grid.ScrollbackLen()
	if y < scrollbackLen {
		return grid.ScrollbackLine(y)
	}
	screenY := y - scrollbackLen
	cells := make([]uv.Cell, grid.Text().Width())
	for x := range cells {
		if cell := grid.Text().CellAt(x, screenY); cell != nil {
			cells[x] = *cell
		} else {
			cells[x] = uv.EmptyCell
		}
	}
	return cells
}

func selectionRowWraps(grid terminal.CopyGrid, y int) bool {
	if grid.Text() == nil {
		return false
	}
	var wrapped, known bool
	if scrollbackLen := grid.ScrollbackLen(); y < scrollbackLen {
		wrapped, known = grid.Text().ScrollbackSoftWrapped(y)
	} else {
		wrapped, known = grid.Text().RowSoftWrapped(y - scrollbackLen)
	}
	return known && wrapped
}

func selectionRowPadded(grid terminal.CopyGrid, y int) bool {
	if grid.Text() == nil {
		return false
	}
	if scrollbackLen := grid.ScrollbackLen(); y < scrollbackLen {
		return grid.Text().ScrollbackPadded(y)
	}
	return grid.Text().RowPadded(y - grid.ScrollbackLen())
}

func lastPrintedCell(cells []uv.Cell) int {
	for x := len(cells) - 1; x >= 0; x-- {
		if c := cells[x].Content; c != "" && c != " " {
			return x
		}
	}
	return -1
}

func writeSelectionCells(text *strings.Builder, cells []uv.Cell, lo, hi int) {
	lo = max(lo, 0)
	hi = min(hi, len(cells)-1)
	for lo > 0 && lo <= hi && cells[lo].Width == 0 && cells[lo].Content == "" && cells[lo-1].Width > 1 {
		lo--
	}
	covered := 0
	for x := lo; x <= hi; x++ {
		c := cells[x]
		if covered > 0 {
			covered--
			if c.Content == "" {
				continue
			}
		}
		if c.Content == "" {
			text.WriteByte(' ')
			continue
		}
		text.WriteString(c.Content)
		if c.Width > 1 {
			covered = c.Width - 1
		}
	}
}

func getLineContentBounds(_ *terminal.CopyMode, grid terminal.CopyGrid, absY int) (int, int) {
	scrollbackLen := grid.ScrollbackLen()

	var cells []uv.Cell
	if absY < scrollbackLen {
		cells = grid.ScrollbackLine(absY)
	} else {
		screenY := absY - scrollbackLen
		cells = getScreenLineCells(grid.Text(), screenY)
	}

	if len(cells) == 0 {
		return 0, 0
	}

	startX := 0
	for i, cell := range cells {
		if cell.Width > 0 && cell.Content != "" && cell.Content != " " {
			startX = i
			break
		}
	}

	endX := len(cells) - 1
	for i := len(cells) - 1; i >= 0; i-- {
		if cells[i].Width > 0 && cells[i].Content != "" && cells[i].Content != " " {
			endX = i
			break
		}
	}

	if endX < startX {
		return 0, 0
	}

	return startX, endX
}

func getLineText(_ *terminal.CopyMode, grid terminal.CopyGrid, absY int) string {
	scrollbackLen := grid.ScrollbackLen()

	if absY < scrollbackLen {
		line := grid.ScrollbackLine(absY)
		if line != nil {
			return extractLineTextFromCells(line)
		}
	} else {
		screenY := absY - scrollbackLen
		return extractScreenLineText(grid.Text(), screenY)
	}

	return ""
}

func swapVisualEnds(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	cm.VisualStart, cm.VisualEnd = cm.VisualEnd, cm.VisualStart
	if cm.State == terminal.CopyModeVisualChar || cm.State == terminal.CopyModeVisualBlock {
		cm.CursorX = cm.VisualEnd.X
	}
	showAbsoluteRow(cm, grid, cm.VisualEnd.Y)
	updateVisualEnd(cm, grid)
}

func showAbsoluteRow(cm *terminal.CopyMode, grid terminal.CopyGrid, absY int) {
	scrollbackLen := grid.ScrollbackLen()
	top := scrollbackLen - cm.ScrollOffset
	switch {
	case absY < top:
		top = absY
	case absY >= top+grid.ViewHeight():
		top = absY - grid.ViewHeight() + 1
	}
	cm.ScrollOffset = max(scrollbackLen-top, 0)
	grid.SetScrollOffset(cm.ScrollOffset)
	cm.CursorY = absY - (scrollbackLen - cm.ScrollOffset)
}
