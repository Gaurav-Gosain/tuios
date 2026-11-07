package app

import (
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// tuios draws pane content into its own frame, so the outer terminal sees
// tuios's output and never the guest's. Whatever the outer terminal does with
// a link (its own hover, cmd+click on macOS, ctrl+shift+click in kitty,
// shift+click in Alacritty) works only on what tuios hands it.
//
// So the frame carries every link as OSC 8:
//
//   - A marked link keeps the guest's own URI and parameters, id= included,
//     so the outer terminal joins the same runs the guest meant and shows the
//     same target. The emulator stripped control bytes on the way in.
//   - A bare URL tuios detects is wrapped in OSC 8 too, with the address the
//     detector found and an id of tuios's own. A URL soft-wrapped across rows
//     is then one link to the outer terminal, not two halves its own regex
//     reads separately, and the edge of a pane never ends up inside it.
//
// A cell that has a marked link keeps it. A bare URL inside the text of an OSC
// 8 link is the guest's label, not the target, so it never replaces it.
//
// This is the cell loop's half. The emulator's own renderer, which the fast
// path for an unfocused pane uses, already writes the marked links. A bare URL
// in such a pane is left to the outer terminal's own detection.

// paneBareSpan is the cells of one detected URL on one viewport row, inclusive.
type paneBareSpan struct {
	X0, X1 int
	Link   uv.Link
}

// linkScan is the scratch space appendBareSpans reuses for every line of one
// frame. Link rules scan every line rather than only those holding "://", and
// without it each line cost its text twice over in allocations.
type linkScan struct {
	buf    []byte        // the line's text
	rowAt  []int         // one row's column-to-byte map
	refs   []linkCellRef // the cell of each glyph in buf
	byteAt []int         // the byte offset in buf of each glyph
}

// appendBareSpans reads rows top..bottom as one line, finds the URLs in it with
// the shared detector, and records each one's cells per row. rows must have
// maxY entries. The caller holds the window's I/O read lock.
//
// The viewport may cut the line: its first row can continue a row above the
// viewport, and its last row can wrap onto one below, as in a pane scrolled
// back to the middle of a long URL. The line is followed past both edges in
// the emulator (up to linkWrapRows rows each way), so a cut URL keeps its
// whole address. Spans are recorded only for the rows on screen.
func appendBareSpans(rows [][]paneBareSpan, window *terminal.Window, top, bottom, maxX, maxY int, rules *hints.LinkRules, scan *linkScan) [][]paneBareSpan {
	if top == 0 {
		for limit := top - linkWrapRows; top > limit && paneRowWraps(window, top-1); {
			top--
		}
	}
	if bottom == maxY-1 {
		for limit := bottom + linkWrapRows; bottom < limit && paneRowWraps(window, bottom); {
			bottom++
		}
	}
	if scan == nil {
		scan = &linkScan{}
	}
	if cap(scan.rowAt) < maxX {
		scan.rowAt = make([]int, maxX)
	}
	scan.buf, scan.refs, scan.byteAt = scan.buf[:0], scan.refs[:0], scan.byteAt[:0]
	for row := top; row <= bottom; row++ {
		base := len(scan.buf)
		scan.buf, scan.rowAt = appendPaneRowText(scan.buf, scan.rowAt, window, row, maxX)
		for col, off := range scan.rowAt {
			if off < 0 {
				continue
			}
			scan.refs = append(scan.refs, linkCellRef{X: col, Y: row})
			scan.byteAt = append(scan.byteAt, base+off)
		}
	}
	// Most lines hold no link. Telling them apart on the bytes, before the
	// line becomes a string, is what keeps a screen of ordinary output from
	// allocating with rules on.
	if !hints.MayHoldLink(scan.buf, rules) {
		return rows
	}
	line := string(scan.buf)
	refs, byteAt := scan.refs, scan.byteAt
	for _, l := range hints.Links(line, rules) {
		first, last := -1, -1
		for i, off := range byteAt {
			if off >= l.Start && off < l.End {
				if first < 0 {
					first = i
				}
				last = i
			}
		}
		if first < 0 {
			continue
		}
		url := l.Target
		link := uv.Link{URL: url, Params: "id=" + bareLinkID(window.ID, url, refs[first])}
		for i := first; i <= last; {
			y := refs[i].Y
			j := i
			for j+1 <= last && refs[j+1].Y == y {
				j++
			}
			if y >= 0 && y < maxY {
				rows[y] = append(rows[y], paneBareSpan{X0: refs[i].X, X1: refs[j].X, Link: link})
			}
			i = j + 1
		}
	}
	return rows
}

// bareLinkID names one detected URL for the outer terminal. The rows of a
// wrapped URL share it, so the outer terminal hovers and opens them as one
// link, and two copies of the same address on screen stay two links.
func bareLinkID(windowID, url string, at linkCellRef) string {
	h := fnv.New64a()
	h.Write([]byte(windowID))
	h.Write([]byte{0})
	h.Write([]byte(url))
	h.Write([]byte{0, byte(at.X), byte(at.X >> 8), byte(at.Y), byte(at.Y >> 8)})
	return "tuios-" + strconv.FormatUint(h.Sum64(), 36)
}

// bareSpanCount counts the spans recorded for viewport rows top..bottom.
func bareSpanCount(rows [][]paneBareSpan, top, bottom int) int {
	n := 0
	for y := max(top, 0); y <= bottom && y < len(rows); y++ {
		n += len(rows[y])
	}
	return n
}

// bareLinkFor returns the detected link covering column x of a row's spans.
func bareLinkFor(spans []paneBareSpan, x int) uv.Link {
	for i := range spans {
		if x >= spans[i].X0 && x <= spans[i].X1 {
			return spans[i].Link
		}
	}
	return uv.Link{}
}

// linkParamID returns the id= value of OSC 8 parameters, or "".
func linkParamID(params string) string {
	for p := range strings.SplitSeq(params, ":") {
		if v, ok := strings.CutPrefix(p, "id="); ok {
			return v
		}
	}
	return ""
}

// paneTextBuf is the cell loop's output buffer. It is a strings.Builder that
// can also be cut back, which a logical line drawn again needs (see the row
// loop in renderTerminal). A bytes.Buffer can be cut back too, but its String
// copies the whole pane once more per render.
type paneTextBuf struct{ b []byte }

func (p *paneTextBuf) Grow(n int)           { p.b = slices.Grow(p.b, n) }
func (p *paneTextBuf) Len() int             { return len(p.b) }
func (p *paneTextBuf) Truncate(n int)       { p.b = p.b[:n] }
func (p *paneTextBuf) WriteString(s string) { p.b = append(p.b, s...) }
func (p *paneTextBuf) WriteRune(r rune)     { p.b = utf8.AppendRune(p.b, r) }

// String returns the bytes as a string without a copy. The buffer is local to
// one render and never written after this, which is what makes that safe.
func (p *paneTextBuf) String() string {
	if len(p.b) == 0 {
		return ""
	}
	return unsafe.String(&p.b[0], len(p.b))
}
