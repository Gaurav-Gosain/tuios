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

const paneViewStatusRetry = 2 * time.Second

const paneViewStatusQuery = "\x1b]7501;?\x1b\\"

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

func paneStatusText(s string, limit int) string {
	s = strings.Join(strings.Fields(progstatus.FitText(invisible.Strip(s), 4*limit)), " ")
	return progstatus.FitText(s, limit)
}

func (v *paneView) writeOut(b []byte) {
	v.outMu.Lock()
	defer v.outMu.Unlock()
	_, _ = v.out.Write(b)
	if len(b) > 0 {
		v.between = v.filter.state == fsGround && !v.filter.esc && !endsMidRune(b)
	}
	v.flushStatusLocked()
}

func (v *paneView) setStatus(body string) {
	v.outMu.Lock()
	defer v.outMu.Unlock()
	v.statusWant = body
	v.flushStatusLocked()
}

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

func (v *paneView) clearStatus() string {
	v.outMu.Lock()
	defer v.outMu.Unlock()
	if v.statusSent == "" {
		return ""
	}
	v.statusSent, v.statusWant, v.statusOn = "", "", false
	return progstatus.Sequence(progstatus.Report{State: progstatus.Clear})
}

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

func probeStatus(got []byte) bool {
	return bytes.Contains(got, []byte(progstatus.QueryReply))
}
