package session

import (
	"math"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

// agentContext is how full an agent's context is, as list-agents gives it:
// the tokens it holds, the window it can hold, the percent, and whether it is
// near the window.
//
// Only what a source stated is given. The tokens come from the transcript, the
// status line feed or a protocol agent's usage update; the window only from a
// source that states it (the status line, a usage update), never from the
// model's name. Without a window there is no percent and no near, unless the
// harness stated the percent itself.
type agentContext struct {
	Tokens  int64 `json:"tokens,omitempty"`
	Window  int64 `json:"window,omitempty"`
	Percent *int  `json:"percent,omitempty"`
	Near    bool  `json:"near"`
}

// agentContextOf reads a pane's context from its live metadata, or nil when
// no source said anything about it.
func agentContextOf(meta map[string]string) *agentContext {
	c := agentContext{
		Tokens: metaCount(meta[integration.MetaContextTokens]),
		Window: metaCount(meta[integration.MetaContextWindow]),
	}
	switch {
	case c.Tokens > 0 && c.Window > 0:
		pct := int(math.Round(math.Min(100, float64(c.Tokens)*100/float64(c.Window))))
		c.Percent = &pct
	default:
		// The status line's own percent, for a payload with no token counts.
		if pct, err := strconv.Atoi(strings.TrimSuffix(meta[integration.MetaContext], "%")); err == nil && pct >= 0 && pct <= 100 {
			c.Percent = &pct
		}
	}
	if c.Tokens == 0 && c.Percent == nil {
		return nil
	}
	c.Near = c.Percent != nil && *c.Percent >= config.SidebarContextWarnAt
	return &c
}

// metaCount reads a token count a source wrote, 0 for anything that is not
// a positive decimal integer.
func metaCount(v string) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// agentLimitResets is the reset a pane stopped on its usage limit names, in
// the harness's words, or "" for a pane in any other state or a limit that
// named none.
func agentLimitResets(w *WindowState) string {
	if w.AgentState != AgentStateLimitReached {
		return ""
	}
	return harness.LimitReset(w.AgentMessage)
}
