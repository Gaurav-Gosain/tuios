package vt

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi/kitty"
)

// Sixel image cells.
//
// A sixel image is pixels a terminal paints at the cursor. tuios cannot pass
// that through as it is: a pane is a sub-rectangle of the host screen, and the
// host would paint the picture over whatever tuios drew next to the pane, over
// popups, and past the pane's edges, with no way to take it back.
//
// So the emulator does what a sixel terminal does, in text. When a guest draws
// an image, every cell the image covers is written with a marker cell that
// names the image and the cell's row and column inside it. From then on the
// image is wherever its cells are: it scrolls into the scrollback with them,
// text written over a cell takes that cell's part of the picture away, and an
// erase clears it, all because those are things the grid already does to
// cells. Both emulator backends get this for free, because to them a marker is
// just a character with combining marks.
//
// The compositor finds the markers on the finished frame, replaces them with
// blanks, and tells the host to draw exactly the parts of each image whose
// cells survived: cropped to the pane, cut around a popup, gone when cleared.
// See internal/app/sixel_frame.go.
//
// A marker is U+10EEED, a private-use character next to kitty's placeholder
// U+10EEEE, followed by four combining marks from kitty's diacritic table:
// the cell's row, its column, and the image id in two base-297 digits. The
// id rides in marks and not in the foreground colour, as kitty's does,
// because tuios dims and re-themes colours and a mark survives both.
const sixelMarkerRune = '\U0010EEED'

// SixelMarkerLead is the UTF-8 encoding of sixelMarkerRune, for a byte
// compare on the hot path.
const SixelMarkerLead = "\xf4\x8e\xbb\xad"

// sixelDigits is the number of combining marks kitty's table has, and so the
// base the marks count in.
const sixelDigits = 297

// SixelMaxCells bounds an image's rows and columns: a marker states them in
// one mark each.
const SixelMaxCells = sixelDigits - 1

// SixelMaxID is the largest image id a marker can carry. Id 0 names no image:
// the cells are reserved and the compositor draws them blank.
const SixelMaxID = sixelDigits*sixelDigits - 1

// IsSixelMarker reports whether a cell's content is a sixel image cell. It is
// a byte compare, cheap enough for every cell of every frame.
func IsSixelMarker(content string) bool {
	return len(content) >= len(SixelMarkerLead) && content[:len(SixelMarkerLead)] == SixelMarkerLead
}

// SixelMarker is the cell content for row and column of image id.
func SixelMarker(id uint32, row, col int) string {
	var b strings.Builder
	b.Grow(4 + 4*3)
	b.WriteRune(sixelMarkerRune)
	b.WriteRune(kitty.Diacritic(max(0, min(row, SixelMaxCells))))
	b.WriteRune(kitty.Diacritic(max(0, min(col, SixelMaxCells))))
	id = min(id, SixelMaxID)
	b.WriteRune(kitty.Diacritic(int(id / sixelDigits)))
	b.WriteRune(kitty.Diacritic(int(id % sixelDigits)))
	return b.String()
}

// ParseSixelMarker reads a marker cell back. It reports false for content
// that is not a complete marker, which the compositor then draws blank.
func ParseSixelMarker(content string) (id uint32, row, col int, ok bool) {
	if !IsSixelMarker(content) {
		return 0, 0, 0, false
	}
	var digits [4]int
	n := 0
	for _, r := range content[len(SixelMarkerLead):] {
		if n == len(digits) {
			return 0, 0, 0, false
		}
		d, found := diacriticIndex()[r]
		if !found {
			return 0, 0, 0, false
		}
		digits[n] = d
		n++
	}
	if n != len(digits) {
		return 0, 0, 0, false
	}
	return uint32(digits[2]*sixelDigits + digits[3]), digits[0], digits[1], true
}

// SixelCells returns the rows and columns an image covers at the given cell
// size, bounded so a marker can state every one of them.
func SixelCells(cmd *SixelCommand, cellW, cellH int) (rows, cols int) {
	return min(cmd.RowsForHeight(cellH), SixelMaxCells), min(cmd.ColsForWidth(cellW), SixelMaxCells)
}

// StripSixelMarkers replaces every sixel marker in s with a space, for text
// that leaves the grid as text: a copy, a search, a capture.
func StripSixelMarkers(s string) string {
	if !strings.Contains(s, SixelMarkerLead) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		j := strings.Index(s[i:], SixelMarkerLead)
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+j])
		b.WriteByte(' ')
		i += j + len(SixelMarkerLead)
		// Skip the marks that belong to the marker.
		for i < len(s) {
			r, size := utf8.DecodeRuneInString(s[i:])
			if _, mark := diacriticIndex()[r]; !mark {
				break
			}
			i += size
		}
	}
	return b.String()
}

// SixelPassthroughFunc receives a guest's sixel image and the cursor it was
// drawn at, and returns the id to mark the image's cells with, or 0 when the
// image will not be shown.
type SixelPassthroughFunc func(cmd *SixelCommand, cursorX, cursorY int) uint32
