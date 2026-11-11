package main

import (
	"bytes"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"tuios.dev/progstatus"
)

// pane-view as a program that reports to the terminal it runs in.
//
// A terminal that reads OSC 7501, the Program Status Protocol (Rex does),
// shows what the program in each of its panes is doing. In pane-view that
// program is the tuios pane, and the most useful thing to say about it is its
// agent state: a Claude Code or Codex pane reports working or needs input
// through hooks to the daemon, and none of that is in the pane's bytes. So
// when the terminal answers the query, pane-view reports the pane's agent
// state as one record, built the way an attached client builds its host
// records (internal/app/host_program_status.go), and keeps it current from
// the daemon's agent-state events.
//
// What the pane itself writes as OSC 7501 is not passed through then: the
// daemon reads it into the pane's agent state, and the record is tuios's own
// report of that state, never the pane's sequence.
//
// How it could go wrong, and what holds it:
//   - A record written into the middle of the pane's own output would cut a
//     sequence or a character in two. A record goes out only between whole
//     sequences and whole characters of the stream; one that comes while the
//     stream is inside one waits for the next write that ends outside it.
//   - A state event lost while the event stream was down. Each (re)connect
//     reads the pane's state again before it waits for events.
//   - A record left on the terminal after pane-view ends. The exit writes a
//     clear before it resets the terminal.

// paneViewStatusRetry is how long the status loop waits before it dials
// again after the daemon or the event stream went away.
const paneViewStatusRetry = 2 * time.Second

// paneViewStatusQuery is the feature detection query. A terminal that reads
// the protocol answers it with progstatus.QueryReply.
const paneViewStatusQuery = "\x1b]7501;?\x1b\\"

// paneStatusRecord is the body of the record that says what the pane's agent
// is doing, from get-agent-state, or "" when there is nothing to say: no
// agent state, or unknown.
func paneStatusRecord(raw []byte, title string) string {
	var st struct {
		State         string `json:"state"`
		Message       string `json:"message"`
		BlockedBy     string `json:"blocked_by"`
		HarnessID     string `json:"harness_id"`
		ProgramStatus []struct {
			App      string `json:"app"`
			Progress int    `json:"progress"`
		} `json:"program_status"`
	}
	if jsonx.Unmarshal(raw, &st) != nil {
		return ""
	}
	r := progstatus.Report{Progress: progstatus.NoProgress}
	switch st.State {
	case "working":
		r.State = progstatus.Working
	case "needs_input":
		r.State = progstatus.Blocked
		switch st.BlockedBy {
		case "approval":
			r.Kind = progstatus.Permission
		case "question":
			r.Kind = progstatus.Question
		case "auth":
			r.Kind = progstatus.Auth
		}
	case "done":
		r.State = progstatus.Done
	case "errored":
		r.State = progstatus.Error
	case "idle":
		r.State = progstatus.Idle
	default:
		return ""
	}
	app := st.HarnessID
	if len(st.ProgramStatus) > 0 {
		if app == "" {
			app = st.ProgramStatus[0].App
		}
		if r.State == progstatus.Working || r.State == progstatus.Blocked {
			r.Progress = st.ProgramStatus[0].Progress
		}
	}
	if app != "" {
		r.App = progstatus.FitSegment(app, progstatus.MaxApp)
	}
	r.Title = paneStatusText(title, progstatus.MaxTitleDecoded)
	r.Msg = paneStatusText(st.Message, progstatus.MaxMsgDecoded)
	return progstatus.Encode(r)
}

// paneStatusText makes a pane's text fit a title or msg: one line, no control
// or invisible formatting characters, at most limit bytes.
func paneStatusText(s string, limit int) string {
	s = strings.Join(strings.Fields(progstatus.FitText(invisible.Strip(s), 4*limit)), " ")
	return progstatus.FitText(s, limit)
}

// writeOut writes the pane's output to the terminal and notes whether the
// stream now stands between sequences, where a record may go, then sends a
// record that waited for that.
func (v *paneView) writeOut(b []byte) {
	v.outMu.Lock()
	defer v.outMu.Unlock()
	_, _ = v.out.Write(b)
	if len(b) > 0 {
		v.between = v.filter.state == fsGround && !v.filter.esc && !endsMidRune(b)
	}
	v.flushStatusLocked()
}

// setStatus makes body the record the terminal should have, and sends it
// when the stream allows.
func (v *paneView) setStatus(body string) {
	v.outMu.Lock()
	defer v.outMu.Unlock()
	v.statusWant = body
	v.flushStatusLocked()
}

// flushStatusLocked sends the wanted record when it differs from the one
// sent and the stream stands between sequences. outMu is held.
func (v *paneView) flushStatusLocked() {
	if !v.statusOn || !v.between || v.statusWant == v.statusSent {
		return
	}
	if v.statusWant == "" {
		_, _ = v.out.Write([]byte(progstatus.Sequence(progstatus.Report{State: progstatus.Clear})))
	} else {
		_, _ = v.out.Write([]byte("\x1b]7501;" + v.statusWant + "\x1b\\"))
	}
	v.statusSent = v.statusWant
}

// clearStatus is the sequence that removes the record pane-view left on its
// terminal, or "" when it left none. It forgets the record.
func (v *paneView) clearStatus() string {
	v.outMu.Lock()
	defer v.outMu.Unlock()
	if v.statusSent == "" {
		return ""
	}
	v.statusSent, v.statusWant, v.statusOn = "", "", false
	return progstatus.Sequence(progstatus.Report{State: progstatus.Clear})
}

// endsMidRune reports whether b ends inside a UTF-8 character.
func endsMidRune(b []byte) bool {
	start := max(len(b)-utf8.UTFMax, 0)
	tail := b[start:]
	for i := len(tail) - 1; i >= 0; i-- {
		if utf8.RuneStart(tail[i]) {
			return !utf8.FullRune(tail[i:])
		}
	}
	return false
}

// followStatus keeps the record current: it reads the pane's agent state,
// then waits for agent-state events about the pane and reads it again after
// each. It runs until pane-view ends, and dials again when the connection
// drops.
func (v *paneView) followStatus() {
	for {
		v.followStatusOnce()
		time.Sleep(paneViewStatusRetry)
	}
}

func (v *paneView) followStatusOnce() {
	reader, err := dialVerb()
	if err != nil {
		return
	}
	defer func() { _ = reader.Close() }()
	events, err := dialVerb()
	if err != nil {
		return
	}
	defer func() { _ = events.Close() }()

	v.mu.Lock()
	sess, window := v.session, v.window
	v.mu.Unlock()
	read := func() bool {
		raw, err := reader.Call("get-agent-state", map[string]any{"session": sess, "window": window})
		if err != nil {
			return false
		}
		v.setStatus(paneStatusRecord(raw, v.statusTitle))
		return true
	}
	if _, err := events.Call("subscribe", map[string]any{"session": sess, "window": window, "types": []string{"agent-state"}}); err != nil {
		return
	}
	if !read() {
		return
	}
	for {
		line, err := events.ReadEventLine(0)
		if err != nil {
			return
		}
		var ev struct {
			WindowID string `json:"window_id"`
		}
		if jsonx.Unmarshal(line, &ev) == nil && ev.WindowID != "" && ev.WindowID != window {
			continue
		}
		if !read() {
			return
		}
	}
}

// probeStatus reports whether the probe's answers hold the answer to the
// OSC 7501 query.
func probeStatus(got []byte) bool {
	return bytes.Contains(got, []byte(progstatus.QueryReply))
}
