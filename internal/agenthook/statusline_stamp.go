package agenthook

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Stamp is the last values sent for one pane, and when.
type Stamp struct {
	Session string            `json:"session,omitempty"`
	Values  map[string]string `json:"values"`
	// At is when they were sent, in Unix milliseconds.
	At int64 `json:"at"`
	// Pending is what a run the interval held back saw and did not send.
	// The next run that is due sends it, and so does the harness's turn
	// end (FlushStatusLine).
	Pending map[string]string `json:"pending,omitempty"`
	// RestAt is when the harness's turn last ended, in Unix milliseconds. A
	// change after a turn ended and before the next report goes at once.
	RestAt int64 `json:"rest_at,omitempty"`
}

// StampName is the stamp file for one pane. Anything but letters,
// digits, '-' and '_' becomes '_', so a name cannot leave the directory.
func StampName(sessionName, window string) string {
	safe := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
				b.WriteRune(r)
			default:
				b.WriteByte('_')
			}
		}
		return b.String()
	}
	return "statusline-" + safe(sessionName) + "-" + safe(window) + ".json"
}

func ReadStamp(path string) *Stamp {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 64<<10 {
		return nil
	}
	var st Stamp
	if jsonx.Unmarshal(data, &st) != nil {
		return nil
	}
	return &st
}

// WriteStamp records what was sent, merged over what was sent
// before, since a payload that leaves a field out does not clear it. Nothing
// is pending after a send.
func WriteStamp(path string, prev *Stamp, convo string, sent map[string]string, now time.Time) {
	st := Stamp{Session: convo, Values: map[string]string{}, At: now.UnixMilli()}
	if prev != nil && prev.Session == convo {
		maps.Copy(st.Values, prev.Values)
		st.RestAt = prev.RestAt
	}
	maps.Copy(st.Values, sent)
	saveStamp(path, st)
}

// WritePending records values a run was not due to send, over
// what earlier held runs left. The stamp's time is left alone, so the
// interval still counts from the last report.
func WritePending(path string, prev *Stamp, values integration.StatusLineValues) {
	st := Stamp{Session: values.Session, Values: map[string]string{}, Pending: map[string]string{}}
	if prev != nil {
		st.At, st.RestAt = prev.At, prev.RestAt
		if prev.Session == values.Session {
			maps.Copy(st.Values, prev.Values)
			maps.Copy(st.Pending, prev.Pending)
		}
	}
	next := values.Tokens()
	if prev != nil && prev.Session == values.Session {
		same := true
		for k, v := range next {
			if prev.Pending[k] != v {
				same = false
				break
			}
		}
		if same {
			// Already recorded: a status line runs several times a
			// second, and the file need not be written for each.
			return
		}
	}
	maps.Copy(st.Pending, next)
	saveStamp(path, st)
}

// FlushStatusLine is the harness's turn end for its status line feed: what a
// run held back for the pane is sent now, and the stamp records that the
// turn ended, so the first change after it goes at once too. A pane with no
// stamp has no status line feed and is left alone. It reports whether
// set-agent-meta was called and answered.
func FlushStatusLine(client Caller, sessionName, window, dir string, now time.Time) (bool, error) {
	if dir == "" || window == "" {
		return false, nil
	}
	path := filepath.Join(dir, StampName(sessionName, window))
	st := ReadStamp(path)
	if st == nil {
		return false, nil
	}
	sent := false
	if len(st.Pending) > 0 {
		params := map[string]any{
			"window": window,
			"tokens": st.Pending,
			"source": integration.StatusLineSource,
		}
		if sessionName != "" {
			params["session"] = sessionName
		}
		if _, err := client.Call("set-agent-meta", params); err != nil {
			return false, err
		}
		if st.Values == nil {
			st.Values = map[string]string{}
		}
		maps.Copy(st.Values, st.Pending)
		st.Pending = nil
		st.At = now.UnixMilli()
		sent = true
	}
	st.RestAt = now.UnixMilli()
	saveStamp(path, *st)
	return sent, nil
}

// saveStamp writes a stamp file, 0600, replaced whole.
func saveStamp(path string, st Stamp) {
	data, err := jsonx.Marshal(st)
	if err != nil {
		return
	}
	_ = atomicfile.Write(path, data, 0o600, false)
}
