package session

import (
	"encoding/json"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/google/uuid"
)

type switchSessionParams struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Create  bool   `json:"create"`
	Cwd     string `json:"cwd"`
	Session string `json:"session"`
	Client  string `json:"client"`
}

func (d *Daemon) verbSwitchSession(cs *connState, params json.RawMessage) (any, *verbError) {
	var p switchSessionParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Host = strings.TrimSpace(p.Host)
	if p.Name == "" {
		return nil, invalidParam("name", "name is the session to switch to and cannot be empty")
	}
	if p.Cwd != "" && !p.Create {
		return nil, invalidParam("cwd", "cwd is the directory of a session create makes, so it needs create")
	}
	if p.Host == federation.LocalHostName {
		p.Host = ""
	}

	tui, verr := d.switchTarget(cs, p)
	if verr != nil {
		return nil, verr
	}
	clientID := tui.clientID

	if p.Host != "" {
		return d.switchToHost(tui, p)
	}

	created := false
	sess, _ := d.manager.ResolveSession(p.Name)
	if sess == nil {
		if !p.Create {
			available := d.sessionNames()
			return nil, hintedVerbError(ErrVerbSessionNotFound, "session "+echoName(p.Name)+" not found", &VerbHint{
				Param:      "name",
				Command:    "tuios switch-session --create " + p.Name,
				DidYouMean: closestMatch(p.Name, available),
				Available:  available,
				Detail:     "the name matches no live session. Pass create to make it, with cwd for the directory it starts in.",
			})
		}
		args := map[string]any{"name": p.Name}
		if p.Cwd != "" {
			args["cwd"] = p.Cwd
		}
		raw, _ := jsonx.Marshal(args)
		if _, verr := d.verbNewSession(cs, raw); verr != nil {
			if verr.Code != ErrVerbSessionExists {
				return nil, verr
			}
		} else {
			created = true
		}
		if sess, _ = d.manager.ResolveSession(p.Name); sess == nil {
			return nil, newVerbError(ErrVerbInternal, "session "+echoName(p.Name)+" was created and is gone")
		}
	}

	out := map[string]any{
		"type":      "session_switched",
		"session":   sess.Name(),
		"client_id": clientID,
		"created":   created,
	}
	if connSessionID(tui) == sess.ID {
		out["already"] = true
		return out, nil
	}

	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
		CommandType: "switch_session",
		TapeArgs:    []string{sess.Name()},
	}, routedVerbTimeout)
	if err != nil {
		return nil, newVerbError(ErrVerbCommandFailed, err.Error())
	}
	if !res.Success {
		return nil, newVerbError(ErrVerbCommandFailed, nonEmpty(res.Message, "the client refused the switch"))
	}
	if !sess.waitChange(routedVerbTimeout, func() bool { return connSessionID(tui) == sess.ID }) {
		return nil, newVerbError(ErrVerbCommandFailed, "the client did not switch to "+sess.Name()+". Its screen says why")
	}
	return out, nil
}

func (d *Daemon) switchToHost(tui *connState, p switchSessionParams) (any, *verbError) {
	args := []string{p.Name, p.Host, "", p.Cwd}
	if p.Create {
		args[2] = "create"
	}
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
		CommandType: "switch_session",
		TapeArgs:    args,
	}, routedVerbTimeout)
	if err != nil {
		return nil, newVerbError(ErrVerbCommandFailed, err.Error())
	}
	if !res.Success {
		return nil, newVerbError(ErrVerbCommandFailed, nonEmpty(res.Message, "the client refused the switch"))
	}
	return map[string]any{
		"type":      "session_switched",
		"session":   p.Name,
		"host":      p.Host,
		"client_id": tui.clientID,
	}, nil
}

func (d *Daemon) switchTarget(cs *connState, p switchSessionParams) (*connState, *verbError) {
	if p.Client != "" {
		return d.attachedClientByID(p.Client)
	}
	if p.Session != "" {
		sess, verr := d.resolveVerbSession(cs, p.Session)
		if verr != nil {
			return nil, verr
		}
		if tui := d.findTUIClient(sess.ID); tui != nil {
			return tui, nil
		}
		return nil, hintedVerbError(ErrVerbNeedsClient, "no client is attached to session "+sess.Name(), &VerbHint{
			Command: "tuios list-clients",
			Detail:  "switch-session moves a client that shows a session. Name a session a client shows, or a client by its id.",
		})
	}
	if fromPane, window := d.peerPane(cs); fromPane && window != "" {
		if from := d.sessionHoldingWindow(window); from != nil {
			if tui := d.findTUIClient(from.ID); tui != nil {
				return tui, nil
			}
			return nil, hintedVerbError(ErrVerbNeedsClient, "no client is attached to this pane's session, "+from.Name(), &VerbHint{
				Command: "tuios list-clients",
				Detail:  "switch-session moves the client that shows the pane's session, and none does. Name another session with -s, or a client with --client.",
			})
		}
	}
	var only *connState
	n := 0
	d.clientsMu.RLock()
	for _, c := range d.clients {
		c.mu.Lock()
		ok := c.isTUIClient && c.attached
		c.mu.Unlock()
		if ok {
			only = c
			n++
		}
	}
	d.clientsMu.RUnlock()
	switch n {
	case 1:
		return only, nil
	case 0:
		return nil, hintedVerbError(ErrVerbNeedsClient, "no client is attached to switch", &VerbHint{
			Command: "tuios attach",
			Detail:  "switch-session moves a client that is attached. Attach one, or use tuios attach to open the session here.",
		})
	}
	return nil, hintedVerbError(ErrVerbInvalidParams, "several clients are attached, so the one to switch must be named", &VerbHint{
		Param:   "session",
		Command: "tuios list-clients",
		Detail:  "Name the session the client shows with -s, or the client with --client.",
	})
}

func connSessionID(c *connState) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.attached {
		return ""
	}
	return c.sessionID
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
