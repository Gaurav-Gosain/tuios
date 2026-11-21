package config

import (
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

var agentMarkDefaults = map[string]struct{ glyph, ascii string }{
	"working":       {"●", "*"},
	"needs_input":   {"▲", "!"},
	"idle":          {"○", "o"},
	"done":          {"■", "#"},
	"errored":       {"×", "x"},
	"unknown":       {"□", "?"},
	"limit_reached": {"▼", "="},
	"waiting":       {"◊", "%"},
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
