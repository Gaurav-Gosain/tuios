package overlay

import "sync/atomic"

// Chrome is the handful of characters the overlay family draws its own
// furniture with. A host that lets the user choose a glyph set pushes one in;
// with none pushed every helper draws what it always drew.
//
// It is pushed rather than pulled for the same reason SetASCII is: this package
// depends on nothing inside tuios so that it can be lifted out, and the registry
// that resolves a named set lives above it. An empty field means "keep the
// built-in", so a set that renames two glyphs is two fields rather than a full
// table.
type Chrome struct {
	Ellipsis   string // truncation marker
	Sigil      string // the mark fronting an input or a cursor row
	ArrowLeft  string // cycler and tab-strip overflow, pointing back
	ArrowRight string // the same, pointing on
	Rule       string // a panel's solid divider
	DashRule   string // a dialog's lighter divider
}

// chrome holds the pushed set. A pointer so the zero state is "nothing pushed"
// and a swap is one store; atomic because every session's render loop reads it
// while a set-config on another session writes it.
var chrome atomic.Pointer[Chrome]

// SetChrome records the glyphs the overlay furniture is drawn with. A nil
// argument clears back to the built-ins.
func SetChrome(c *Chrome) { chrome.Store(c) }

// chromeOr returns the pushed glyph for one role, or def when no set is pushed
// or the set leaves that role alone.
//
// Callers check UseASCII before reaching here, so a pushed glyph outside 7-bit
// loses to ASCII mode. That is deliberate: the set a user chose says what their
// font can draw, and --ascii-only says the running terminal cannot draw it,
// which is the narrower claim and the one to believe. The test is per glyph
// rather than per set, so a set that is ASCII in the roles it can be keeps them
// in a terminal that has to be.
func chromeOr(role func(*Chrome) string, def string) string {
	if c := chrome.Load(); c != nil {
		if g := role(c); g != "" && (!UseASCII() || IsASCII(g)) {
			return g
		}
	}
	return def
}

// IsASCII reports whether every rune of s is 7-bit, which is the test for
// whether a glyph can be drawn in a terminal that has told us it cannot manage
// more.
func IsASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}
