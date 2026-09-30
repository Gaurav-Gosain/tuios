package app

import (
	"github.com/charmbracelet/x/ansi"
)

// hostDamage follows the renderer's output to learn which host cells it
// wrote.
//
// A sixel image is pixels in the host's cells, and a host drops them from any
// cell a character or an erase is written into. The renderer writes more cells
// than the ones that changed: a line whose two ends changed is often rewritten
// from end to end, and some mode changes repaint the whole screen. Each of
// those takes the picture away under an image cell that did not change at all.
// So instead of guessing from the frame, the passthrough reads the bytes the
// renderer actually sent and resends every rectangle one of them touched.
//
// It tracks only the cursor and the cells written, for the subset of controls
// a renderer uses. A sequence it does not know is taken to write nothing,
// which at worst leaves a picture missing until the next change, and never
// leaves one where it should not be: that is the text's job, and the text is
// always drawn.
type hostDamage struct {
	w, h     int
	x, y     int
	top, bot int
	savedX   int
	savedY   int
	lastW    int
	state    byte
	parser   *ansi.Parser
	rows     [][]uint64
	any      bool
	allDirty bool
}

func (d *hostDamage) resize(w, h int) {
	if w == d.w && h == d.h && d.rows != nil {
		return
	}
	d.w, d.h = w, h
	d.top, d.bot = 0, max(0, h-1)
	d.rows = make([][]uint64, h)
	words := (w + 63) / 64
	for i := range d.rows {
		d.rows[i] = make([]uint64, words)
	}
	d.x, d.y = min(d.x, max(0, w-1)), min(d.y, max(0, h-1))
	d.allDirty = true
}

// reset forgets the damage recorded so far; the cursor is kept.
func (d *hostDamage) reset() {
	if d.any {
		for _, r := range d.rows {
			clear(r)
		}
	}
	d.any, d.allDirty = false, false
}

func (d *hostDamage) mark(y, x0, x1 int) {
	if y < 0 || y >= d.h {
		return
	}
	x0, x1 = max(0, x0), min(d.w, x1)
	row := d.rows[y]
	for x := x0; x < x1; x++ {
		row[x>>6] |= 1 << (x & 63)
	}
	if x1 > x0 {
		d.any = true
	}
}

func (d *hostDamage) markRows(y0, y1 int) {
	for y := max(0, y0); y <= min(y1, d.h-1); y++ {
		d.mark(y, 0, d.w)
	}
}

// hit reports whether any cell of r was written.
func (d *hostDamage) hit(r sixelRect) bool {
	if d.allDirty {
		return true
	}
	if !d.any {
		return false
	}
	for y := r.y; y < r.y+r.rows() && y < d.h; y++ {
		if y < 0 {
			continue
		}
		row := d.rows[y]
		for x := max(0, r.x); x < r.x+r.cols() && x < d.w; x++ {
			if row[x>>6]&(1<<(x&63)) != 0 {
				return true
			}
		}
	}
	return false
}

func (d *hostDamage) clampCursor() {
	d.x = max(0, min(d.x, d.w))
	d.y = max(0, min(d.y, d.h-1))
}

// lineFeed moves down a row, scrolling the region when at its bottom.
func (d *hostDamage) lineFeed() {
	if d.y == d.bot {
		d.markRows(d.top, d.bot)
		return
	}
	d.y = min(d.y+1, d.h-1)
}

