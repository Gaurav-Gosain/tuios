package snapshot

import (
	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt"
)

// HistoryPage is a run of history lines in the packed wire form, numbered as
// vt.Terminal.ScrollbackBase numbers them. A client that keeps less history
// than its source asks the source for the older lines a page at a time.
type HistoryPage struct {
	// First is the number of the first line the page holds.
	First int64 `json:"first"`
	// Base and End are the source's oldest history line number and one past
	// its newest, when the page was read. A line below Base is gone.
	Base int64 `json:"base"`
	End  int64 `json:"end"`
	// Epoch is the source's vt.Terminal.ScrollbackEpoch when the page was
	// read. Lines read in another epoch have other numbers.
	Epoch uint64 `json:"epoch,omitempty"`
	// Packed holds the lines, oldest first, as State.PackedScrollback holds
	// them, with their style table, soft-wrap flags and padding flags.
	Packed []byte       `json:"packed,omitempty"`
	Styles []StyleState `json:"styles,omitempty"`
	Wraps  []byte       `json:"wraps,omitempty"`
	Pads   []byte       `json:"pads,omitempty"`
}

// Len is how many lines the page holds.
func (p *HistoryPage) Len() int { return packedRowCount(p.Packed) }

// CaptureHistoryPage reads up to limit history lines of t that come before
// line number before, the newest of them last. It copies the lines while the
// caller holds t's lock, and returns finish, which packs them without reading
// t. The page holds no lines until finish has run. A terminal that does not
// number its history returns a nil page. Lines before the oldest the terminal
// holds are gone, and a page for them is empty. The caller bounds limit: the
// page is as long as the history allows.
func CaptureHistoryPage(t vt.Terminal, before int64, limit int) (page *HistoryPage, finish func()) {
	base, ok := t.ScrollbackBase()
	if !ok {
		return nil, func() {}
	}
	n := t.ScrollbackLen()
	// Clamped first, so a number from the wire cannot overflow the
	// subtraction.
	before = min(max(before, base), base+int64(n))
	end := before - base
	from := max(end-int64(max(limit, 0)), 0)
	page = &HistoryPage{First: base + from, Base: base, End: base + int64(n), Epoch: t.ScrollbackEpoch()}
	history := t.CopyScrollback(int(from), int(end))
	return page, func() {
		colors := colorWireCache{}
		p := newRowPacker()
		k := history.Len()
		b := newPackedRows(k * 32)
		flags := make([]rowFlags, k)
		var row []CellState
		history.Rows(0, k, func(i int, line uv.Line) bool {
			flags[i] = newRowFlags(history.Wrapped(i), history.Padded(i))
			if cap(row) < len(line) {
				row = make([]CellState, len(line))
			}
			r := row[:len(line)]
			used := usedCells(line)
			for x := range used {
				r[x] = colors.cellState(&line[x])
			}
			for x := used; x < len(r); x++ {
				r[x] = blankCellState
			}
			b.add(p, r)
			return true
		})
		page.Packed = b.blob()
		page.Wraps, page.Pads = rowFlagBits(flags)
		page.Styles = p.styles
	}
}

// Lines calls fn with each line of the page, oldest first, and its number
// and flags. Colours resolve against t's palette. Each line is new, so fn
// may keep it.
func (p *HistoryPage) Lines(t vt.Terminal, fn func(number int64, line uv.Line, wrapped, padded bool)) error {
	styles := resolveStyles(t, p.Styles)
	n := p.Len()
	wraps, pads := wrapFlags(p.Wraps, n), wrapFlags(p.Pads, n)
	return walkPacked(p.Packed, len(styles), func(y int, row []packedCell) error {
		fn(p.First+int64(y), packedLine(row, styles, make(uv.Line, len(row))), wraps[y], pads[y])
		return nil
	})
}
