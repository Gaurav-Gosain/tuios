package session

import (
	"encoding/json"

	"github.com/google/uuid"
)

// The pip verb: pin a pane as the attached client's picture-in-picture view,
// or unpin it.
//
// The view is the client's, not the session's. It is drawn by the client from
// the cells its own copy of the pane holds, and it is not in session state, so
// the daemon resolves the window and hands the rest to the client it routes
// commands to. With several clients attached that is one of them, the same
// one run-command reaches.

func (d *Daemon) verbPiP(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Off     bool   `json:"off"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Off && p.Window != "" {
		return nil, invalidParam("off", "off unpins whatever is pinned, so it takes no window. Pass one or the other")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	// Resolved here, where names and list indexes are known, so the client is
	// handed an id and a typo fails with the list of windows there are.
	windowID := ""
	if p.Window != "" {
		var windows []WindowState
		if state := sess.GetState(); state != nil {
			windows = state.Windows
		}
		idx, err := findWindowStateIndex(windows, p.Window)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		windowID = windows[idx].ID
	}

	tui := d.findTUIClient(sess.ID)
	if tui == nil {
		return nil, hintedVerbError(ErrVerbNeedsClient,
			"the picture-in-picture view is drawn by a client, and no client is attached",
			&VerbHint{
				Command: "tuios attach " + sess.Name(),
				Detail:  "Attach a client to the session, then retry.",
			})
	}
	mode := "on"
	if p.Off {
		mode = "off"
	}
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
		CommandType: "pip",
		TapeArgs:    []string{windowID, mode},
	}, routedVerbTimeout)
	if err != nil {
		return nil, &verbError{Code: ErrVerbTimeout, Message: "the attached client did not answer: " + err.Error()}
	}
	if res == nil || !res.Success {
		message := "the attached client refused the request"
		if res != nil && res.Message != "" {
			message = res.Message
		}
		return nil, &verbError{Code: ErrVerbCommandFailed, Message: message}
	}
	out := map[string]any{"type": "pip", "pinned": false, "window_id": ""}
	if res.Data != nil {
		if v, ok := res.Data["pinned"]; ok {
			out["pinned"] = v
		}
		if v, ok := res.Data["window_id"]; ok {
			out["window_id"] = v
		}
	}
	return out, nil
}
