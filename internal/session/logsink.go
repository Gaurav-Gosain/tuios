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

	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/adrg/xdg"
)

const (
	daemonLogName = "tuios/daemon.log"

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
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" && serverscope.Name() == "" {
		return filepath.Join(dir, daemonLogName)
	}
	return filepath.Join(xdg.StateHome, daemonLogName)
}

type ringWriter struct {
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

	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(&ringWriter{echo: echo})

	if foreground {
		SetDebugOutput(os.Stderr)
	} else {
		SetDebugOutput(io.Discard)
	}

	openDaemonLogFile(path)
}

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

func alwaysFile(level DebugLevel) bool {
	return level <= DebugBasic
}

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
