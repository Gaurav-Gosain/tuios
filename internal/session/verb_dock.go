package session

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// The dock's two verbs.
//
// A thing an agent cannot enumerate or verify is a thing an agent cannot rice.
// list-dock-components says what the bar is made of, what every cell currently
// reads, and what each component's command last did; refresh-dock re-runs one.
// Together they are the whole loop: write a script and a five-line table, apply
// it, refresh it, read back whether it worked and why not.
//
// Both route to the attached client, because a dock component is UI. The bar is
// composed in the client, so the components run there, and the daemon has
// nothing to report about a dock nobody is drawing.

func (d *Daemon) verbListDockComponents(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	tui := d.findTUIClient(sess.ID)
	if tui == nil {
		return nil, &verbError{
			Code: ErrVerbNeedsClient,
			Message: "no client is attached, so no dock is drawn and there are no components to list; " +
				"attach the session first",
		}
	}
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
		CommandType: "list_dock_components",
	}, routedVerbTimeout)
	if err != nil {
		return nil, &verbError{Code: ErrVerbTimeout, Message: "the attached client did not answer: " + err.Error()}
	}
	if res == nil || !res.Success {
		return nil, &verbError{Code: ErrVerbCommandFailed, Message: "the attached client refused the request"}
	}

	out := map[string]any{"type": "dock_component_list", "components": []any{}}
	if res.Data != nil {
		if comps, ok := res.Data["components"]; ok {
			out["components"] = comps
		}
	}
	return out, nil
}

func (d *Daemon) verbRefreshDock(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Component string `json:"component"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	tui := d.findTUIClient(sess.ID)
	if tui == nil {
		return nil, &verbError{
			Code: ErrVerbNeedsClient,
			Message: "no client is attached, so nothing is drawing the dock; " +
				"a component refreshes itself when a client attaches",
		}
	}
	name := strings.TrimSpace(p.Component)
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
		CommandType: "refresh_dock",
		TapeArgs:    []string{name},
	}, routedVerbTimeout)
	if err != nil {
		return nil, &verbError{Code: ErrVerbTimeout, Message: "the attached client did not answer: " + err.Error()}
	}
	if res == nil || !res.Success {
		message := "the attached client refused the refresh"
		if res != nil && res.Message != "" {
			message = res.Message
		}
		return nil, &verbError{Code: ErrVerbCommandFailed, Message: message}
	}

	refreshed := name
	if refreshed == "" {
		refreshed = "all"
	}
	return map[string]any{"type": "dock_refreshed", "component": refreshed}, nil
}
