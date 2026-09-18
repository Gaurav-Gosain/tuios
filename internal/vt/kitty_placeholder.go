package vt

import (
	"image/color"
	"sync"

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

// kittyPlaceholderID reads the image id a placeholder cell names: the low 24
// bits from the foreground colour, and the top 8 from a third combining mark
// when the cell carries one.
//
// It reports false for a foreground that cannot state an id. A placeholder
// drawn in a palette index or in the default colour names no image, and
// guessing one would put somebody else's picture on the screen.
func kittyPlaceholderID(content string, fg color.Color) (uint32, bool) {
	if fg == nil {
		return 0, false
	}
	r, g, b, a := fg.RGBA()
	if a == 0 {
		return 0, false
	}
	id := uint32(r>>8)<<16 | uint32(g>>8)<<8 | uint32(b>>8)
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
	return color.RGBA{
		R: uint8(id >> 16),
		G: uint8(id >> 8),
		B: uint8(id),
		A: 0xff,
	}
}

// translateKittyPlaceholderFg returns the foreground a placeholder cell should
// be drawn in, given the guest's. It returns nil when nothing should change,
// which is the answer for a cell that names no id, an image the host has not
// been told about, and an id too wide for a colour to carry.
func translateKittyPlaceholderFg(content string, fg color.Color, tr KittyImageIDTranslator) color.Color {
	if tr == nil {
		return nil
	}
	guestID, ok := kittyPlaceholderID(content, fg)
	if !ok {
		return nil
	}
	hostID, ok := tr(guestID)
	if !ok || hostID == guestID || hostID > 0xffffff {
		return nil
	}
	return kittyPlaceholderFg(hostID)
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

// kittyPlaceholderSelfDescribing returns the cell content with its row and
// column spelled out, keeping any third mark (the image id's high byte) after
// them. It returns "" when nothing needs changing.
func kittyPlaceholderSelfDescribing(content string, row, col int) string {
	if row < 0 || col < 0 || row >= 297 || col >= 297 {
		return ""
	}
	if r, c, hasRow, hasCol := kittyPlaceholderRowCol(content); hasRow && hasCol && r == row && c == col {
		return ""
	}
	out := string(kittyPlaceholderChar) + string(kitty.Diacritic(row)) + string(kitty.Diacritic(col))
	if high, ok := kittyPlaceholderHighByte(content); ok {
		out += string(kitty.Diacritic(high))
	}
	return out
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

// sameFg reports whether two cell colours are the same, which is how two
// placeholder cells are known to belong to the same image.
func sameFg(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
