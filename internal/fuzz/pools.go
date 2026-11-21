package fuzz

var plainKeys = []string{
	"i", "esc", "enter", "tab", "shift+tab", "space", "q", "?",
	"up", "down", "left", "right", "home", "end", "pgup", "pgdown",
	"shift+up", "shift+down", "shift+left", "shift+right",
	"1", "2", "3", "9", "0", "!", "@", "#",
	"h", "j", "k", "l", "n", "p", "v", "y", "/", "g", "G",
	"ctrl+c", "ctrl+d", "ctrl+u", "backspace", "delete",
}

var chordKeys = []string{
	"c", "x", "r", ",", "n", "p", "tab", "shift+tab",
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
	"space", "w", "m", "t", "d", "X", "esc", "[", "?", "D", "T", "q",
	"z", "-", "|", "\\", "R", "=", "s", "P", "b", "S", "W", "L", "e", "j",
}

var guestWrites = []string{
	"hello\r\n",
	"$ ",
	"\x1b[2J\x1b[H",
	"\x1b[?1049h",
	"\x1b[?1049l",
	"\x1b]0;t\x07",
	"\x1b[8;24;80t",
	"\x1b[999;999H",
	"\x1b[1;1H\x1b[K",
	"\xe4\xb8\x96\xe4\xb8\x96\xe4\xb8\x96\r\n",
	"\xf0\x9f\x91\x8d\xf0\x9f\x91\xa8\xe2\x80\x8d\xf0\x9f\x91\xa9\r\n",
	"e\xcc\x81\xcc\x82\r\n",
	"\x1b[31mred\x1b[0m\r\n",
	"col\tcol\tcol\r\n",
	"\xff\xfe bad utf8\r\n",
	"long line without any newline at all so it has to wrap somewhere",
}

var awkwardNames = []string{
	"",
	" ",
	"a",
	"work",
	"a b c",
	"../escape",
	"nested/dir/name",
	"C:\\windows\\style",
	".",
	"..",
	"-",
	"--flag",
	"\xe4\xb8\x96\xe7\x95\x8c",
	"e\xcc\x81\xcc\x82\xcc\x83",
	"\xf0\x9f\x91\xa8\xe2\x80\x8d\xf0\x9f\x91\xa9\xe2\x80\x8d\xf0\x9f\x91\xa6",
	"\xf0\x9f\x8f\xb3\xef\xb8\x8f\xe2\x80\x8d\xf0\x9f\x8c\x88",
	"\xe2\x80\x8b",
	"\xd8\xa7\xd9\x84\xd8\xb9\xd8\xb1\xd8\xa8\xd9\x8a\xd8\xa9",
	"\x1b[31mred\x1b[0m",
	"tab\there",
	"new\nline",
	"nul\x00byte",
	"\x7f",
	"very long name that will certainly not fit inside a collapsed rail strip or a dock chip",
	"\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d",
}

const (
	overlayCount  = 10
	settingCount  = 12
	settingValues = 12
)
