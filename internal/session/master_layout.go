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

// maxMasterLayouts caps how many workspaces hold a shape. An ordinary
// workspace is bounded by the workspace count and a scratch one by the windows
// on it, so this only stops a client that keeps sending new scratch numbers
// from growing the state past what a push can carry.
const maxMasterLayouts = 64

// checkMasterLayout refuses a shape the session cannot hold: a workspace that
// is not one (0, past the workspace count, or a scratch workspace with no
// window on it), a side that is not a side, or a count out of range.
func checkMasterLayout(state *SessionState, ws int, st MasterLayoutState) error {
	ok := ws >= 1 && ws <= state.workspaceBound()
	if IsScratchWorkspace(ws) {
		ok = countOnWorkspace(state, ws) > 0
	}
	if !ok {
		return fmt.Errorf("workspace %d is out of range", ws)
	}
	if st.Count < 1 || st.Count > config.MasterCountMax || !slices.Contains(config.MasterPositions, st.Position) {
		return fmt.Errorf("master layout %+v is not valid", st)
	}
	return nil
}

// RestoreMasterLayouts puts the saved master-stack shapes back on a session a
// restore just rebuilt. UpdateState keeps the daemon's own copy of the field
// (see retainDaemonExclusive), and a new session has none, so the restore
// cannot carry them in the state it pushes. An entry the session could not
// take from a client is dropped here too.
func (s *Session) RestoreMasterLayouts(saved map[int]MasterLayoutState) {
	if len(saved) == 0 {
		return
	}
	_ = s.mutateState(func(state *SessionState) error {
		next := make(map[int]MasterLayoutState, len(saved))
		for ws, st := range maps.Clone(saved) {
			if checkMasterLayout(state, ws, st) == nil && len(next) < maxMasterLayouts {
				next[ws] = st
			}
		}
		if len(next) == 0 {
			return nil
		}
		state.WorkspaceMasterLayout = next
		return nil
	})
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
		if err := checkMasterLayout(state, p.Workspace, p.Layout); err != nil {
			return err
		}
		old, had := state.WorkspaceMasterLayout[p.Workspace]
		if (had && p.IfAbsent) || (had && old == p.Layout) {
			return errMasterLayoutSame
		}
		if !had && len(state.WorkspaceMasterLayout) >= maxMasterLayouts {
			return fmt.Errorf("the session already holds %d master layouts", maxMasterLayouts)
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
