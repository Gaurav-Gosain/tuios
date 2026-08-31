package hooks

import (
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// This file carries the answer to the oldest unanswerable question about
// tuios's oldest extension point: "why does my hook not fire?".
//
// A hook used to run with stdout and stderr set to nil and its error dropped,
// so a command that was never found, exited non-zero, or was never registered
// at all produced exactly the same observable result as one that worked. The
// dock's components already report what their command last did, and that is why
// "my dock component prints nothing" is answerable. Hooks now report the same
// three facts in the same three field names: last_exit, last_run, last_error.

// stderrTailLimit bounds how many bytes of a hook's stderr are kept. A hook is
// a user command and may write without limit, so the capture is a tail with a
// fixed ceiling rather than a buffer that grows to whatever it was handed.
const stderrTailLimit = 1024

// verbose gates the one line logged per firing. The failure warning is not
// gated: a hook that fails is the thing the user is looking for.
var verbose atomic.Bool

// SetVerbose turns the per-firing log line on or off. The daemon and the client
// both set it from the debug level, so a user chasing a hook turns on logging
// where they already turn it on.
func SetVerbose(on bool) { verbose.Store(on) }

// Verbose reports whether hook firings are logged.
func Verbose() bool { return verbose.Load() }

// Status is what one registered hook command last did. The field names mirror
// the dock's component rows deliberately: an agent that already reads
// list-dock-components can read this without learning a second vocabulary.
type Status struct {
	// Event is the hook event this command is registered on.
	Event string
	// Command is the shell command, exactly as it was configured.
	Command string
	// Runs counts every firing since the table was loaded. Zero is the direct
	// answer to "my hook does not fire": it never ran.
	Runs int
	// LastRun is when the most recent firing finished. Zero when it never ran.
	LastRun time.Time
	// LastExit is the exit code of the most recent firing.
	LastExit int
	// LastDuration is how long the most recent firing took.
	LastDuration time.Duration
	// LastError is the reason the most recent failing firing failed, with the
	// tail of its stderr appended. Empty once a firing succeeds, so a hook that
	// was fixed stops reporting the failure it used to have. Bounded.
	LastError string
}

// hookResult is what one command run reports back to the manager.
type hookResult struct {
	exitCode int
	err      error
	stderr   string
	duration time.Duration
}

// statusKey addresses one command in the table: its event and its position in
// that event's list. Position rather than the command text, so a user who
// registered the same command twice sees two rows rather than one shared one.
type statusKey struct {
	event Event
	index int
}

// tailBuffer keeps only the last limit bytes written to it. It is bounded at
// write time rather than at read time, so a hook that writes a gigabyte to
// stderr costs the limit and not the gigabyte.
type tailBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	if n >= t.limit {
		t.buf = append(t.buf[:0], p[n-t.limit:]...)
		return n, nil
	}
	if len(t.buf)+n > t.limit {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)+n-t.limit:]...)
	}
	t.buf = append(t.buf, p...)
	return n, nil
}

// String returns the kept tail with surrounding whitespace removed.
func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// Statuses reports what every registered hook command last did, ordered by
// event name and then by the order the commands were registered in.
func (m *Manager) Statuses() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Status, 0, len(m.status))
	for _, event := range AllEvents() {
		for i, command := range m.hooks[event] {
			st := Status{Event: string(event), Command: command}
			if rec, ok := m.status[statusKey{event: event, index: i}]; ok {
				st.Runs = rec.Runs
				st.LastRun = rec.LastRun
				st.LastExit = rec.LastExit
				st.LastDuration = rec.LastDuration
				st.LastError = rec.LastError
			}
			out = append(out, st)
		}
	}
	return out
}

// record stores what one firing did and logs a failure. It is called from the
// hook's own goroutine, never from a render or update loop.
func (m *Manager) record(event Event, index int, command string, res hookResult) {
	message := failureMessage(res)

	m.mu.Lock()
	if m.status == nil {
		m.status = make(map[statusKey]*Status)
	}
	key := statusKey{event: event, index: index}
	rec := m.status[key]
	if rec == nil {
		rec = &Status{Event: string(event), Command: command}
		m.status[key] = rec
	}
	rec.Command = command
	rec.Runs++
	rec.LastRun = time.Now()
	rec.LastExit = res.exitCode
	rec.LastDuration = res.duration
	rec.LastError = message
	m.mu.Unlock()

	if message != "" {
		log.Printf("hooks: warning: the %s hook failed. Exit %d after %s. Command: %s. Stderr: %s",
			event, res.exitCode, res.duration.Round(time.Millisecond), command, message)
	}
}

// failureMessage renders a run's failure, or empty when it succeeded. The
// stderr tail is already bounded, and the whole message is bounded again so a
// hook that writes one very long line without a newline cannot make a status
// row unbounded either.
func failureMessage(res hookResult) string {
	if res.err == nil && res.exitCode == 0 {
		return ""
	}
	parts := make([]string, 0, 2)
	if res.err != nil {
		parts = append(parts, res.err.Error())
	}
	if res.stderr != "" {
		parts = append(parts, res.stderr)
	}
	msg := strings.Join(parts, ": ")
	if len(msg) > stderrTailLimit {
		msg = msg[len(msg)-stderrTailLimit:]
	}
	return msg
}

// clearStatus drops every recorded run. The caller holds the write lock. It is
// called wherever the table changes, so a row can never describe a command that
// is no longer at that position.
func (m *Manager) clearStatus() { m.status = nil }

// Rows renders the table as the list-hooks verb serialises it. side is
// "session" for the hooks a daemon runs and "client" for the ones an attached
// client runs.
//
// The field names are the dock's on purpose: last_exit, last_run and last_error
// say the same three things about a hook that they already say about a dock
// component, so a caller that can debug one can debug the other.
func (m *Manager) Rows(side string) []map[string]any {
	if m == nil {
		return []map[string]any{}
	}
	statuses := m.Statuses()
	rows := make([]map[string]any, 0, len(statuses))
	for _, st := range statuses {
		lastRun := ""
		if !st.LastRun.IsZero() {
			lastRun = st.LastRun.Format(time.RFC3339)
		}
		rows = append(rows, map[string]any{
			"event":      st.Event,
			"side":       side,
			"command":    st.Command,
			"runs":       st.Runs,
			"last_exit":  st.LastExit,
			"last_run":   lastRun,
			"last_error": st.LastError,
			"last_ms":    st.LastDuration.Milliseconds(),
		})
	}
	return rows
}
