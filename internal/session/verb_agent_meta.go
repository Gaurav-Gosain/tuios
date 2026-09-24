package session

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"
)

// verbSetAgentMeta records display metadata about the agent in a pane. See
// agent_meta.go for what it is and why it is display only.
func (d *Daemon) verbSetAgentMeta(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string          `json:"session"`
		Window  string          `json:"window"`
		Tokens  json.RawMessage `json:"tokens"`
		Source  string          `json:"source"`
		TTLMs   int64           `json:"ttl_ms"`
		Clear   bool            `json:"clear"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	keys, values, err := decodeAgentMetaTokens(p.Tokens)
	if err != nil {
		return nil, invalidParam("tokens", "tokens: "+err.Error())
	}
	if len(keys) == 0 && !p.Clear {
		return nil, invalidParam("tokens", "tokens is required unless clear is true: an object of key to a string, or to null to remove the key")
	}
	if len(keys) > AgentMetaMaxPerCall {
		return nil, invalidParam("tokens", "one call sets at most "+strconv.Itoa(AgentMetaMaxPerCall)+" keys")
	}
	var truncated []string
	for i, k := range keys {
		if !ValidAgentMetaKey(k) {
			return nil, invalidParam("tokens", "metadata key "+echoName(k)+
				" is not valid: use 1 to "+strconv.Itoa(AgentMetaMaxKey)+
				" lower-case letters, digits, '_' or '-', starting with a letter")
		}
		if slices.Contains(reservedAgentMetaKeys, k) {
			return nil, hintedVerbError(ErrVerbInvalidParams, "metadata key "+echoName(k)+" is written by tuios from hook activity", &VerbHint{
				Param:  "tokens",
				Detail: "now and prompt are set by the daemon from the activity a harness hook reports with set-agent-state. Nothing was set.",
			})
		}
		if values[i] == nil {
			continue
		}
		v, cut := CleanAgentMetaValue(*values[i])
		if cut {
			truncated = append(truncated, k)
		}
		values[i] = &v
	}
	if p.TTLMs < 0 || time.Duration(p.TTLMs)*time.Millisecond > AgentMetaMaxTTL {
		return nil, invalidParam("ttl_ms", "ttl_ms must be between 0 (no expiry) and "+
			strconv.FormatInt(AgentMetaMaxTTL.Milliseconds(), 10))
	}
	source, _ := CleanAgentMetaValue(p.Source)

	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	idx, err := findWindowStateIndex(sess.GetState().Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	windowID := sess.GetState().Windows[idx].ID

	tokens, err := sess.SetAgentMeta(windowID, AgentMetaUpdate{
		Keys:   keys,
		Values: values,
		Source: source,
		TTL:    time.Duration(p.TTLMs) * time.Millisecond,
		Clear:  p.Clear,
		// clear removes what callers wrote. The reserved keys are the
		// daemon's, so they stay.
		Protect: reservedAgentMetaKeys,
	})
	if errors.Is(err, errAgentMetaFull) {
		return nil, invalidParam("tokens", err.Error()+". Remove keys by setting them to null, or pass clear")
	}
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	if truncated == nil {
		truncated = []string{}
	}
	return map[string]any{
		"type":      "agent_meta_set",
		"window_id": windowID,
		"meta":      agentMetaMap(tokens, time.Now().UnixNano()),
		"truncated": truncated,
	}, nil
}

// agentMetaMap is a pane's live metadata as the verbs report it: key to value,
// never nil, so a caller reads {} rather than null for a pane with none.
func agentMetaMap(tokens []AgentMetaToken, now int64) map[string]string {
	out := make(map[string]string, len(tokens))
	for _, t := range liveAgentMeta(tokens, now) {
		out[t.Key] = t.Value
	}
	return out
}
