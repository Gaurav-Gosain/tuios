package session

import (
	"encoding/json"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/google/uuid"
)

// list-hooks answers the one question the oldest extension point in tuios could
// not answer: "why does my hook not fire?".
//
// A dock component reports its exit code, when it last ran and its last error,
// which is why "my dock component prints nothing" is answerable. A hook ran
// with its output discarded and its error dropped, so a command that was never
// found and a command that worked looked identical. This reports the table as
// loaded plus the same three facts per command, under the same three names.
//
// It answers for both sides. The daemon fires the hooks for the facts it owns
// and the client fires the ones that need a terminal, so a caller that only saw
// one table would be told a hook does not exist when it does.

func (d *Daemon) verbListHooks(_ *connState, params json.RawMessage) (any, *verbError) {
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
	// The session is only needed to find the client that holds the other half of
	// the table, so a named one that does not resolve is still an error and an
	// omitted one on a daemon holding no sessions is not. Reading the hook table
	// is the first thing a plugin author does, often before any session exists.
	var sess *Session
	if p.Session != "" {
		var verr *verbError
		if sess, verr = d.resolveVerbSession(p.Session); verr != nil {
			return nil, verr
		}
	} else {
		sess = d.findTargetSession("")
	}

	rows := anySlice(d.hooks.Rows("session"))

	// The client's half is asked for rather than assumed. A session with nobody
	// attached reports client_attached false, so an empty client table reads as
	// "nothing is drawing this session" and not as "the hook is not there".
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

// clientHookRows reads the rows a client sent back, whichever shape the wire
// left them in.
//
// This is the gob trap this codebase has been bitten by before. The client
// sends []map[string]any inside a map[string]any; that concrete type is
// registered with gob, so it decodes back as itself and not as []any. A caller
// that asserts only one of the two shapes drops every client row without an
// error, which is the failure this whole verb exists to prevent.
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

// anySlice widens the daemon's own rows so they can be appended to the client's,
// which may arrive as []any.
func anySlice(rows []map[string]any) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return out
}

// filterHookRows keeps the rows for one event. The rows are maps because half
// of them came back over the wire from the client.
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

// hookEventNames lists every event a hook can be written on, so a caller can
// check a spelling without reading the docs.
func hookEventNames() []string {
	events := hooks.AllEvents()
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, string(e))
	}
	return names
}
