package fuzz

// The pools the generator draws its strings and key names from. They are named
// constants rather than random bytes because the interesting inputs here are a
// small, known set of shapes: a rune whose display width is two, a mark that
// occupies no cell of its own, a separator that means something to a path, and
// a control sequence a title bar will happily echo.

// plainKeys are window-management keys pressed without the leader. Modes are
// included so a run can wander into terminal mode, copy mode, and the rail and
// find its way back out.
var plainKeys = []string{
	"i", "esc", "enter", "tab", "shift+tab", "space", "q", "?",
	"up", "down", "left", "right", "home", "end", "pgup", "pgdown",
	"shift+up", "shift+down", "shift+left", "shift+right",
	"1", "2", "3", "9", "0", "!", "@", "#",
	"h", "j", "k", "l", "n", "p", "v", "y", "/", "g", "G",
	"ctrl+c", "ctrl+d", "ctrl+u", "backspace", "delete",
}

// chordKeys are the second key of a leader chord. Every prefix submenu opener
// is here (t, m, w, D, T) so the run reaches the nested modes, alongside the
// verbs that change layout, which is where the size invariant is decided.
var chordKeys = []string{
	"c", "x", "r", ",", "n", "p", "tab", "shift+tab",
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
	"space", "w", "m", "t", "d", "X", "esc", "[", "?", "D", "T", "q",
	"z", "-", "|", "\\", "R", "=", "s", "P", "b", "S", "W", "L", "e", "j",
}

// guestWrites are what a pane's own program prints. The escape sequences matter
// as much as the text: an alt-screen switch, a resize request, and a title set
// all change what the pane's frame is allowed to look like.
var guestWrites = []string{
	"hello\r\n",
	"$ ",
	"\x1b[2J\x1b[H",
	"\x1b[?1049h",  // enter the alternate screen
	"\x1b[?1049l",  // leave it
	"\x1b]0;t\x07", // set the title
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

// awkwardNames are what a user types into a rename field. Each entry is a shape
// that has broken something: a width table, a session file path, a signature
// hash, or a row the rail had to truncate.
var awkwardNames = []string{
	"",
	" ",
	"a",
	"work",
	"a b c",
	"../escape",          // a path separator in a name that reaches a filename
	"nested/dir/name",    //
	"C:\\windows\\style", //
	".",
	"..",
	"-",
	"--flag",
	"\xe4\xb8\x96\xe7\x95\x8c",  // wide runes, two cells each
	"e\xcc\x81\xcc\x82\xcc\x83", // a base rune under three combining marks
	"\xf0\x9f\x91\xa8\xe2\x80\x8d\xf0\x9f\x91\xa9\xe2\x80\x8d\xf0\x9f\x91\xa6", // a ZWJ sequence
	"\xf0\x9f\x8f\xb3\xef\xb8\x8f\xe2\x80\x8d\xf0\x9f\x8c\x88",                 // flag plus variation selector
	"\xe2\x80\x8b", // a zero-width space, which occupies no cell
	"\xd8\xa7\xd9\x84\xd8\xb9\xd8\xb1\xd8\xa8\xd9\x8a\xd8\xa9", // right-to-left text
	"\x1b[31mred\x1b[0m", // an escape sequence typed as a name
	"tab\there",          //
	"new\nline",          //
	"nul\x00byte",        //
	"\x7f",               //
	"very long name that will certainly not fit inside a collapsed rail strip or a dock chip",
	"\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d\xf0\x9f\x91\x8d",
}

// Overlay and setting indexes. The targets map these onto their own tables; the
// generator only needs to know how many there are.
const (
	overlayCount = 10
	settingCount = 12
)
