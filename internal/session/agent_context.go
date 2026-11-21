package session

import (
	"math"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

type agentContext struct {
	Tokens  int64 `json:"tokens,omitempty"`
	Window  int64 `json:"window,omitempty"`
	Percent *int  `json:"percent,omitempty"`
	Near    bool  `json:"near"`
}

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

func metaCount(v string) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func agentLimitResets(w *WindowState) string {
	if w.AgentState != AgentStateLimitReached {
		return ""
	}
	return harness.LimitReset(w.AgentMessage)
}
