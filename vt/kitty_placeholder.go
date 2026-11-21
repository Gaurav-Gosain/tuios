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

var diacriticIndex = sync.OnceValue(func() map[rune]int {
	m := make(map[rune]int, 297)
	first := kitty.Diacritic(0)
	for i := 0; ; i++ {
		r := kitty.Diacritic(i)
		if i > 0 && r == first {
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

func kittyPlaceholderFg(id uint32) color.Color {
	id &= 0xffffff
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

type kittyPlaceholderMemo struct {
	x, y        int
	guest, host uint32
}

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

func kittyPlaceholderNext(content, left string, sameImage bool) (row, col int, ok bool) {
	r, c, hasRow, hasCol := kittyPlaceholderRowCol(content)
	switch {
	case hasRow && hasCol:
		return r, c, true
	case !sameImage || !IsKittyPlaceholder(left):
		if hasRow {
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
