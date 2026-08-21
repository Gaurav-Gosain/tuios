//go:build ghostty

package vt

import (
	"bytes"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
)

// Snapshot restore. The pure emulator pokes restored state straight into its
// structs; libghostty accepts only a byte stream. The Restore*/SetCell
// family therefore buffers everything and the first operation that needs
// terminal state flushes the buffer as one synthesized stream, in a fixed
// order that does not depend on the order ApplyTerminalState called the
// primitives. The synthesis starts from a hard reset, which is exactly the
// situation the wire protocol restores into: a freshly built emulator.
type ghosttyRestore struct {
	scrollback []uv.Line
	// grids[0] is the main screen, grids[1] the alternate.
	grids            [2]map[[2]int]*uv.Cell
	modes            map[int]bool
	charsets         [4]byte
	gl, gr           int
	hasCharsets      bool
	scrollRegion     uv.Rectangle
	hasScrollRegion  bool
	altScreen        bool
	hasAltScreen     bool
	cursorX, cursorY int
	hasCursor        bool
	pen              uv.Style
	penLink          uv.Link
	hasPen           bool
	kittyKbdStack    []int
}

func (t *GhosttyTerminal) pendingRestore() *ghosttyRestore {
	if t.restore == nil {
		t.restore = &ghosttyRestore{}
	}
	return t.restore
}

// setCell buffers one restored cell. The target screen honors a pending
// alt-screen restore, since ApplyTerminalState switches screens before it
// writes cells.
func (r *ghosttyRestore) setCell(activeNow, x, y int, c *uv.Cell) {
	idx := activeNow
	if r.hasAltScreen {
		idx = 0
		if r.altScreen {
			idx = 1
		}
	}
	if r.grids[idx] == nil {
		r.grids[idx] = make(map[[2]int]*uv.Cell)
	}
	var copied *uv.Cell
	if c != nil {
		cc := *c
		copied = &cc
	}
	r.grids[idx][[2]int{x, y}] = copied
}

// flushRestoreLocked synthesizes and applies the pending restore. Call with
// mu held.
func (t *GhosttyTerminal) flushRestoreLocked() {
	r := t.restore
	if r == nil {
		return
	}
	t.restore = nil

	var seq bytes.Buffer

	// Hard reset gives the synthesis a known ground state.
	seq.WriteString("\x1bc")

	// Scrollback replays as printed lines pushed off the top.
	if len(r.scrollback) > 0 {
		for _, line := range r.scrollback {
			appendStyledLine(&seq, line)
			seq.WriteString("\x1b[0m\r\n")
		}
		// The last rows are still on screen; scroll them into history.
		rows := min(len(r.scrollback), t.height-1)
		fmt.Fprintf(&seq, "\x1b[%d;1H", t.height)
		for range rows {
			seq.WriteByte('\n')
		}
		seq.WriteString("\x1b[2J\x1b[H")
	}

	// Main screen cells.
	appendGridPaint(&seq, r.grids[0], t.width, t.height)

	// The alternate screen switches on before its cells paint, so region,
	// pen and cursor below land on the screen the snapshot took them from.
	altActive := r.hasAltScreen && r.altScreen
	if altActive {
		seq.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
		appendGridPaint(&seq, r.grids[1], t.width, t.height)
	}

	// Charsets.
	if r.hasCharsets {
		inters := [4]byte{'(', ')', '*', '+'}
		for i, id := range r.charsets {
			switch id {
			case 'A', '0':
			default:
				id = 'B'
			}
			seq.WriteByte(0x1b)
			seq.WriteByte(inters[i])
			seq.WriteByte(id)
		}
		switch r.gl {
		case 1:
			seq.WriteByte(0x0e)
		case 2:
			seq.WriteString("\x1bn")
		case 3:
			seq.WriteString("\x1bo")
		default:
			seq.WriteByte(0x0f)
		}
		switch r.gr {
		case 1:
			seq.WriteString("\x1b~")
		case 2:
			seq.WriteString("\x1b}")
		case 3:
			seq.WriteString("\x1b|")
		}
	}

	// Kitty keyboard: the library only needs the effective flags for its
	// query answers; the full stack lives in the shadow.
	if len(r.kittyKbdStack) > 0 {
		top := r.kittyKbdStack[len(r.kittyKbdStack)-1]
		fmt.Fprintf(&seq, "\x1b[=%d;1u", top)
	}

	// Modes. Origin mode last: enabling it homes the cursor, and the
	// cursor restore below compensates for it.
	decom := false
	if r.modes != nil {
		for _, m := range ghosttyModeNumbers {
			v, ok := r.modes[m.num]
			if !ok {
				continue
			}
			switch m.num {
			case 47, 1047, 1049:
				// Screen selection already synthesized.
				continue
			case 6:
				decom = v
				continue
			}
			ch := byte('l')
			if v {
				ch = 'h'
			}
			fmt.Fprintf(&seq, "\x1b[?%d%c", m.num, ch)
		}
	}

	// Scroll region. DECSTBM homes the cursor; restore order puts the
	// cursor after it.
	regionTop := 0
	if r.hasScrollRegion {
		reg := r.scrollRegion.Intersect(uv.Rect(0, 0, t.width, t.height))
		if !reg.Empty() && (reg.Min.Y > 0 || reg.Max.Y < t.height) {
			fmt.Fprintf(&seq, "\x1b[%d;%dr", reg.Min.Y+1, reg.Max.Y)
			regionTop = reg.Min.Y
		}
	}

	if decom {
		seq.WriteString("\x1b[?6h")
	}

	// Pen.
	if r.hasPen {
		seq.WriteString("\x1b[0m")
		seq.WriteString(penStyleSequence(&r.pen))
	}

	// Cursor. With origin mode on, addressing is region-relative.
	if r.hasCursor {
		y := r.cursorY
		if decom {
			y -= regionTop
		}
		if y < 0 {
			y = 0
		}
		fmt.Fprintf(&seq, "\x1b[%d;%dH", y+1, r.cursorX+1)
	}

	// Shadow state follows the synthesized stream, which bypassed the
	// scanner deliberately.
	t.charsetIDs = defaultCharsetIDs
	t.savedCharsets = defaultCharsetIDs
	t.gl, t.gr = 0, 0
	if r.hasCharsets {
		for i, id := range r.charsets {
			switch id {
			case 'A', '0':
				t.charsetIDs[i] = id
			default:
				t.charsetIDs[i] = 'B'
			}
		}
		if r.gl >= 0 && r.gl < 4 {
			t.gl = r.gl
		}
		if r.gr >= 0 && r.gr < 4 {
			t.gr = r.gr
		}
	}
	t.scrollRegion = uv.Rect(0, 0, t.width, t.height)
	if r.hasScrollRegion {
		t.scrollRegion = r.scrollRegion.Intersect(uv.Rect(0, 0, t.width, t.height))
	}
	t.kittyKbd.Reset()
	if len(r.kittyKbdStack) > 0 {
		t.kittyKbd.stack = append([]int(nil), r.kittyKbdStack...)
	}

	t.term.VTWrite(seq.Bytes())
	t.gridStale = true
	t.scrollGeneration++
	t.markAllDirtyLocked()
	t.refreshCachesLocked()
}

