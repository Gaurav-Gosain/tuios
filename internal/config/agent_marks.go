package config

import (
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// agentMarkDefaults is each agent state's mark, and the ASCII form it takes on
// a terminal that cannot draw more, for a glyph set that names none. The marks
// are deliberately distinct shapes rather than the same shape in different
// colours, so the state reads at a glance and survives a monochrome capture: a
// filled circle working, a triangle needs-input, a hollow circle idle, a filled
// square done, and a cross errored. The ASCII set keeps the same states apart
// in one cell.
var agentMarkDefaults = map[string]struct{ glyph, ascii string }{
	"working":     {"●", "*"},
	"needs_input": {"▲", "!"},
	"idle":        {"○", "o"},
	"done":        {"■", "#"},
	"errored":     {"×", "x"},
	// An agent is there and the daemon cannot say what it is doing. It is the
	// common case rather than an edge: the stall timer writes this state for
	// any agent with no screen rules to read, which is every agent matched by
	// name alone.
	//
	// A hollow square rather than a question mark: the row already names the
	// agent, so the question is about the state and not about whether
	// anything is there. It reads as the same family as idle's hollow circle,
	// which is the nearest thing to what it means.
	"unknown": {"□", "?"},
	// Display only, never a pane's state: the agent is at rest and subagents
	// it started still run. A lozenge in the working ink: a shape none of the
	// states uses, and one Go Mono, which tuios shot draws with when no font
	// is given, has. The half-filled circle the design named draws as a
	// missing glyph there.
	"waiting": {"◊", "%"},
}

// SetAgentStateMark is the active glyph set's mark for an agent state, or the
// empty string when the set names none or names one the terminal cannot draw
// (a non-ASCII mark under ASCII mode). A surface with marks of its own design,
// the cockpit, draws its own when this is empty; every other surface takes
// AgentStateMark.
func SetAgentStateMark(state string, ascii bool) string {
	if g := theme.Glyphs().StateMark(state); g != "" && (!ascii || overlay.IsASCII(g)) {
		return g
	}
	return ""
}

// AgentStateMark is the mark an agent state draws: the glyph set's, and the
// built-in one where the set names none, in its ASCII form under ASCII mode.
// It is the empty string for a state that has no mark (none, or unset).
func AgentStateMark(state string, ascii bool) string {
	if g := SetAgentStateMark(state, ascii); g != "" {
		return g
	}
	m, ok := agentMarkDefaults[state]
	if !ok {
		return ""
	}
	if ascii {
		return m.ascii
	}
	return m.glyph
}

// agentMarkInfo is what the active set's marks come to, worked out once per
// set and mode rather than once per drawn row.
type agentMarkInfo struct {
	set   *theme.GlyphSet
	ascii bool
	width int
	runes map[rune]bool
}

var agentMarkCache atomic.Pointer[agentMarkInfo]

func agentMarks(ascii bool) *agentMarkInfo {
	set := theme.Glyphs()
	if c := agentMarkCache.Load(); c != nil && c.set == set && c.ascii == ascii {
		return c
	}
	info := &agentMarkInfo{set: set, ascii: ascii, width: 1, runes: map[rune]bool{}}
	for state, def := range agentMarkDefaults {
		// The built-in marks are kept as chrome in both forms, so a mark a
		// surface drew a moment before the set changed is not stripped.
		for _, r := range def.glyph + def.ascii {
			info.runes[r] = true
		}
		mark := AgentStateMark(state, ascii)
		for _, r := range mark {
			info.runes[r] = true
		}
		if w, ok := theme.MarkWidth(mark); ok && w > info.width {
			info.width = w
		}
	}
	agentMarkCache.Store(info)
	return info
}

// AgentMarkWidth is the width of the widest agent-state mark the active set
// draws, one cell or two. A column of marks that ends on a row's right edge
// pads the narrower marks out to it, so the figures in front of them stay in
// one column.
func AgentMarkWidth(ascii bool) int {
	return agentMarks(ascii).width
}

// IsAgentMarkRune reports whether r is part of an agent-state mark tuios
// draws, built-in or from the active set. The title sanitiser strips symbol
// and emoji codepoints out of foreign titles and keeps these, because they
// are chrome rather than something a guest put there.
func IsAgentMarkRune(r rune, ascii bool) bool {
	return agentMarks(ascii).runes[r]
}
