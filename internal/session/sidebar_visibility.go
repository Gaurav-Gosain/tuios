package session

import (
	"errors"
	"fmt"
	"os"
)

// Whether the sidebar rail is shown, as an op.
//
// The rail is chrome, and the panes' box is settled across every client of a
// session (see LayoutReserve): the daemon takes the largest reserve any client
// asks for. So a rail one client hid while another still drew it took nothing
// back for the panes, and the client that hid it was left with a blank band
// where the rail had been. Showing or hiding the rail is therefore session
// state. Every client of a session shows it or hides it together, and the
// panes grow or shrink on every screen at once.
//
// It travels the way the master-stack shape does (see master_layout.go): a
// client that toggles the rail sends MsgSidebarVisibility, the daemon applies
// it under mutateState, Version advances, and every client (the sender
// included) is sent the result. A push never carries the field, so two
// clients cannot argue over it the way issue #162 describes.
//
// Only whether the rail is shown is shared. Its width stays each viewer's
// own, because it says how much of one screen the rail may have, and the
// daemon settles the panes' box against the widest rail a client draws.

// The values SessionState.Sidebar holds. Empty means no client has said yet.
const (
	SidebarShown  = "shown"
	SidebarHidden = "hidden"
)

// SidebarVisibilityPayload is the body of MsgSidebarVisibility.
type SidebarVisibilityPayload struct {
	// PushOrigin and PushSeq name the op the way they name a state push, in
	// the same sequence, so the sender drops every state built before the op
	// landed. See LayoutTreePayload.
	PushOrigin string
	PushSeq    uint64
	// Visibility is SidebarShown or SidebarHidden.
	Visibility string
	// IfAbsent applies the op only when the session has no value yet. A
	// client sends its configured default this way when it attaches, so the
	// first client to attach settles the rail and a later one with another
	// config adopts it instead of fighting over it.
	IfAbsent bool
}

// maxSidebarVisibilityFrame bounds a MsgSidebarVisibility frame. The payload
// is a push origin, a number, a short word and a flag.
const maxSidebarVisibilityFrame = 4 * 1024

// validSidebarVisibility says whether v is a value the session can hold.
func validSidebarVisibility(v string) bool {
	return v == SidebarShown || v == SidebarHidden
}

// errSidebarSame refuses an op that would not change the state, so it does
// not advance Version and wake every client for nothing.
var errSidebarSame = errors.New("sidebar op changes nothing")

// ApplySidebarVisibility applies one client's sidebar op. It reports whether
// the state changed; false with a nil error means the op changed nothing.
func (s *Session) ApplySidebarVisibility(p *SidebarVisibilityPayload) (bool, error) {
	if p == nil {
		return false, nil
	}
	snap, err := s.mutateStateLocked(func(state *SessionState) error {
		s.notePushLocked(p.PushOrigin, p.PushSeq)
		if !validSidebarVisibility(p.Visibility) {
			return fmt.Errorf("sidebar visibility %q is not valid", p.Visibility)
		}
		if (state.Sidebar != "" && p.IfAbsent) || state.Sidebar == p.Visibility {
			return errSidebarSame
		}
		state.Sidebar = p.Visibility
		// Recorded as a tree op is: the op changes one field that no push
		// carries, so a push built before it has missed nothing it could undo.
		s.noteTreeOpLocked(state.Version+1, p.PushOrigin)
		return nil
	})
	switch {
	case errors.Is(err, errSidebarSame):
		return false, nil
	case err != nil:
		return false, err
	}
	s.publishState(snap)
	return true, nil
}

// RestoreSidebar puts the saved sidebar visibility back on a session a restore
// just rebuilt. UpdateState keeps the daemon's own copy of the field (see
// retainDaemonExclusive), and a new session has none, so the restore cannot
// carry it in the state it pushes.
func (s *Session) RestoreSidebar(saved string) {
	if !validSidebarVisibility(saved) {
		return
	}
	_ = s.mutateState(func(state *SessionState) error {
		state.Sidebar = saved
		return nil
	})
}

// handleSidebarVisibility applies a client's sidebar op. An op that is
// applied reaches every client through the state sink. One that changes
// nothing, or that is refused, is answered to its sender alone with the state
// that stands, so the sender never keeps showing a rail the session did not
// take.
func (d *Daemon) handleSidebarVisibility(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}
	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}
	var p SidebarVisibilityPayload
	if err := msg.ParsePayload(&p); err != nil {
		return fmt.Errorf("invalid sidebar payload: %w", err)
	}
	d.notePushOrigin(cs, session, p.PushOrigin)
	applied, err := session.ApplySidebarVisibility(&p)
	if err != nil {
		LogError("Refused a sidebar op from %s: %v", cs.clientID, err)
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

// legacySidebar makes this client behave as a build from before the sidebar
// was session state: it ignores SidebarOps in the daemon's welcome, keeps its
// rail to itself, and ignores the session's value. It is
// TUIOS_SIDEBAR_LEGACY=1, and it exists so the mixed-version promise can be
// tested without an old build.
func legacySidebar() bool {
	return os.Getenv("TUIOS_SIDEBAR_LEGACY") == "1"
}

// SidebarOps reports whether the daemon takes MsgSidebarVisibility. When it
// does not, showing or hiding the rail stays on this client.
func (c *TUIClient) SidebarOps() bool {
	return c != nil && c.sidebarOps.Load()
}

// SendSidebarVisibility sends whether the rail is shown to the daemon as an
// op, numbered in the same sequence as the state pushes. See SendLayoutTree.
func (c *TUIClient) SendSidebarVisibility(shown, ifAbsent bool) error {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	seq := c.pushSeq.Load() + 1
	v := SidebarHidden
	if shown {
		v = SidebarShown
	}
	p := &SidebarVisibilityPayload{PushSeq: seq, Visibility: v, IfAbsent: ifAbsent}
	if origin := c.pushOrigin.Load(); origin != nil {
		p.PushOrigin = *origin
	}
	msg, err := NewMessage(MsgSidebarVisibility, p)
	if err != nil {
		return err
	}
	if err := c.send(msg); err != nil {
		return err
	}
	c.pushSeq.Store(seq)
	return nil
}
