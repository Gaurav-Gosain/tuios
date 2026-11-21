package session

import (
	"encoding/json"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/google/uuid"
)

func (d *Daemon) verbListHooks(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Event   string `json:"event"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Event != "" {
		if _, ok := hooks.ParseEventName(p.Event); !ok {
			names := hookEventNames()
			return nil, hintedVerbError(ErrVerbInvalidParams, "unknown hook event "+echoName(p.Event), &VerbHint{
				Param:      "event",
				DidYouMean: closestMatch(p.Event, names),
				Available:  names,
				Detail:     "an event name that is not in this list is ignored when the config loads, so a hook written on one never runs.",
			})
		}
	}
	var sess *Session
	if p.Session != "" {
		var verr *verbError
		if sess, verr = d.resolveVerbSession(cs, p.Session); verr != nil {
			return nil, verr
		}
	} else {
		sess = d.findTargetSession(cs, "")
	}

	rows := anySlice(d.hooks.Rows("session"))

	attached := false
	if sess != nil {
		if tui := d.findTUIClient(sess.ID); tui != nil {
			attached = true
			res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
				CommandType: "list_hooks",
			}, routedVerbTimeout)
			if err == nil && res != nil && res.Success && res.Data != nil {
				rows = append(rows, clientHookRows(res.Data["hooks"])...)
			}
		}
	}

	if p.Event != "" {
		rows = filterHookRows(rows, p.Event)
	}

	return map[string]any{
		"type":            "hook_list",
		"hooks":           rows,
		"total":           len(rows),
		"events":          hookEventNames(),
		"client_attached": attached,
	}, nil
}

func clientHookRows(v any) []any {
	switch rows := v.(type) {
	case []any:
		return rows
	case []map[string]any:
		return anySlice(rows)
	default:
		return nil
	}
}

func anySlice(rows []map[string]any) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return out
}

func filterHookRows(rows []any, event string) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		if m["event"] == event {
			out = append(out, row)
		}
	}
	return out
}

func hookEventNames() []string {
	events := hooks.AllEvents()
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, string(e))
	}
	return names
}
