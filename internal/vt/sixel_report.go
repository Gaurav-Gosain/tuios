package vt

import "strconv"

// SixelMaxRegisters is the number of colour registers a pane is told it has
// (XTSMGRAPHICS), and the most a decoded image may use.
const SixelMaxRegisters = 256

// XTSMGRAPHICS answers. See sixelGraphicsReply.
const (
	xtsmColors   = 1
	xtsmGeometry = 2
)

// sixelGraphicsReply answers XTSMGRAPHICS (CSI ? Pi ; Pa ; Pv S), which lsix,
// notcurses and others ask before they draw: how many colour registers there
// are, and the largest image in pixels. Only reads are honoured; a request to
// change either value is answered with the value that stands, which is what
// xterm does when it refuses one. A pane that is not told sixel gets the
// error status, so a program that asks here and not in DA1 comes to the same
// answer.
func sixelGraphicsReply(item, action int, on bool, widthPx, heightPx int) string {
	if !on {
		return "\x1b[?" + strconv.Itoa(item) + ";3;0S"
	}
	switch item {
	case xtsmColors:
		if action < 1 || action > 4 {
			return "\x1b[?1;2;0S"
		}
		return "\x1b[?1;0;" + strconv.Itoa(SixelMaxRegisters) + "S"
	case xtsmGeometry:
		if action < 1 || action > 4 {
			return "\x1b[?2;2;0S"
		}
		return "\x1b[?2;0;" + strconv.Itoa(widthPx) + ";" + strconv.Itoa(heightPx) + "S"
	default:
		return "\x1b[?" + strconv.Itoa(item) + ";1;0S"
	}
}

// DeviceAttributes is the DA1 attribute list both backends answer with: a
// VT220-class terminal (62) with ANSI colour (22), and sixel graphics (4) only
// when the pane's images will be shown. chafa, img2sixel's callers, lsix,
// timg, yazi, notcurses and matplotlib's sixel backends decide between sixel
// and a text fallback on that one attribute, so listing it when the picture
// would be dropped trades their fallback for a blank.
//
// Every attribute listed is one a guest may act on, so only what is
// implemented is listed. The answer used to add 132 columns (1), selective
// erase (6), national replacement character sets (9), technical characters
// (15) and user windows (18), none of which exists here: a pane cannot change
// its own width, no cell can be protected, and only the UK and DEC special
// graphics sets are designated. The class stays 62, which is what kitty,
// ghostty and foot answer, so a program that reads the class to decide it is
// on a VT220 or later (vim, neovim, notcurses, tmux) sees no change.
func DeviceAttributes(sixel bool) []int {
	if sixel {
		return []int{62, 4, 22}
	}
	return []int{62, 22}
}
