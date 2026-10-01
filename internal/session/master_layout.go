package session

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The master-stack shape of a workspace as an op.
//
// A workspace's master position and master count are session state: they
// decide every pane's rectangle, and a PTY has one size, so every client of a
// session has to lay a workspace out with the same ones. They are never carried
// in a client's state push. A client that changes one sends MsgMasterLayout,
// the daemon applies it under mutateState, Version advances, and every client
// (the sender included) is sent the result. This is the end state issue #162
// names for every session-owned field, so these fields start there instead of
// adding another merge rule.
//
// The op is recorded with noteTreeOpLocked. Like a tree op it changes one
// field that no push carries, so a push built before it has missed nothing it
// could undo, and a peer whose push crossed it is answered with the state.

// MasterLayoutState is one workspace's master-stack shape.
type MasterLayoutState struct {
	// Position is the side the masters take: one of config.MasterPositions.
	Position string `json:"position,omitempty"`
	// Count is how many panes are masters.
	Count int `json:"count,omitempty"`
	// NoGrid keeps the masters at four or more panes while they are one pane
	// on the left, where the default is a grid. See layout.MasterParams.Grid.
	// The zero value of every field here is the layout as it was before the
	// field existed.
	NoGrid bool `json:"no_grid,omitempty"`
}

// MasterLayoutPayload is the body of MsgMasterLayout.
type MasterLayoutPayload struct {
	// PushOrigin and PushSeq name the op the way they name a state push, in
	// the same sequence, so the sender drops every state built before the op
	// landed. See LayoutTreePayload.
	PushOrigin string
	PushSeq    uint64
	Workspace  int
	Layout     MasterLayoutState
	// IfAbsent applies the op only when the workspace has no shape yet. A
	// client sends its configured default this way the first time it lays a
	// workspace out, so the first client to do so settles the shape and a
	// later one with another default adopts it instead of fighting over it.
	IfAbsent bool
}

// errMasterLayoutSame refuses an op that would not change the state, so it
// does not advance Version and wake every client for nothing.
var errMasterLayoutSame = errors.New("master layout op changes nothing")

// ApplyMasterLayout applies one client's master-stack shape for one
// workspace. It reports whether the state changed; false with a nil error
// means the op changed nothing.
func (s *Session) ApplyMasterLayout(p *MasterLayoutPayload) (bool, error) {
	if p == nil {
		return false, nil
	}
	snap, err := s.mutateStateLocked(func(state *SessionState) error {
		s.notePushLocked(p.PushOrigin, p.PushSeq)
		if p.Workspace < 0 || (p.Workspace > state.workspaceBound() && !IsScratchWorkspace(p.Workspace)) {
			return fmt.Errorf("workspace %d is out of range", p.Workspace)
		}
		if p.Layout.Count < 1 || p.Layout.Count > config.MasterCountMax || !slices.Contains(config.MasterPositions, p.Layout.Position) {
			return fmt.Errorf("master layout %+v is not valid", p.Layout)
		}
		old, had := state.WorkspaceMasterLayout[p.Workspace]
		if (had && p.IfAbsent) || (had && old == p.Layout) {
			return errMasterLayoutSame
		}
		// The snapshot handed out before this aliases the map, so it is
		// cloned before the write.
		next := maps.Clone(state.WorkspaceMasterLayout)
		if next == nil {
			next = make(map[int]MasterLayoutState, 1)
		}
		next[p.Workspace] = p.Layout
		state.WorkspaceMasterLayout = next
		s.noteTreeOpLocked(state.Version+1, p.PushOrigin)
		return nil
	})
	switch {
	case errors.Is(err, errMasterLayoutSame):
		return false, nil
	case err != nil:
		return false, err
	}
	s.publishState(snap)
	return true, nil
}

// handleMasterLayout applies a client's master layout op. An op that is
// applied reaches every client through the state sink. One that changes
// nothing, or that is refused, is answered to its sender alone with the state
// that stands, so the sender never keeps showing a shape the session did not
// take.
func (d *Daemon) handleMasterLayout(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}
	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}
	var p MasterLayoutPayload
	if err := msg.ParsePayload(&p); err != nil {
		return fmt.Errorf("invalid master layout payload: %w", err)
	}
	d.notePushOrigin(cs, session, p.PushOrigin)
	applied, err := session.ApplyMasterLayout(&p)
	if err != nil {
		LogError("Refused a master layout from %s: %v", cs.clientID, err)
		session.NotePush(p.PushOrigin, p.PushSeq)
	}
	if applied {
		return nil
	}
	return d.sendMessage(cs, MsgStateSync, &StateSyncPayload{
		State:       session.GetState(),
		TriggerType: "reconcile",
	})
}

// MasterLayoutOps reports whether the daemon takes MsgMasterLayout. When it
// does not, a master layout change stays on this client.
func (c *TUIClient) MasterLayoutOps() bool {
	return c != nil && c.masterOps.Load()
}

// SendMasterLayout sends one workspace's master-stack shape to the daemon as an
// op, numbered in the same sequence as the state pushes. See SendLayoutTree.
func (c *TUIClient) SendMasterLayout(ws int, st MasterLayoutState, ifAbsent bool) error {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	seq := c.pushSeq.Load() + 1
	p := &MasterLayoutPayload{PushSeq: seq, Workspace: ws, Layout: st, IfAbsent: ifAbsent}
	if origin := c.pushOrigin.Load(); origin != nil {
		p.PushOrigin = *origin
	}
	msg, err := NewMessage(MsgMasterLayout, p)
	if err != nil {
		return err
	}
	if err := c.send(msg); err != nil {
		return err
	}
	c.pushSeq.Store(seq)
	return nil
}
