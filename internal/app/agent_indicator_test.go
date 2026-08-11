package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestAgentStateIndicatorDistinctPerMode guards the one property every surface
// that shows agent state depends on: each state has a glyph, and no two states
// share one. ASCII-only mode used to have no glyphs at all, which dropped agent
// state from the rail, the title bars and the palette at once.
func TestAgentStateIndicatorDistinctPerMode(t *testing.T) {
	states := []session.AgentState{
		session.AgentStateWorking,
		session.AgentStateNeedsInput,
		session.AgentStateIdle,
		session.AgentStateDone,
		session.AgentStateErrored,
	}

	for _, ascii := range []bool{false, true} {
		overlay.SetASCII(ascii)
		t.Cleanup(func() { overlay.SetASCII(false) })

		seen := map[string]session.AgentState{}
		for _, state := range states {
			glyph := agentStateIndicator(string(state))
			if glyph == "" {
				t.Errorf("ascii=%v: state %q has no glyph", ascii, state)
				continue
			}
			if other, dup := seen[glyph]; dup {
				t.Errorf("ascii=%v: states %q and %q share the glyph %q", ascii, other, state, glyph)
			}
			seen[glyph] = state
		}

		if got := agentStateIndicator(string(session.AgentStateNone)); got != "" {
			t.Errorf("ascii=%v: a pane running no agent got the glyph %q, want none", ascii, got)
		}
	}
}

// TestAgentStateIndicatorASCIIIsOneCell checks the ASCII glyphs fit the single
// cell every caller reserves for them, including the 2-column glyph rail.
func TestAgentStateIndicatorASCIIIsOneCell(t *testing.T) {
	overlay.SetASCII(true)
	t.Cleanup(func() { overlay.SetASCII(false) })

	for _, state := range []session.AgentState{
		session.AgentStateWorking,
		session.AgentStateNeedsInput,
		session.AgentStateIdle,
		session.AgentStateDone,
		session.AgentStateErrored,
	} {
		glyph := agentStateIndicator(string(state))
		if len(glyph) != 1 || glyph[0] > 0x7f {
			t.Errorf("state %q rendered %q, want a single ASCII byte", state, glyph)
		}
	}
}
