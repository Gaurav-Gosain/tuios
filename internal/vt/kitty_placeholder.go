package vt

import (
	"image/color"
	"strings"
	"sync"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// Kitty's Unicode placeholder protocol, and the one thing a multiplexer has to
// do about it.
//
// An application that wants an image to move with the text does not place the
// image itself. It transmits the image, creates a virtual placement saying the
// image occupies a box of c columns by r rows, and then prints cells of
// U+10EEEE where the image should appear. The terminal draws the part of the
// image that belongs to each of those cells. Because the position lives in the
// text grid, the image scrolls, reflows and clips exactly as the text does,
// which is why kitty's own documentation points multiplexers at this protocol.
//
// Each placeholder cell carries the image id in its foreground colour: the low
// 24 bits as the red, green and blue channels, and the top 8 bits, when the id
// needs them, in a third combining mark. The first mark is the image row and
// the second is the column; a cell with no marks continues the run to its left.
//
// tuios has to rewrite that id. The image the host holds is not the image the
// guest transmitted: guests pick ids independently and two panes would collide,
// so every image is re-registered under an id tuios allocates. The cells still
// name the guest's id, and a cell naming an id the host has never heard of
// draws nothing. Translating the colour is the whole of the work, and it is why
// this lives in the emulator rather than in the passthrough: the cells are text
// and the emulator is what owns the text.
const kittyPlaceholderChar = kitty.Placeholder

// KittyPlaceholderMode says what the emulator does with placeholder cells.
type KittyPlaceholderMode int

const (
	// KittyPlaceholdersDrop discards placeholder cells, which is what a host
	// that cannot draw them needs: kept, they render as missing-glyph boxes
	// where the picture should be, which is worse than the blank space the
	// application left room for. This is the default, so a caller that never
	// asks is never surprised.
	KittyPlaceholdersDrop KittyPlaceholderMode = iota
	// KittyPlaceholdersKeep stores them so they reach the host and it draws
	// the image.
	KittyPlaceholdersKeep
)

// KittyImageIDTranslator turns a guest's image id into the id the host knows
// that image by. It reports false when the image has no host id yet, in which
// case the cell is left as the guest wrote it.
type KittyImageIDTranslator func(guestID uint32) (hostID uint32, ok bool)

// IsKittyPlaceholder reports whether a cell's content is a placeholder cell.
//
// This is asked of every cell the emulator prints and of every style run the
// dim blends, so it leaves the hot path on a byte compare: U+10EEEE is F4 8E BB
// AE in UTF-8, and no ordinary character starts with F4.
func IsKittyPlaceholder(content string) bool {
	if len(content) < 4 || content[0] != 0xF4 {
		return false
	}
	for _, r := range content {
		return r == kittyPlaceholderChar
	}
	return false
}

// diacriticIndex is the reverse of kitty.Diacritic: the row or column a
// combining mark stands for. Built once, from the table x/ansi already carries,
// so the 297 code points are not copied into this repo to drift from it.
var diacriticIndex = sync.OnceValue(func() map[rune]int {
	m := make(map[rune]int, 297)
	first := kitty.Diacritic(0)
	for i := 0; ; i++ {
		r := kitty.Diacritic(i)
		if i > 0 && r == first {
			// Diacritic answers out-of-range indices with the first mark, and
			// the first mark appears once in the table, so this is the end.
			break
		}
		m[r] = i
	}
	return m
})

// KittyPlaceholderImageID returns the image id a placeholder cell names. Tests
// outside this package read cells through it.
func KittyPlaceholderImageID(content string, fg color.Color) (uint32, bool) {
	return kittyPlaceholderID(content, fg)
}

// kittyPlaceholderID reads the image id a placeholder cell names: the low 24
// bits from the foreground colour, and the top 8 from a third combining mark
// when the cell carries one. A 256-colour foreground gives the low bits by its
// index, which is how kitty reads it.
//
// It reports false for a foreground that cannot state an id: the default
// colour, or one that is fully transparent.
func kittyPlaceholderID(content string, fg color.Color) (uint32, bool) {
	var id uint32
	switch c := fg.(type) {
	case nil:
		return 0, false
	case ansi.IndexedColor:
		id = uint32(c)
	case ansi.BasicColor:
		id = uint32(c)
	default:
		r, g, b, a := fg.RGBA()
		if a == 0 {
			return 0, false
		}
		id = uint32(r>>8)<<16 | uint32(g>>8)<<8 | uint32(b>>8)
	}
	if high, ok := kittyPlaceholderHighByte(content); ok {
		id |= uint32(high) << 24
	}
	return id, true
}

// kittyPlaceholderHighByte returns the top 8 bits of the image id, which ride
// in the cell's third combining mark when the id is too large for a colour.
func kittyPlaceholderHighByte(content string) (int, bool) {
	marks := 0
	for i, r := range content {
		if i == 0 {
			continue
		}
		marks++
		if marks == 3 {
			idx, ok := diacriticIndex()[r]
			return idx, ok
		}
	}
	return 0, false
}

// kittyPlaceholderFg is the foreground a cell must carry to name id, for ids
// that fit in the 24 bits a colour has. tuios allocates host ids from one
// upward, so this is every id it hands out.
func kittyPlaceholderFg(id uint32) color.Color {
	id &= 0xffffff
	// An id that fits in 256 colours is written as one. tuios draws to the
	// host in the colour profile the host declared, and a host without
	// COLORTERM=truecolor gets true colours rounded to the nearest of 256:
	// the host id 2 became index 22, which names an image that is not there
	// (issue 292). An indexed colour survives that rounding unchanged, and
	// kitty reads its index as the id.
	if id < 256 {
		return ansi.IndexedColor(id)
	}
	return color.RGBA{
		R: uint8(id >> 16),
		G: uint8(id >> 8),
		B: uint8(id),
		A: 0xff,
	}
}

// kittyPlaceholderColour is a placement id carried in a colour, which is how
// the underline colour names the placement: the index for a 256-colour value,
// the 24 bits otherwise, and 0 for none.
func kittyPlaceholderColour(c color.Color) uint32 {
	switch v := c.(type) {
	case nil:
		return 0
	case ansi.IndexedColor:
		return uint32(v)
	case ansi.BasicColor:
		return uint32(v)
	}
	r, g, b, _ := c.RGBA()
	return uint32(r>>8)<<16 | uint32(g>>8)<<8 | uint32(b>>8)
}

// rewriteKittyPlaceholder makes a placeholder cell ready for the host: it names
// the image by the id the host knows, and it states its own row and column.
// left is the cell to its left as already stored, or nil at the start of a row.
//
// The id is rewritten in both places it lives. The foreground carries the low
// 24 bits and the third mark carries the high 8. Keeping the guest's third
// mark under the host's colour names an image that does not exist: kitten
// icat writes that mark on every cell, so its images drew nothing.
//
// Ids below 256, the foreground and the placement id in the underline colour,
// are written as 256-colour indices. tuios draws to the host in the host's
// colour profile, and a 256-colour host rounds a true colour to a nearby index,
// which is another id (issue 292). An index survives the rounding.
//
// Whether left belongs to the same image is decided on the ids, after
// translation, because left is stored translated and this cell is not yet.
//
// A cell with no marks takes the high byte of its id from the cell to its left
// too, and that cell no longer holds the guest's. memo is the last cell this
// rewrote, at (x, y), and supplies it when it is the cell to the left.
func rewriteKittyPlaceholder(cell, left *uv.Cell, tr KittyImageIDTranslator, memo *kittyPlaceholderMemo, x, y int) {
	guestID, ok := kittyPlaceholderID(cell.Content, cell.Style.Fg)
	if !ok {
		return
	}
	if _, hasHigh := kittyPlaceholderHighByte(cell.Content); !hasHigh && memo != nil &&
		memo.x == x-1 && memo.y == y && left != nil && IsKittyPlaceholder(left.Content) &&
		memo.guest&0xffffff == guestID {
		if leftID, ok := kittyPlaceholderID(left.Content, left.Style.Fg); ok && leftID == memo.host {
			guestID = memo.guest
		}
	}
	id := guestID
	if tr != nil {
		if hostID, ok := tr(guestID); ok {
			id = hostID
		}
	}
	placement := kittyPlaceholderColour(cell.Style.UnderlineColor)
	if cell.Style.UnderlineColor != nil && placement < 256 {
		cell.Style.UnderlineColor = ansi.IndexedColor(placement)
	}

	leftContent, sameImage := "", false
	if left != nil && IsKittyPlaceholder(left.Content) {
		leftContent = left.Content
		leftID, ok := kittyPlaceholderID(left.Content, left.Style.Fg)
		sameImage = ok && leftID == id && kittyPlaceholderColour(left.Style.UnderlineColor) == placement
	}

	row, col, ok := kittyPlaceholderNext(cell.Content, leftContent, sameImage)
	if !ok {
		// Nothing says where this cell is, so it keeps what the guest wrote
		// but for the high byte, which is now the host's to state and cannot
		// be stated without a row and column.
		r, c, hasRow, hasCol := kittyPlaceholderRowCol(cell.Content)
		out := string(kittyPlaceholderChar)
		if hasRow {
			out += string(kitty.Diacritic(r))
		}
		if hasCol {
			out += string(kitty.Diacritic(c))
		}
		cell.Content = out
	} else if row < 297 && col < 297 {
		out := string(kittyPlaceholderChar) + string(kitty.Diacritic(row)) + string(kitty.Diacritic(col))
		if high := id >> 24; high != 0 {
			out += string(kitty.Diacritic(int(high)))
		}
		cell.Content = out
	}
	cell.Style.Fg = kittyPlaceholderFg(id)
	if memo != nil {
		*memo = kittyPlaceholderMemo{x: x, y: y, guest: guestID, host: id}
	}
}

// kittyPlaceholderMemo is the last placeholder cell rewritten: where it is,
// and the guest and host ids it names.
type kittyPlaceholderMemo struct {
	x, y        int
	guest, host uint32
}

// Making a placeholder cell stand on its own.
//
// An application writes the row diacritic on the first cell of each row and
// nothing on the rest, and the terminal works the others out by looking left:
// a cell with no marks is the cell to its left, one column on. That is fine
// until something takes the left of the row away, and a multiplexer takes the
// left of rows away constantly. A pane dragged off the left edge of the screen
// is clipped there, and a window drawn over the left half of an image replaces
// those cells with its own. Either way the leftmost surviving cell has nothing
// to inherit from, and kitty's specification says so outright: the rules "will
// not work for horizontal scrolling and overlapping images", and a terminal
// may guess but does not have to.
//
// So the marks are filled in here, where the row is still whole. Every cell is
// given its own row and column, which is what the cell to its left would have
// told it, and then no cell needs a neighbour and any of them can be clipped
// away without taking the rest with it.

// kittyPlaceholderRowCol reads the row and column a cell states for itself.
func kittyPlaceholderRowCol(content string) (row, col int, hasRow, hasCol bool) {
	idx := diacriticIndex()
	n := 0
	for i, r := range content {
		if i == 0 {
			continue
		}
		v, ok := idx[r]
		if !ok {
			continue
		}
		switch n {
		case 0:
			row, hasRow = v, true
		case 1:
			col, hasCol = v, true
		}
		n++
		if n >= 2 {
			break
		}
	}
	return row, col, hasRow, hasCol
}

// kittyPlaceholderNext works out the row and column of a cell from the cell to
// its left, which is the inference the terminal would do if the row reached it
// whole.
//
// left is the content of the cell immediately to the left and leftFg its
// colour; they matter only when this cell states nothing itself. sameImage says
// whether that cell belongs to the same image, which the colours decide.
func kittyPlaceholderNext(content, left string, sameImage bool) (row, col int, ok bool) {
	r, c, hasRow, hasCol := kittyPlaceholderRowCol(content)
	switch {
	case hasRow && hasCol:
		return r, c, true
	case !sameImage || !IsKittyPlaceholder(left):
		if hasRow {
			// A row with no column and nothing to its left starts at zero,
			// which is what an application writes for the first cell.
			return r, 0, true
		}
		return 0, 0, false
	}
	lr, lc, lHasRow, lHasCol := kittyPlaceholderRowCol(left)
	if !lHasRow || !lHasCol {
		return 0, 0, false
	}
	_ = lHasRow
	if hasRow {
		if r != lr {
			// A new row starting beside another one begins at column zero.
			return r, 0, true
		}
		return r, lc + 1, true
	}
	return lr, lc + 1, true
}

// StripKittyPlaceholders returns s with each placeholder cell, U+10EEEE and
// the row, column and id marks after it, written as one space. A placeholder is
// a piece of a picture, and text that leaves the grid (a capture) has no use
// for it. s is returned as it is when it holds none.
func StripKittyPlaceholders(s string) string {
	const placeholder = "\U0010EEEE"
	if !strings.Contains(s, placeholder) {
		return s
	}
	idx := diacriticIndex()
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		i := strings.Index(s, placeholder)
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		b.WriteByte(' ')
		s = s[i+len(placeholder):]
		for len(s) > 0 {
			r, n := utf8.DecodeRuneInString(s)
			if _, mark := idx[r]; !mark {
				break
			}
			s = s[n:]
		}
	}
	return b.String()
}
