package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// agentMetaFromWire is the display half of a pane's synced agent metadata. It
// hands back cur when nothing changed, so a state sync that moves nothing on
// the rail allocates nothing for it. The daemon prunes expired tokens itself,
// so every token that arrives is live.
func agentMetaFromWire(cur []sessiontree.MetaToken, in []session.AgentMetaToken) []sessiontree.MetaToken {
	if len(in) == 0 {
		return nil
	}
	if len(cur) == len(in) {
		same := true
		for i := range in {
			if cur[i].Key != in[i].Key || cur[i].Value != in[i].Value {
				same = false
				break
			}
		}
		if same {
			return cur
		}
	}
	out := make([]sessiontree.MetaToken, len(in))
	for i, t := range in {
		out[i] = sessiontree.MetaToken{Key: t.Key, Value: t.Value}
	}
	return out
}
