package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

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
