package session

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adrg/xdg"
)

// The daemon keeps two records of the same lines. The ring buffer answers
// `tuios logs` and dies with the process. The file outlives it, which is the
// only reason a crash leaves anything to read at all.
//
// Both are fed from logRaw in debug.go, so a line cannot reach one and miss the
// other. Nothing here runs on a client's render or tick path: the sink is
// opened by the daemon process and every write is a daemon-side event.

const (
	// daemonLogName is the file the daemon appends to under the state directory.
	daemonLogName = "tuios/daemon.log"

	// daemonLogMaxBytes caps the file. At the cap the daemon renames it over
	// daemon.log.old and starts a new one, so it keeps between one and two caps
	// of history and never grows without bound.
	daemonLogMaxBytes = 5 << 20
)

var (
	daemonLogMu   sync.Mutex
	daemonLogFH   *os.File
	daemonLogPath string
	daemonLogSize int64
)

// DefaultDaemonLogPath reports the file the daemon appends to when no path is
// configured. It follows the same state-directory convention as the session
// resurrection files and the render trace.
func DefaultDaemonLogPath() string {
	// The environment is read first, as the render trace does, because xdg
	// resolves its directories once at package init and a test that redirects
	// the state directory later would otherwise be ignored.
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, daemonLogName)
	}
	return filepath.Join(xdg.StateHome, daemonLogName)
}

// ringWriter is the standard library logger's output inside the daemon. Every
// log.Printf in the daemon process becomes a ring entry, so the ~112 call sites
// that write nowhere in a background daemon today become visible in
// `tuios logs` without one of them changing.
//
// The lines are recorded at "warn": they are unclassified by construction, they
// are not protocol events, and dropping them below the always-on tier would put
// a daemon panic back where it started.
type ringWriter struct {
	// echo is stderr in a foreground daemon and nil in a background one, whose
	// stderr the parent points at the log file instead.
	echo io.Writer
}

func (w *ringWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\r\n")
	if msg == "" {
		return len(p), nil
	}

	if logBuffer != nil {
		logBuffer.Add("warn", msg)
	}
	writeDaemonLogFile("warn", msg)

	if w.echo != nil {
		fmt.Fprintf(w.echo, "[TUIOS] %s %s\n", timeNow().Format("2006/01/02 15:04:05.000000"), msg)
	}
	return len(p), nil
}

// daemonLoggingInstalled keeps the install to the first caller. The daemon
// process installs it from its entry point, before the daemon is constructed,
// and Run calls it again so that a Run reached any other way is still covered.
var daemonLoggingInstalled atomic.Bool

// InstallDaemonLogging points the standard library logger at the log ring and
// opens the daemon log file. Call it from the daemon process only. The first
// call wins and later ones do nothing.
//
// Install it before building the daemon. The constructor already logs (a
// harness manifest that does not parse is reported from there), and a line
// written before the sink exists is exactly the line this is for.
//
// path selects the file. An empty path means DefaultDaemonLogPath.
func InstallDaemonLogging(foreground bool, path string) {
	if !daemonLoggingInstalled.CompareAndSwap(false, true) {
		return
	}

	var echo io.Writer
	if foreground {
		echo = os.Stderr
	}

	// The ring and the file both carry their own timestamp, so the standard
	// logger must not prepend a second one.
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(&ringWriter{echo: echo})

	// A background daemon's stderr belongs to the parent that spawned it, which
	// points it at this same file. Writing the protocol logger there too would
	// double every line, so it goes nowhere and the file sink is the record.
	if foreground {
		SetDebugOutput(os.Stderr)
	} else {
		SetDebugOutput(io.Discard)
	}

	openDaemonLogFile(path)
}

// openDaemonLogFile opens the log file and writes its header. A failure is not
// fatal: the daemon runs, and the ring buffer still answers `tuios logs`.
func openDaemonLogFile(path string) {
	if path == "" {
		path = DefaultDaemonLogPath()
	}

	daemonLogMu.Lock()
	defer daemonLogMu.Unlock()

	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	size := int64(0)
	if st, serr := fh.Stat(); serr == nil {
		size = st.Size()
	}

	daemonLogFH = fh
	daemonLogPath = path
	daemonLogSize = size
	writeDaemonLogHeaderLocked()
}

// writeDaemonLogHeaderLocked marks the start of a run and states what the file
// can contain. The privacy rule is a property of the level, so the reader is
// told the rule rather than the current level alone.
func writeDaemonLogHeaderLocked() {
	if daemonLogFH == nil {
		return
	}
	header := fmt.Sprintf(""+
		"\n=== tuios daemon log started %s pid=%d level=%s ===\n"+
		"# Levels off, errors, basic and messages record identifiers, sizes, counts, states and error text.\n"+
		"# Levels verbose and trace also record pane content, window titles and paths.\n",
		timeNow().Format(time.RFC3339), os.Getpid(), GetDebugLevel())
	n, err := daemonLogFH.WriteString(header)
	if err == nil {
		daemonLogSize += int64(n)
	}
}

// writeDaemonLogFile appends one entry when the file is open.
//
// Errors and basic events are always written, whatever the configured level:
// the file exists so that a daemon that died leaves a reason behind, and a
// reason that depended on someone having raised the level first is no reason at
// all. Anything above the basic tier follows daemon.log_level.
func writeDaemonLogFile(level string, message string) {
	daemonLogMu.Lock()
	defer daemonLogMu.Unlock()

	if daemonLogFH == nil {
		return
	}

	line := fmt.Sprintf("%s [%s] %s\n", timeNow().Format("2006-01-02T15:04:05.000"), level, message)
	n, err := daemonLogFH.WriteString(line)
	if err != nil {
		return
	}
	daemonLogSize += int64(n)
	if daemonLogSize >= daemonLogMaxBytes {
		rotateDaemonLogLocked()
	}
}

// alwaysFile reports whether a level is written to the file whatever the
// configured level is. Errors and basic events are the always-on tier.
func alwaysFile(level DebugLevel) bool {
	return level <= DebugBasic
}

// rotateDaemonLogLocked moves the full file to daemon.log.old and starts a new
// one. One generation is kept. A rotation that cannot rename gives up and keeps
// appending, because a daemon must not stop over its own log.
func rotateDaemonLogLocked() {
	if daemonLogFH == nil {
		return
	}
	_ = daemonLogFH.Close()
	daemonLogFH = nil

	if err := os.Rename(daemonLogPath, daemonLogPath+".old"); err != nil {
		fh, oerr := os.OpenFile(daemonLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if oerr != nil {
			return
		}
		daemonLogFH = fh
		return
	}

	fh, err := os.OpenFile(daemonLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	daemonLogFH = fh
	daemonLogSize = 0
	writeDaemonLogHeaderLocked()
}

// closeDaemonLogFile releases the file. Test-only: the daemon holds it for the
// life of the process.
func closeDaemonLogFile() {
	daemonLogMu.Lock()
	defer daemonLogMu.Unlock()
	if daemonLogFH != nil {
		_ = daemonLogFH.Close()
		daemonLogFH = nil
	}
	daemonLogPath = ""
	daemonLogSize = 0
}