// appendGridPaint paints buffered cells row by row with minimal style churn.
func appendGridPaint(seq *bytes.Buffer, grid map[[2]int]*uv.Cell, width, height int) {
	if len(grid) == 0 {
		return
	}
	for y := 0; y < height; y++ {
		rowHas := false
		for x := 0; x < width; x++ {
			if _, ok := grid[[2]int{x, y}]; ok {
				rowHas = true
				break
			}
		}
		if !rowHas {
			continue
		}
		fmt.Fprintf(seq, "\x1b[%d;1H", y+1)
		line := make(uv.Line, width)
		for x := 0; x < width; x++ {
			if c, ok := grid[[2]int{x, y}]; ok && c != nil {
				line[x] = *c
			} else {
				line[x] = uv.Cell{Content: " ", Width: 1}
			}
		}
		appendStyledLine(seq, line)
		seq.WriteString("\x1b[0m")
	}
}

// appendStyledLine emits one line's cells with SGR changes only at style
// boundaries. Zero-width cells (wide-cell tails) emit nothing; the leading
// cell advanced the cursor for them.
func appendStyledLine(seq *bytes.Buffer, line uv.Line) {
	var cur uv.Style
	curSet := false
	link := ""
	for x := 0; x < len(line); x++ {
		c := line[x]
		if c.Width == 0 && c.Content == "" {
			continue
		}
		if !curSet || !c.Style.Equal(&cur) {
			seq.WriteString("\x1b[0m")
			seq.WriteString(penStyleSequence(&c.Style))
			cur = c.Style
			curSet = true
		}
		if c.Link.URL != link {
			if c.Link.URL != "" {
				seq.WriteString("\x1b]8;;" + c.Link.URL + "\x1b\\")
			} else {
				seq.WriteString("\x1b]8;;\x1b\\")
			}
			link = c.Link.URL
		}
		if c.Content == "" {
			seq.WriteByte(' ')
		} else {
			seq.WriteString(c.Content)
		}
	}
	if link != "" {
		seq.WriteString("\x1b]8;;\x1b\\")
	}
}
