package tfx

// Anchor is one of the nine compass points a block of text can be pinned to
// inside the canvas.
type Anchor int

// The nine anchors.
const (
	AnchorSW Anchor = iota
	AnchorS
	AnchorSE
	AnchorW
	AnchorC
	AnchorE
	AnchorNW
	AnchorN
	AnchorNE
)

// Canvas is the drawable rectangle, in 1-based coordinates with the origin at
// the bottom left. It also tracks the smaller rectangle the input text
// actually occupies, which is what gradients are painted across.
type Canvas struct {
	Top    int
	Right  int
	Bottom int
	Left   int

	CenterRow    int
	CenterColumn int
	Center       Coord

	Width  int
	Height int

	TextTop    int
	TextRight  int
	TextBottom int
	TextLeft   int

	TextWidth  int
	TextHeight int

	TextCenterRow    int
	TextCenterColumn int
	TextCenter       Coord
}

// NewCanvas builds a canvas of the given size, anchored at (1,1).
func NewCanvas(width, height int) *Canvas {
	c := &Canvas{Top: height, Right: width, Bottom: 1, Left: 1, Width: width, Height: height}
	c.CenterRow = max(floorDiv(height, 2), 1)
	if height%2 != 0 && height > 1 {
		c.CenterRow++
	}
	c.CenterColumn = max(floorDiv(width, 2), 1)
	if width%2 != 0 && width > 1 {
		c.CenterColumn++
	}
	c.Center = C(c.CenterColumn, c.CenterRow)
	return c
}

// CoordIsInCanvas reports whether a coordinate falls inside the canvas.
func (c *Canvas) CoordIsInCanvas(coord Coord) bool {
	return c.Left <= coord.Column && coord.Column <= c.Right &&
		c.Bottom <= coord.Row && coord.Row <= c.Top
}

// CoordIsInText reports whether a coordinate falls inside the text block.
func (c *Canvas) CoordIsInText(coord Coord) bool {
	return c.TextLeft <= coord.Column && coord.Column <= c.TextRight &&
		c.TextBottom <= coord.Row && coord.Row <= c.TextTop
}

// RandomColumn picks a column, either from the whole canvas or from the text
// block only.
func (c *Canvas) RandomColumn(rng *Rng, withinText bool) int {
	if withinText {
		return rng.IntBetween(c.TextLeft, c.TextRight)
	}
	return rng.IntBetween(c.Left, c.Right)
}

// RandomRow picks a row, either from the whole canvas or from the text block
// only.
func (c *Canvas) RandomRow(rng *Rng, withinText bool) int {
	if withinText {
		return rng.IntBetween(c.TextBottom, c.TextTop)
	}
	return rng.IntBetween(c.Bottom, c.Top)
}

// RandomCoord picks a coordinate. With outsideScope set it picks one cell past
// a randomly chosen edge, which is where effects launch characters from.
func (c *Canvas) RandomCoord(rng *Rng, outsideScope, withinText bool) Coord {
	if outsideScope {
		above := C(c.RandomColumn(rng, false), c.Top+1)
		below := C(c.RandomColumn(rng, false), c.Bottom-1)
		left := C(c.Left-1, c.RandomRow(rng, false))
		right := C(c.Right+1, c.RandomRow(rng, false))
		return *Choice(rng, []Coord{above, below, left, right})
	}
	return C(c.RandomColumn(rng, withinText), c.RandomRow(rng, withinText))
}

// anchorText shifts characters to sit at the requested anchor, drops the ones
// that then fall outside the canvas, and records the text extents. It returns
// the surviving characters.
func (c *Canvas) anchorText(characters []*Character, anchor Anchor) []*Character {
	if len(characters) == 0 {
		return nil
	}
	inputWidth, inputHeight := 0, 0
	for _, ch := range characters {
		inputWidth = max(inputWidth, ch.InputCoord.Column)
		inputHeight = max(inputHeight, ch.InputCoord.Row)
	}

	columnDelta, rowDelta := 0, 0
	if inputWidth != c.Width {
		switch anchor {
		case AnchorS, AnchorN, AnchorC:
			columnDelta = c.CenterColumn - floorDiv(inputWidth, 2)
		case AnchorSE, AnchorE, AnchorNE:
			columnDelta = c.Right - inputWidth
		case AnchorSW, AnchorW, AnchorNW:
			columnDelta = c.Left - 1
		}
	}
	if inputHeight != c.Height {
		switch anchor {
		case AnchorW, AnchorE, AnchorC:
			rowDelta = c.CenterRow - floorDiv(inputHeight, 2)
		case AnchorNW, AnchorN, AnchorNE:
			rowDelta = c.Top - inputHeight
		case AnchorSW, AnchorS, AnchorSE:
			rowDelta = c.Bottom - 1
		}
	}

	kept := make([]*Character, 0, len(characters))
	for _, ch := range characters {
		anchored := C(ch.InputCoord.Column+columnDelta, ch.InputCoord.Row+rowDelta)
		ch.InputCoord = anchored
		ch.Motion.SetCoordinate(anchored)
		if c.CoordIsInCanvas(anchored) {
			kept = append(kept, ch)
		}
	}
	if len(kept) == 0 {
		return nil
	}

	c.TextLeft, c.TextRight = kept[0].InputCoord.Column, kept[0].InputCoord.Column
	c.TextBottom, c.TextTop = kept[0].InputCoord.Row, kept[0].InputCoord.Row
	for _, ch := range kept {
		c.TextLeft = min(c.TextLeft, ch.InputCoord.Column)
		c.TextRight = max(c.TextRight, ch.InputCoord.Column)
		c.TextBottom = min(c.TextBottom, ch.InputCoord.Row)
		c.TextTop = max(c.TextTop, ch.InputCoord.Row)
	}
	c.TextWidth = max(c.TextRight-c.TextLeft+1, 1)
	c.TextHeight = max(c.TextTop-c.TextBottom+1, 1)
	c.TextCenterRow = c.TextBottom + floorDiv(c.TextTop-c.TextBottom, 2)
	c.TextCenterColumn = c.TextLeft + floorDiv(c.TextRight-c.TextLeft, 2)
	c.TextCenter = C(c.TextCenterColumn, c.TextCenterRow)
	return kept
}
