package guibridge

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Muxed by default (plan 9): the renderer reopens where the person left, and
// a bridge that dies is started again behind emulators the renderer kept.
//
// Which session. The renderer saves the id of the session it showed. A new
// bridge attaches that id when the daemon still holds it (ids survive a
// rename and a restore), and otherwise the session last active, never
// "the first one listed".
//
// Kept emulators. Before each pane's snapshot the bridge sends a "seq" event
// with the stream position the snapshot ends at. The renderer counts the bytes
// that follow, so it always knows the position its emulator holds. A new
// bridge started with those positions (--resume) compares each with the
// snapshot it is about to send:
//
//   - the same position: the renderer is current. No snapshot is sent, only a
//     "seq" event with kept set, and the renderer keeps its emulator and all
//     its history.
//   - an older position the daemon's catch-up ring still holds: the pane is
//     subscribed from that position, and the renderer gets the bytes it missed
//     as ordinary output. The model's own emulator starts at the snapshot, so
//     those bytes go to the renderer only (app.StreamResumer).
//   - anything else (the ring has moved on, a newer position, another
//     daemon): the snapshot is sent as before.

// resumeWindow is how far behind a snapshot a kept emulator may be and still
// be caught up from the ring. The ring holds the last 64 KiB of a pane's
// output; the margin covers what arrives between the snapshot and the
// subscribe.
const resumeWindow = 48 << 10

// Attached says which session the bridge attached, on which daemon.
type Attached struct {
	// SessionID is the daemon's id for the session.
	SessionID string `json:"session_id,omitempty"`
	// DaemonPID is the daemon's process id, 0 for a daemon on another host.
	// A new pid with a restored session means the daemon restarted.
	DaemonPID int `json:"daemon_pid,omitempty"`
	// Restored marks a session the daemon rebuilt from saved state that no
	// client had attached since.
	Restored bool `json:"restored,omitempty"`
	// Host is the machine the session is on, empty for this one.
	Host string `json:"host,omitempty"`
}

// pickSession chooses the session to attach: the one with id, then name
// (made when missing). With neither it is "", and the daemon picks the
// session the person used last, as a bare `tuios attach` does, or makes one
// when it holds none.
func pickSession(list []session.SessionInfo, id, name string) string {
	if id != "" {
		for _, s := range list {
			if s.ID == id {
				return s.Name
			}
		}
	}
	return name
}

// attachedInfo is what the attached event says about name, read from the
// listing before the attach clears the restored mark.
func attachedInfo(list []session.SessionInfo, name, host string) Attached {
	a := Attached{Host: host}
	if host == "" {
		a.DaemonPID = session.GetDaemonPID()
	}
	for _, s := range list {
		if s.Name == name {
			a.SessionID, a.Restored = s.ID, s.Restored
		}
	}
	return a
}

// listSessions asks the daemon that holds the sessions (this machine's, or
// host's through it) for every session with its id.
func listSessions(version, host string) []session.SessionInfo {
	var c *session.VerbClient
	var err error
	if host == "" {
		c, err = session.DialVerbClientAs(version)
	} else {
		c, _, err = session.DialVerbClientThroughHost(host, version)
	}
	if err != nil {
		return nil
	}
	defer func() { _ = c.Close() }()
	raw, err := c.Call("list-sessions", nil)
	if err != nil {
		return nil
	}
	var out struct {
		Sessions []session.SessionInfo `json:"sessions"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out.Sessions
}

// sessionID asks the daemon for the id of the named session, for a session
// the attach made.
func (m *model) sessionID(name string) string {
	c, err := m.dialVerb()
	if err != nil {
		return ""
	}
	defer func() { _ = c.Close() }()
	raw, err := c.Call("list-sessions", nil)
	if err != nil {
		return ""
	}
	return idOf(raw, name)
}

// ParseResume reads --resume: "pty=seq,pty=seq".
func ParseResume(v string) (map[string]int64, error) {
	out := map[string]int64{}
	for part := range strings.SplitSeq(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, seq, ok := strings.Cut(part, "=")
		n, err := strconv.ParseInt(seq, 10, 64)
		if !ok || id == "" || err != nil || n < 0 {
			return nil, fmt.Errorf("bad --resume entry %q, want pty=seq", part)
		}
		out[id] = n
	}
	return out, nil
}

// setResume replaces the positions the renderer holds, for a switch to a
// session whose emulators it kept.
func (t *tap) setResume(r map[string]int64) {
	t.mu.Lock()
	t.resume = r
	t.mu.Unlock()
}

// keep decides, for a snapshot about to be sent, whether the renderer's own
// emulator stands in for it. It sends the seq event either way.
func (t *tap) keep(ptyID string, state *session.TerminalState) bool {
	if state == nil {
		return false
	}
	t.mu.Lock()
	held, ok := t.resume[ptyID]
	if ok {
		delete(t.resume, ptyID)
	}
	kept := ok && held == state.Seq
	behind := ok && held > 0 && held < state.Seq && state.Seq-held <= resumeWindow
	if behind {
		if t.from == nil {
			t.from = map[string]int64{}
		}
		t.from[ptyID] = held
	}
	t.mu.Unlock()
	switch {
	case kept, behind:
		t.out.JSON(Event{Type: "seq", PTY: ptyID, Seq: held, Kept: true})
		// The kept emulator may be at another size than the pane is now.
		b := appendID(nil, ptyID)
		b = binary.BigEndian.AppendUint16(b, uint16(state.Width))
		b = binary.BigEndian.AppendUint16(b, uint16(state.Height))
		t.out.Frame(KindResize, b)
		return true
	}
	t.out.JSON(Event{Type: "seq", PTY: ptyID, Seq: state.Seq})
	return false
}

// ResumeFrom is app.StreamResumer: the position a pane that keep let
// through is to be streamed from.
func (t *tap) ResumeFrom(ptyID string, snapSeq int64) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.from[ptyID]
	if !ok {
		return 0
	}
	delete(t.from, ptyID)
	if at >= snapSeq {
		return 0
	}
	return at
}

// dialVerb opens a verb connection to the daemon that holds the session: this
// machine's, or the host's through it.
func (m *model) dialVerb() (*session.VerbClient, error) {
	if m.host == "" {
		return session.DialVerbClientAs(m.version)
	}
	c, _, err := session.DialVerbClientThroughHost(m.host, m.version)
	return c, err
}

// idOf finds name's id in a list-sessions answer.
func idOf(raw []byte, name string) string {
	var out struct {
		Sessions []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"sessions"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return ""
	}
	for _, s := range out.Sessions {
		if s.Name == name {
			return s.ID
		}
	}
	return ""
}
