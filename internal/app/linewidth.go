package app

import "github.com/charmbracelet/x/ansi"

// lineWidth returns the display width of one rendered line, in cells.
//
// It exists because ansi.StringWidth was the entire cost of clipWindowContent:
// a CPU profile of the compositor's per-window clip at 207x55 attributed 100%
// of the time to it, via grapheme cluster segmentation, for roughly 175us per
// window per frame. That measurement runs over every line of every redrawn
// window, so it is squarely on the per-frame path.
//
// The overwhelmingly common rendered line is ASCII text interleaved with SGR
// escapes, where each printable byte occupies exactly one cell and no grapheme
// segmentation is required to know that. This walks bytes for that case and
// defers to ansi.StringWidth, unchanged, the moment it sees anything it is not
// certain about: any non-ASCII byte, or any escape that is not a plain CSI or
// OSC sequence. Deferring re-measures the whole line rather than trying to
// stitch a partial count onto a grapheme-aware count, because a preceding byte
// can combine with a following one, so a partial count is not a safe prefix.
//
// The fallback makes non-ASCII exactly as correct as before, and the fuzz test
// in linewidth_test.go pins the fast path to ansi.StringWidth's answer.
func lineWidth(line string) int {
	w := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c >= 0x80:
			// Multi-byte rune: may be wide, may combine with its neighbours.
			return ansi.StringWidth(line)

		case c == 0x1b:
			// Escape sequence. Only CSI is handled here, because SGR is the
			// only escape the cell renderer emits in bulk and it is the only
			// one worth a second implementation. String-terminated sequences
			// (OSC, DCS, SOS, PM, APC) were tried and withdrawn: a fuzz run
			// kept finding disagreements over how the reference parser ends
			// them (a bare ESC in the body, a C1 ST byte), and a line carrying
			// one is rare enough that measuring it the slow way costs nothing.
			if i+1 >= len(line) {
				return ansi.StringWidth(line)
			}
			switch line[i+1] {
			case '[':
				// CSI, accepted only in its strictly well-formed shape: zero or
				// more parameter bytes (0x30..0x3F) followed by a final byte
				// (0x40..0x7E). Sequences carrying intermediate bytes, and
				// malformed ones generally, are handed to ansi.StringWidth
				// rather than guessed at: a fuzz run found that "\x1b[ 0A" (an
				// intermediate space) is not consumed as a whole sequence by
				// the reference parser, and treating it as one under-reported
				// the width by a cell.
				j := i + 2
				for j < len(line) && line[j] >= 0x30 && line[j] <= 0x3f {
					j++
				}
				if j >= len(line) || line[j] < 0x40 || line[j] > 0x7e {
					return ansi.StringWidth(line)
				}
				i = j
			default:
				return ansi.StringWidth(line)
			}

		case c < 0x20 || c == 0x7f:
			// C0 control or DEL: occupies no cell.

		default:
			w++
		}
	}
	return w
}

// framesWidth returns the width of the widest line, which is the width of the
// window as a whole. Measuring only the first line under-reports it whenever
// the top row is blank, which is what discarded whole panes for full-screen
// applications with an empty first row.
func framesWidth(lines []string) int {
	widest := 0
	for _, line := range lines {
		if w := lineWidth(line); w > widest {
			widest = w
		}
	}
	return widest
}
