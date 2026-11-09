package session

import (
	"errors"
	"maps"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// The scrolling layout's columns as an op (notes: strip-ops-plan.md).
//
// A workspace's columns (which panes each column holds, top to bottom, and
// how wide it is) decide every pane's rectangle in the scrolling layout, so
// they are session state. A client used to send its whole columns in every
// state push, and the later push won. A strip change the daemon made (the
// arrange verb's scroll ops, which guios uses) was then put back by the push
// of a client that built it on the columns from before: two scroll.consume
// at once with two TUIs attached ended one column short of both.
//
// A client whose hello offers ScrollColumnOps sends a workspace's columns as
// MsgScrollColumns when it changes them, the daemon applies the op under
// mutateState, Version advances, and every client, the sender included, is
// sent the result. Its pushes do not move the columns (columnsByOp). A
// client that predates the op keeps sending them in its pushes, which the
// daemon takes as before, so mixed versions keep working with the old rule.
//
// The op is the whole columns of one workspace, as for the trees: every strip
// change in the client already ends in columns. Two ops on one workspace are
// settled by the order they reach the daemon. The op is recorded like a tree
// op (noteTreeOpLocked) and counted in the push sequence (PushSeen), so a
// client drops every state built before its own op landed.

// ScrollColumnsPayload is the body of MsgScrollColumns.
type ScrollColumnsPayload struct {
	// PushOrigin and PushSeq name the op in the sequence of the client's
	// pushes. See LayoutTreePayload.
	PushOrigin string
	PushSeq    uint64
	Workspace  int
	// Columns are the workspace's columns. Empty means the workspace has no
	// strip.
	Columns []SerializedScrollColumn
}

// maxScrollColumnWorkspaces caps how many workspaces hold columns, for the
// reason maxMasterLayouts caps the shapes.
const maxScrollColumnWorkspaces = 64

var errScrollColumnsSame = errors.New("scroll columns op changes nothing")

// liveColumns is cols with every pane that is not a window on ws dropped,
// each pane once, and the columns left empty dropped.
func liveColumns(state *SessionState, ws int, cols []SerializedScrollColumn) []SerializedScrollColumn {
	on := make(map[string]bool, len(state.Windows))
	for i := range state.Windows {
		if state.Windows[i].Workspace == ws {
			on[state.Windows[i].ID] = true
		}
	}
	seen := map[string]bool{}
	var out []SerializedScrollColumn
	for _, c := range cols {
		var ids []string
		for _, id := range c.Windows {
			if on[id] && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		c.Windows = ids
		c.Active = min(max(c.Active, 0), len(ids)-1)
		out = append(out, c)
	}
	return out
}

// sameColumns reports whether two workspaces' columns are the same.
func sameColumns(a, b []SerializedScrollColumn) bool {
	return slices.EqualFunc(a, b, func(x, y SerializedScrollColumn) bool {
		return slices.Equal(x.Windows, y.Windows) && x.Proportion == y.Proportion && x.FixedWidth == y.FixedWidth && x.Active == y.Active
	})
}

// ApplyScrollColumns applies one client's columns for one workspace. It
// reports whether the state changed; false with a nil error means the op
// changed nothing.
func (s *Session) ApplyScrollColumns(p *ScrollColumnsPayload) (bool, error) {
	if p == nil {
		return false, nil
	}
	snap, err := s.mutateStateLocked(func(state *SessionState) error {
		s.notePushLocked(p.PushOrigin, p.PushSeq)
		ws := p.Workspace
		if ws < 1 || (ws > state.workspaceBound() && !IsScratchWorkspace(ws)) {
			return errfmt.Errorf("workspace %d is out of range", ws)
		}
		cols := liveColumns(state, ws, p.Columns)
		old, had := state.WorkspaceScrollColumns[ws]
		if sameColumns(old, cols) {
			return errScrollColumnsSame
		}
		if !had && len(state.WorkspaceScrollColumns) >= maxScrollColumnWorkspaces {
			return errfmt.Errorf("the session already holds the columns of %d workspaces", maxScrollColumnWorkspaces)
		}
		// The snapshot handed out before this aliases the map.
		next := maps.Clone(state.WorkspaceScrollColumns)
		if next == nil {
			next = make(map[int][]SerializedScrollColumn, 1)
		}
		if len(cols) == 0 {
			delete(next, ws)
		} else {
			next[ws] = cols
		}
		state.WorkspaceScrollColumns = next
		s.noteTreeOpLocked(state.Version+1, p.PushOrigin, ws)
		return nil
	})
	switch {
	case errors.Is(err, errScrollColumnsSame):
		return false, nil
	case err != nil:
		return false, err
	}
	s.publishState(snap)
	return true, nil
}

// handleScrollColumns applies a client's columns op. One that is applied
// reaches every client through the state sink. One that changes nothing, or
// that is refused, is answered to its sender alone with the state that
// stands, so the sender never keeps showing columns the session did not take.
func (d *Daemon) handleScrollColumns(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}
	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.replyError(cs, msg, ErrCodeSessionNotFound, "session not found")
	}
	var p ScrollColumnsPayload
	if err := msg.ParsePayload(&p); err != nil {
		return errfmt.Errorf("invalid scroll columns payload: %w", err)
	}
	d.notePushOrigin(cs, session, p.PushOrigin)
	applied, err := session.ApplyScrollColumns(&p)
	if err != nil {
		LogError("Refused scroll columns from %s: %v", cs.clientID, err)
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

// ScrollColumnOps reports whether the daemon takes MsgScrollColumns. When it
// does not, the columns travel in this client's pushes.
func (c *TUIClient) ScrollColumnOps() bool {
	return c != nil && c.scrollOps.Load()
}

// SendScrollColumns sends one workspace's columns to the daemon as an op,
// numbered in the same sequence as the state pushes. See SendLayoutTree.
func (c *TUIClient) SendScrollColumns(ws int, cols []SerializedScrollColumn) error {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	seq := c.pushSeq.Load() + 1
	p := &ScrollColumnsPayload{PushSeq: seq, Workspace: ws, Columns: cols}
	if origin := c.pushOrigin.Load(); origin != nil {
		p.PushOrigin = *origin
	}
	msg, err := NewMessage(MsgScrollColumns, p)
	if err != nil {
		return err
	}
	if len(msg.Payload) > maxStateUpdateBytes {
		return &FrameTooLargeError{Type: MsgScrollColumns, Size: uint32(len(msg.Payload)) + 2, Limit: uint32(maxStateUpdateBytes) + 2}
	}
	if err := c.send(msg); err != nil {
		return err
	}
	c.pushSeq.Store(seq)
	return nil
}
