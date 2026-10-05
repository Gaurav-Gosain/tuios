package session

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/google/uuid"
)

// switch-session moves an attached client to another session in place, the
// way the session switcher does. It is the CLI's route to what herdr's
// workspace.focus already did, and it reaches the other machines the
// switcher and the rail reach.
//
// It acts on a client, not on a session, so it is an admin action for a pane
// (scopeDeny in verbScopes) and a write over a link.
//
// Which client: the one named by client, else the one showing session, else
// the one showing the caller's own session when the caller runs in a pane,
// else the only TUI client attached. With several attached and nothing to
// choose by, the call is refused rather than guessed.
//
// A target on this daemon is checked, and with create made, before the
// client hears of it, so a mistyped name is refused here with the names that
// exist. A target on another machine is opened by the client, which holds
// the link, and the client answers once the attach there has landed or
// failed.

// switchSessionParams are the verb's parameters.
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
		raw, _ := json.Marshal(args)
		if _, verr := d.verbNewSession(cs, raw); verr != nil {
			if verr.Code != ErrVerbSessionExists {
				return nil, verr
			}
			// Made by somebody else between the lookup and the create: the
			// switch still goes where it was asked to.
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
	// The client answers before it switches, because the switch moves the
	// connection the answer goes over. So the answer here waits for the
	// switch to show, and says when it did not.
	if !sess.waitChange(routedVerbTimeout, func() bool { return connSessionID(tui) == sess.ID }) {
		return nil, newVerbError(ErrVerbCommandFailed, "the client did not switch to "+sess.Name()+". Its screen says why")
	}
	return out, nil
}

// switchToHost has the client open a session on another machine. The client
// holds the link and makes the attach, and answers once it has landed.
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

// switchTarget picks the client a switch moves. See verbSwitchSession.
func (d *Daemon) switchTarget(cs *connState, p switchSessionParams) (*connState, *verbError) {
	if p.Client != "" {
		var ids []string
		var found *connState
		d.clientsMu.RLock()
		for _, c := range d.clients {
			c.mu.Lock()
			ok := c.isTUIClient && c.attached
			c.mu.Unlock()
			if !ok {
				continue
			}
			ids = append(ids, c.clientID)
			if c.clientID == p.Client {
				found = c
			}
		}
		d.clientsMu.RUnlock()
		if found == nil {
			slices.Sort(ids)
			return nil, hintedVerbError(ErrVerbInvalidParams, "no attached client has id "+echoName(p.Client), &VerbHint{
				Param:     "client",
				Command:   "tuios list-clients",
				Available: ids,
			})
		}
		return found, nil
	}
	if p.Session != "" {
		sess, verr := d.resolveVerbSession(p.Session)
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

// connSessionID is the session a connection shows, "" while detached.
func connSessionID(c *connState) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.attached {
		return ""
	}
	return c.sessionID
}

// nonEmpty is s, or fallback when s is empty.
func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
