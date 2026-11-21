package vt

// CharSet represents a character set designator.
// This can be used to select a character set for G0 or G1 and others.
type CharSet map[byte]string

var (
	charsetUK = CharSet{
		'#': "£",
	}
	charsetSpecialDrawing = CharSet{
		'`': "◆",
		'a': "▒",
		'b': "␉",
		'c': "␌",
		'd': "␍",
		'e': "␊",
		'f': "°",
		'g': "±",
		'h': "␤",
		'i': "␋",
		'j': "┘",
		'k': "┐",
		'l': "┌",
		'm': "└",
		'n': "┼",
		'o': "⎺",
		'p': "⎻",
		'q': "─",
		'r': "⎼",
		's': "⎽",
		't': "├",
		'u': "┤",
		'v': "┴",
		'w': "┬",
		'x': "│",
		'y': "⩽",
		'z': "⩾",
		'{': "π",
		'|': "≠",
		'}': "£",
		'~': "·",
	}
)
