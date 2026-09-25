package tmuxcompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

// Environment variables the launcher sets for the shim's log.
const (
	// EnvLog is the log file path. Empty means DefaultLogPath.
	EnvLog = "TUIOS_TMUX_SHIM_LOG"
	// EnvLogAll, set to 1, records every call and not only the ones the shim
	// could not fully answer.
	EnvLogAll = "TUIOS_TMUX_SHIM_LOG_ALL"
)

// logCap is the size past which the log is moved to <path>.1 and started
// again, so a tool that calls tmux in a loop cannot fill the disk.
const logCap = 1 << 20

// Outcomes a log entry records.
const (
	// OutcomeOK: the call was answered in full.
	OutcomeOK = "ok"
	// OutcomeIgnored: the command is known and deliberately does nothing
	// here, such as set-option. The caller was told it succeeded.
	OutcomeIgnored = "ignored"
	// OutcomePartial: the call succeeded, but some of it (a flag, a format
	// variable) was not honoured. Detail says which.
	OutcomePartial = "partial"
	// OutcomeUnsupported: the command or a flag is not implemented. The
	// caller got an error.
	OutcomeUnsupported = "unsupported"
	// OutcomeError: the call was understood and failed.
	OutcomeError = "error"
)

// LogEntry is one line of the log, a JSON object.
type LogEntry struct {
	Time    string   `json:"time"`
	Argv    []string `json:"argv"`
	Outcome string   `json:"outcome"`
	Detail  []string `json:"detail,omitempty"`
}

// Logger appends entries to the shim log. The zero value, and a nil Logger,
// record nothing.
type Logger struct {
	// Path is the file. Empty records nothing.
	Path string
	// All records every call. Without it, only calls whose outcome is not ok
	// or ignored are recorded: that is the list of what to implement next.
	All bool
}

// DefaultLogPath is $XDG_STATE_HOME/tuios/tmux-shim.log.
func DefaultLogPath() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "tuios", "tmux-shim.log")
	}
	return filepath.Join(xdg.StateHome, "tuios", "tmux-shim.log")
}

// LoggerFromEnv builds the logger the environment asks for.
func LoggerFromEnv(getenv func(string) string) *Logger {
	path := getenv(EnvLog)
	if path == "" {
		path = DefaultLogPath()
	}
	return &Logger{Path: path, All: getenv(EnvLogAll) == "1"}
}

// Record writes one entry when the logger's mode asks for it. A failure to
// write is dropped: the log must never turn a working call into a failing one.
func (l *Logger) Record(argv []string, outcome string, detail []string) {
	if l == nil || l.Path == "" {
		return
	}
	if !l.All && (outcome == OutcomeOK || outcome == OutcomeIgnored) {
		return
	}
	line, err := json.Marshal(LogEntry{
		Time:    time.Now().UTC().Format(time.RFC3339),
		Argv:    argv,
		Outcome: outcome,
		Detail:  detail,
	})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return
	}
	if st, err := os.Stat(l.Path); err == nil && st.Size() > logCap {
		_ = os.Rename(l.Path, l.Path+".1")
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	// The log is best effort: a failed write or close has nowhere to go.
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}
