package config

import (
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// GlyphSet names the chrome glyph set, or "default" for the shipped one. It is
// the shape half of a rice, beside Theme's colour half.
var GlyphSet = theme.GlyphSetNone

// BorderStyleGlyphs is the appearance.border_style value meaning "take the
// border from the active glyph set".
//
// A set could have been let win over border_style whenever it defines a border,
// which is one fewer thing to say. It would also mean that selecting a set
// silently turned an option the user had already set into a no-op, with nothing
// on screen or in get-config to say why. This way both settings are always live
// and the one that is in charge is the one the user named.
const BorderStyleGlyphs = "glyphs"

// glyphOr returns the active set's glyph for one role, or a default: the ASCII
// one in a terminal that cannot draw more, and the shipped one otherwise.
//
// The ASCII test is per glyph rather than per set, so a set that is 7-bit in the
// roles it can be keeps those roles under --ascii-only and gives up only the
// ones it cannot draw. A set-wide test would have thrown away a whole
// hand-written set over one arrow.
func glyphOr(role func(*theme.GlyphSet) string, def, asciiDef string) string {
	if g := role(theme.Glyphs()); g != "" && (!UseASCIIOnly || overlay.IsASCII(g)) {
		return g
	}
	if UseASCIIOnly {
		return asciiDef
	}
	return def
}

// The rail's marks. They were literals in two sidebar files with an ASCII
// branch beside each, which is why a rail could not be restyled without
// editing Go.

// GetRailFocusMark is the one-cell gutter mark saying "you are here".
func GetRailFocusMark() string {
	return glyphOr(func(g *theme.GlyphSet) string { return g.Focus }, "▎", ">")
}

// GetRailAttentionMark is the gutter mark saying "this one wants a human".
func GetRailAttentionMark() string {
	return glyphOr(func(g *theme.GlyphSet) string { return g.Attention }, "▎", "!")
}

// GetRailBullet is the quiet mark a resting row carries.
func GetRailBullet() string {
	return glyphOr(func(g *theme.GlyphSet) string { return g.Bullet }, "·", ".")
}

// GetRailAddGlyph is the new-session and new-window control.
func GetRailAddGlyph() string {
	return glyphOr(func(g *theme.GlyphSet) string { return g.Add }, "+", "+")
}

// GetRailCollapseGlyph is the arrow that folds the rail down to its strip.
//
// Two cells in ASCII, where "«" has no one-cell stand-in: a lone "<" in the
// footer of a column of one-cell marks reads as one more mark rather than as a
// control. The rail measures its own footer, so unlike a window button this
// role is not held to a width.
func GetRailCollapseGlyph() string {
	return glyphOr(func(g *theme.GlyphSet) string { return g.Collapse }, "«", "<<")
}

// GetRailExpandGlyph is the arrow that opens it again.
func GetRailExpandGlyph() string {
	return glyphOr(func(g *theme.GlyphSet) string { return g.Expand }, "»", ">>")
}

// ResolvedGlyphs reports what is actually drawn for every role: the active
// set's glyph where it names one, and the built-in beneath it where it does
// not.
//
// It exists because a set says only what it changes, which is the right shape
// for a file a person writes and the wrong answer to "what will this look
// like". The describe verb reports this rather than the set's own fields, so a
// caller ricing over the protocol sees the frame it is going to get.
func ResolvedGlyphs() map[string]string {
	out := map[string]string{
		"close":           GetWindowButtonCloseMark(),
		"maximize":        GetWindowButtonMaximizeMark(),
		"minimize":        GetWindowButtonMinimizeMark(),
		"dot":             GetWindowButtonDot(),
		"pill_left":       GetWindowPillLeft(),
		"pill_right":      GetWindowPillRight(),
		"rule":            GetWindowSeparatorChar(),
		"separator":       GetDockSeparator(),
		"arrow_left":      GetDockWorkspaceMoreLeft(),
		"arrow_right":     GetDockWorkspaceMoreRight(),
		"focus":           GetRailFocusMark(),
		"attention":       GetRailAttentionMark(),
		"bullet":          GetRailBullet(),
		"add":             GetRailAddGlyph(),
		"collapse":        GetRailCollapseGlyph(),
		"expand":          GetRailExpandGlyph(),
		"scrollbar_thumb": GetScrollbarThumbChar(),
		"scrollbar_track": GetScrollbarTrackChar(),
		"ellipsis":        overlay.Ellipsis(),
		"sigil":           overlay.SigilMark(),
		"dash_rule":       overlay.DashRuleGlyph(),
	}
	// The border is reported through the set's own resolution rather than
	// through GetBorderForStyle, because the two answer different questions.
	// GetBorderForStyle says what is on screen now, which is border_style's
	// answer unless border_style is "glyphs"; this says what the set would
	// draw, which is what a caller inspecting a set is asking. The describe
	// verb reports border_style alongside so the caller can tell whether the
	// two are currently the same thing.
	b := glyphSetBorder()
	for role, glyph := range map[string]string{
		"border.top": b.Top, "border.bottom": b.Bottom,
		"border.left": b.Left, "border.right": b.Right,
		"border.top_left": b.TopLeft, "border.top_right": b.TopRight,
		"border.bottom_left": b.BottomLeft, "border.bottom_right": b.BottomRight,
		"border.middle": b.Middle, "border.middle_top": b.MiddleTop,
		"border.middle_bottom": b.MiddleBottom,
		"border.middle_left":   b.MiddleLeft, "border.middle_right": b.MiddleRight,
	} {
		out[role] = glyph
	}
	return out
}

// glyphBorrowMu serialises the borrow in GlyphsForSet, so two callers cannot
// leave the selection somewhere neither of them asked for.
var glyphBorrowMu sync.Mutex

// GlyphsForSet is what a set would draw if it were selected, without selecting
// it.
//
// Answered by borrowing the selection, reading through the same accessors the
// renderer calls, and putting it back. That is the honest answer and the only
// one that cannot drift from what a frame would actually show: a preview built
// by reading the set's own fields would report the roles it names and say
// nothing about the built-ins underneath, which is most of what a person sees.
//
// The borrow is process-local state that no frame is composed from while it is
// held, so a caller on the render goroutine is safe. It is still not free, so
// callers building a list of previews should do it once rather than per frame.
func GlyphsForSet(id string) map[string]string {
	glyphBorrowMu.Lock()
	defer glyphBorrowMu.Unlock()
	prev := theme.ActiveGlyphSetID()
	theme.SetActiveGlyphs(id)
	drawn := ResolvedGlyphs()
	theme.SetActiveGlyphs(prev)
	return drawn
}