// feed follows one write of the renderer.
func (d *hostDamage) feed(p []byte) {
	if d.h == 0 || d.w == 0 {
		return
	}
	if d.parser == nil {
		d.parser = ansi.NewParser()
	}
	for len(p) > 0 {
		seq, width, n, state := ansi.DecodeSequence(p, d.state, d.parser)
		d.state = state
		if n <= 0 {
			break
		}
		p = p[n:]
		if width > 0 {
			if d.x >= d.w {
				// Pending wrap: the character goes to the next line.
				d.x = 0
				d.lineFeed()
			}
			d.mark(d.y, d.x, d.x+width)
			d.x += width
			d.lastW = width
			continue
		}
		if len(seq) == 1 {
			switch seq[0] {
			case '\r':
				d.x = 0
			case '\n', '\v', '\f':
				d.lineFeed()
			case '\b':
				d.x = max(0, min(d.x, d.w-1)-1)
			case '\t':
				d.x = min(d.w-1, (d.x/8+1)*8)
			}
			continue
		}
		if len(seq) < 2 || seq[0] != 0x1b {
			continue
		}
		switch {
		case seq[1] == '[':
			d.csi()
		case len(seq) == 2:
			switch seq[1] {
			case '7':
				d.savedX, d.savedY = d.x, d.y
			case '8':
				d.x, d.y = d.savedX, d.savedY
			case 'D':
				d.lineFeed()
			case 'E':
				d.x = 0
				d.lineFeed()
			case 'M':
				if d.y == d.top {
					d.markRows(d.top, d.bot)
				} else {
					d.y = max(0, d.y-1)
				}
			case 'c':
				d.allDirty = true
				d.x, d.y, d.top, d.bot = 0, 0, 0, d.h-1
			}
		}
	}
}

func (d *hostDamage) csi() {
	cmd := ansi.Cmd(d.parser.Command())
	param := func(i, def int) int {
		v, _ := d.parser.Param(i, def)
		if v <= 0 {
			return def
		}
		return v
	}
	raw := func(i int) int {
		v, _ := d.parser.Param(i, 0)
		return v
	}
	if cmd.Prefix() == '?' {
		switch cmd.Final() {
		case 'h', 'l':
			for i := range len(d.parser.Params()) {
				switch raw(i) {
				case 47, 1047, 1049:
					d.allDirty = true
				}
			}
		}
		return
	}
	if cmd.Prefix() != 0 || cmd.Intermediate() != 0 {
		return
	}
	switch cmd.Final() {
	case 'H', 'f':
		d.y, d.x = param(0, 1)-1, param(1, 1)-1
	case 'A':
		d.y -= param(0, 1)
	case 'B':
		d.y += param(0, 1)
	case 'C':
		d.x = min(d.x, d.w-1) + param(0, 1)
		d.x = min(d.x, d.w-1)
	case 'D':
		d.x = min(d.x, d.w-1) - param(0, 1)
	case 'E':
		d.x, d.y = 0, d.y+param(0, 1)
	case 'F':
		d.x, d.y = 0, d.y-param(0, 1)
	case 'G', '`':
		d.x = param(0, 1) - 1
	case 'd':
		d.y = param(0, 1) - 1
	case 'J':
		switch raw(0) {
		case 0:
			d.mark(d.y, d.x, d.w)
			d.markRows(d.y+1, d.h-1)
		case 1:
			d.markRows(0, d.y-1)
			d.mark(d.y, 0, d.x+1)
		default:
			d.allDirty = true
		}
	case 'K':
		switch raw(0) {
		case 0:
			d.mark(d.y, d.x, d.w)
		case 1:
			d.mark(d.y, 0, d.x+1)
		default:
			d.mark(d.y, 0, d.w)
		}
	case 'X':
		d.mark(d.y, d.x, d.x+param(0, 1))
	case 'b':
		n := param(0, 1) * max(1, d.lastW)
		d.mark(d.y, d.x, d.x+n)
		d.x += n
	case '@', 'P':
		d.mark(d.y, d.x, d.w)
	case 'L', 'M':
		d.markRows(d.y, d.bot)
	case 'S', 'T':
		d.markRows(d.top, d.bot)
	case 'r':
		d.top, d.bot = param(0, 1)-1, param(1, d.h)-1
		if d.top >= d.bot || d.bot >= d.h {
			d.top, d.bot = 0, d.h-1
		}
		d.x, d.y = 0, 0
	case 's':
		d.savedX, d.savedY = d.x, d.y
	case 'u':
		d.x, d.y = d.savedX, d.savedY
	}
	d.clampCursor()
}
