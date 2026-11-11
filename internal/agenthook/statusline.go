package agenthook

import (
	"bytes"
	"cmp"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

// StatusLineOptions are what one status line report is about: the pane, when
// the caller names it, and whether the harness's turn just ended, so changed
// values go now, whatever the interval.
type StatusLineOptions struct {
	Session string
	Window  string
	TurnEnd bool
}

// statusLineInterval is the shortest gap between two reports for one pane
// while the values keep changing. A status line runs up to three times a
// second during a turn, and the rail has no use for every one.
const statusLineInterval = 15 * time.Second

// statusLineRefresh is how long unchanged values go before they are sent
// again, so a daemon that restarted gets them back.
const statusLineRefresh = 10 * time.Minute

// statusLineNoPaneWait is how long a process no pane was found for waits
// before it asks the daemon again.
const statusLineNoPaneWait = time.Minute

// contextWarnAt is the context use the rail warns at. Crossing it is reported
// at once, whatever the interval.
const contextWarnAt = 80

// StatusLineOutcome is what --explain prints.
type StatusLineOutcome struct {
	Harness string                        `json:"harness"`
	Values  *integration.StatusLineValues `json:"values,omitempty"`
	Session string                        `json:"session,omitempty"`
	Window  string                        `json:"window,omitempty"`
	PaneBy  string                        `json:"pane_by,omitempty"`
	// Sent says set-agent-meta was called and answered.
	Sent  bool   `json:"sent"`
	Skip  string `json:"skip,omitempty"`
	Error string `json:"error,omitempty"`
}

// ReportStatusLine reads the payload and, when the values are due, writes
// them to the pane.
func ReportStatusLine(o StatusLineOptions, harness string, payload []byte, sio IO) StatusLineOutcome {
	out := StatusLineOutcome{Harness: harness}
	if len(bytes.TrimSpace(payload)) == 0 {
		out.Skip = "empty payload"
		return out
	}
	if owner, foreign := integration.StatusLineForeign(harness, sio.Getenv); foreign {
		out.Skip = "foreign harness: " + integration.AgentHintEnv + " names " + owner
		return out
	}
	values, err := integration.ParseStatusLine(harness, payload)
	if err != nil {
		out.Skip = err.Error()
		return out
	}
	out.Values = &values
	if values.Empty() {
		out.Skip = "the payload names no model, context or cost"
		return out
	}

	var client Caller
	closeClient := func() {
		if c, ok := client.(io.Closer); ok {
			_ = c.Close()
		}
	}
	defer closeClient()
	getenv := sio.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	now := time.Now
	if sio.Now != nil {
		now = sio.Now
	}
	stampDir := ""
	if sio.StampDir != nil {
		if dir, err := sio.StampDir(); err == nil {
			stampDir = dir
		}
	}
	out.Session = cmp.Or(o.Session, getenv("TUIOS_SESSION"))
	switch {
	case o.Window != "":
		out.Window, out.PaneBy = o.Window, "flag"
	case getenv("TUIOS_PANE_ID") != "":
		out.Window, out.PaneBy = getenv("TUIOS_PANE_ID"), "env"
	default:
		// No pane named: ask the daemon which pane this process runs in,
		// the way agent-hook does. A status line runs several times a
		// second, so a process in no pane (a harness outside tuios) asks
		// at most once a minute.
		var sid int
		var ancestors []int
		if sio.Self != nil {
			sid, ancestors = sio.Self()
		}
		noPane := ""
		if stampDir != "" {
			noPane = filepath.Join(stampDir, StampName("nopane", strconv.Itoa(sid)))
			if fi, err := os.Stat(noPane); err == nil && now().Sub(fi.ModTime()) < statusLineNoPaneWait {
				out.Skip = "no pane found for this process in the last minute"
				return out
			}
		}
		dialed, err := sio.Dial()
		if err != nil {
			out.Error = err.Error()
			return out
		}
		client = dialed
		out.Session, out.Window, out.PaneBy, err = ResolvePane(Options{}, IO{Getenv: getenv}, client, sid, ancestors)
		if err != nil {
			if noPane != "" {
				_ = os.WriteFile(noPane, nil, 0o600)
				_ = os.Chtimes(noPane, now(), now())
			}
			out.Error = err.Error()
			return out
		}
	}

	stampPath := ""
	var prev *Stamp
	if stampDir != "" {
		stampPath = filepath.Join(stampDir, StampName(out.Session, out.Window))
		prev = ReadStamp(stampPath)
	}
	if due, why := statusLineDue(prev, values, now(), o.TurnEnd); !due {
		out.Skip = why
		// A change held back by the interval is kept as pending, so the
		// end of the turn can send it: Claude Code runs the status line
		// only while the conversation changes, and the last values of a
		// turn are usually the ones held.
		if stampPath != "" && statusLineChanged(prev, values) {
			WritePending(stampPath, prev, values)
		}
		return out
	}

	if client == nil {
		dialed, err := sio.Dial()
		if err != nil {
			out.Error = err.Error()
			return out
		}
		client = dialed
	}
	tokens := values.Tokens()
	if prev != nil && prev.Session == values.Session {
		// What a held run saw and this payload leaves out goes too.
		for k, v := range prev.Pending {
			if _, ok := tokens[k]; !ok {
				tokens[k] = v
			}
		}
	}
	params := map[string]any{
		"window": out.Window,
		"tokens": tokens,
		"source": integration.StatusLineSource,
	}
	if out.Session != "" {
		params["session"] = out.Session
	}
	if _, err := client.Call("set-agent-meta", params); err != nil {
		out.Error = err.Error()
		return out
	}
	out.Sent = true
	if stampPath != "" {
		WriteStamp(stampPath, prev, values.Session, tokens, now())
	}
	return out
}

// statusLineChanged reports whether values differ from what was last sent.
func statusLineChanged(prev *Stamp, values integration.StatusLineValues) bool {
	if prev == nil || values.Session != prev.Session {
		return true
	}
	for k, v := range values.Tokens() {
		if prev.Values[k] != v {
			return true
		}
	}
	return false
}

// statusLineDue says whether values should be sent now, given what was sent
// last, and why not when they should not. turnEnd sends changed values at
// once, so a turn's last cost is not held until the next turn.
func statusLineDue(prev *Stamp, values integration.StatusLineValues, now time.Time, turnEnd bool) (bool, string) {
	if prev == nil {
		return true, ""
	}
	if values.Session != prev.Session {
		return true, ""
	}
	since := now.Sub(time.UnixMilli(prev.At))
	if since < 0 {
		// The clock went back: treat the stamp as old.
		return true, ""
	}
	next := values.Tokens()
	if !statusLineChanged(prev, values) {
		if since >= statusLineRefresh {
			return true, ""
		}
		return false, "unchanged since the last report"
	}
	if next[integration.MetaModel] != "" && next[integration.MetaModel] != prev.Values[integration.MetaModel] {
		return true, ""
	}
	if warned(prev.Values[integration.MetaContext]) != warned(next[integration.MetaContext]) && next[integration.MetaContext] != "" {
		return true, ""
	}
	if since >= statusLineInterval || turnEnd {
		return true, ""
	}
	if prev.RestAt != 0 && prev.RestAt >= prev.At {
		// The turn ended after the last report: the values a run after
		// the end brings are the turn's last, so they go now.
		return true, ""
	}
	return false, "changed, but reported " + since.Round(time.Millisecond).String() + " ago"
}

// warned reports whether a context value is at or over the rail's warning.
func warned(context string) bool {
	n, err := strconv.Atoi(strings.TrimSuffix(context, "%"))
	return err == nil && n >= contextWarnAt
}
