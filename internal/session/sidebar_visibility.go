package session

import (
	"errors"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

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
	// All applies the op to every session on the daemon, not only the
	// sender's (issue 589): showing or hiding the rail is how the person
	// wants the screen to look, not a property of one session. A daemon
	// from before the field reads it as false and applies the op to the
	// sender's session alone. IfAbsent ops never set it.
	All bool
}

const maxSidebarVisibilityFrame = 4 * 1024

func validSidebarVisibility(v string) bool {
	return v == SidebarShown || v == SidebarHidden
}

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
			return errfmt.Errorf("sidebar visibility %q is not valid", p.Visibility)
		}
		if (state.Sidebar != "" && p.IfAbsent) || state.Sidebar == p.Visibility {
			return errSidebarSame
		}
		state.Sidebar = p.Visibility
		s.noteTreeOpLocked(state.Version+1, p.PushOrigin, treeOpNoWorkspace)
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

func (d *Daemon) handleSidebarVisibility(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}
	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.replyError(cs, msg, ErrCodeSessionNotFound, "session not found")
	}
	var p SidebarVisibilityPayload
	if err := msg.ParsePayload(&p); err != nil {
		return errfmt.Errorf("invalid sidebar payload: %w", err)
	}
	d.notePushOrigin(cs, session, p.PushOrigin)
	applied, err := session.ApplySidebarVisibility(&p)
	if err != nil {
		LogError("Refused a sidebar op from %s: %v", cs.clientID, err)
		session.NotePush(p.PushOrigin, p.PushSeq)
	}
	if err == nil && p.All && !p.IfAbsent {
		d.applySidebarEverywhere(session, p.Visibility)
	}
	if applied {
		return nil
	}
	return d.sendMessage(cs, MsgStateSync, &StateSyncPayload{
		State:       session.GetState(),
		TriggerType: "reconcile",
	})
}

func (d *Daemon) applySidebarEverywhere(from *Session, visibility string) {
	for _, s := range d.manager.AllSessions() {
		if s == from {
			continue
		}
		s.SetSidebarVisibility(visibility)
	}
}

// SetSidebarVisibility sets the rail's visibility on a session for an op
// sent to another one (see SidebarVisibilityPayload.All). It changes nothing
// when the session already holds the value.
func (s *Session) SetSidebarVisibility(visibility string) {
	if !validSidebarVisibility(visibility) {
		return
	}
	snap, err := s.mutateStateLocked(func(state *SessionState) error {
		if state.Sidebar == visibility {
			return errSidebarSame
		}
		state.Sidebar = visibility
		s.noteTreeOpLocked(state.Version+1, "", treeOpNoWorkspace)
		return nil
	})
	if err != nil {
		return
	}
	s.publishState(snap)
}

// SendSidebarVisibility sends whether the rail is shown to the daemon as an
// op, numbered in the same sequence as the state pushes. See SendLayoutTree.
func (c *TUIClient) SendSidebarVisibility(shown, ifAbsent, all bool) error {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	seq := c.pushSeq.Load() + 1
	v := SidebarHidden
	if shown {
		v = SidebarShown
	}
	p := &SidebarVisibilityPayload{PushSeq: seq, Visibility: v, IfAbsent: ifAbsent, All: all && !ifAbsent}
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
