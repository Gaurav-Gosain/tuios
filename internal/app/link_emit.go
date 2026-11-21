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

type paneBareSpan struct {
	X0, X1 int
	Link   uv.Link
}

type linkScan struct {
	buf    []byte
	rowAt  []int
	refs   []linkCellRef
	byteAt []int
}

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

func bareLinkID(windowID, url string, at linkCellRef) string {
	h := fnv.New64a()
	h.Write([]byte(windowID))
	h.Write([]byte{0})
	h.Write([]byte(url))
	h.Write([]byte{0, byte(at.X), byte(at.X >> 8), byte(at.Y), byte(at.Y >> 8)})
	return "tuios-" + strconv.FormatUint(h.Sum64(), 36)
}

func bareSpanCount(rows [][]paneBareSpan, top, bottom int) int {
	n := 0
	for y := max(top, 0); y <= bottom && y < len(rows); y++ {
		n += len(rows[y])
	}
	return n
}

func bareLinkFor(spans []paneBareSpan, x int) uv.Link {
	for i := range spans {
		if x >= spans[i].X0 && x <= spans[i].X1 {
			return spans[i].Link
		}
	}
	return uv.Link{}
}

func linkParamID(params string) string {
	for p := range strings.SplitSeq(params, ":") {
		if v, ok := strings.CutPrefix(p, "id="); ok {
			return v
		}
	}
	return ""
}

type paneTextBuf struct{ b []byte }

func (p *paneTextBuf) Grow(n int)           { p.b = slices.Grow(p.b, n) }
func (p *paneTextBuf) Len() int             { return len(p.b) }
func (p *paneTextBuf) Truncate(n int)       { p.b = p.b[:n] }
func (p *paneTextBuf) WriteString(s string) { p.b = append(p.b, s...) }
func (p *paneTextBuf) WriteRune(r rune)     { p.b = utf8.AppendRune(p.b, r) }

func (p *paneTextBuf) String() string {
	if len(p.b) == 0 {
		return ""
	}
	return unsafe.String(&p.b[0], len(p.b))
}
