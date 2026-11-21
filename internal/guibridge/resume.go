package guibridge

import (
	"encoding/binary"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

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
	if jsonx.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out.Sessions
}

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
			return nil, errfmt.Errorf("bad --resume entry %q, want pty=seq", part)
		}
		out[id] = n
	}
	return out, nil
}

func (t *tap) setResume(r map[string]int64) {
	t.mu.Lock()
	t.resume = r
	t.mu.Unlock()
}

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
		b := appendID(nil, ptyID)
		b = binary.BigEndian.AppendUint16(b, uint16(state.Width))
		b = binary.BigEndian.AppendUint16(b, uint16(state.Height))
		t.out.Frame(KindResize, b)
		return true
	}
	t.out.JSON(Event{Type: "seq", PTY: ptyID, Seq: state.Seq})
	return false
}

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

func (m *model) dialVerb() (*session.VerbClient, error) {
	if m.host == "" {
		return session.DialVerbClientAs(m.version)
	}
	c, _, err := session.DialVerbClientThroughHost(m.host, m.version)
	return c, err
}

func idOf(raw []byte, name string) string {
	var out struct {
		Sessions []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"sessions"`
	}
	if jsonx.Unmarshal(raw, &out) != nil {
		return ""
	}
	for _, s := range out.Sessions {
		if s.Name == name {
			return s.ID
		}
	}
	return ""
}
