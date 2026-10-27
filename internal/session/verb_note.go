package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Return notes.
//
// A note is one short line on a session or a pane that says what it is for
// or what comes next, for the person (or agent) who comes back to it later.
// A preserved layout says where things were; it does not say what you meant
// to do next. Only set-note writes a note, the merge keeps it through every
// client sync (see retainDaemonExclusive), and it is saved with the session.

// NoteMaxBytes is the longest note the daemon keeps. A note is a line, not a
// document: it has to fit a rail row and a dock message.
const NoteMaxBytes = 200

// CleanNote makes a note one line: newlines and tabs become spaces, other
// control characters are dropped, runs of spaces fold to one, the ends are
// trimmed, and the result is cut to NoteMaxBytes on a rune boundary.
func CleanNote(s string) string {
	var b strings.Builder
	b.Grow(min(len(s), NoteMaxBytes))
	space := false
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			continue
		case unicode.IsSpace(r):
			space = b.Len() > 0
			continue
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			continue
		}
		n := utf8.RuneLen(r)
		if space {
			n++
		}
		if b.Len()+n > NoteMaxBytes {
			break
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (d *Daemon) verbSetNote(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Note    string `json:"note"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	note := CleanNote(p.Note)
	id, err := sess.SetNote(p.Window, note)
	if err != nil {
		if p.Window != "" {
			return nil, mapResolveErr(err, sess)
		}
		return nil, newVerbError(ErrVerbInternal, "could not set the note: "+err.Error())
	}
	return map[string]any{"type": "note_set", "session": sess.Name(), "window": id, "note": note}, nil
}

func (d *Daemon) verbGetNote(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	if p.Window == "" {
		return map[string]any{"type": "note", "session": sess.Name(), "window": "", "note": state.Note}, nil
	}
	idx, err := findWindowStateIndex(state.Windows, p.Window)
	if err == nil && idx < 0 {
		err = fmt.Errorf("no window found matching %q", p.Window)
	}
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := state.Windows[idx]
	return map[string]any{"type": "note", "session": sess.Name(), "window": w.ID, "note": w.Note}, nil
}
