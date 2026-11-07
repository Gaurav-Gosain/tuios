// Package debuglog opens the diagnostic logs that TUIOS_DEBUG_INTERNAL=1
// turns on.
//
// The logs sit at fixed names under /tmp so a developer knows where to look,
// and they carry what a pane printed and, for the events log, every key
// pressed. A fixed name in a shared directory is one another local user can
// create first, as a link to a file of the victim's or as a world-readable
// file of their own, so Open refuses both and keeps the file private. The
// render trace, whose name under the temp directory is just as predictable,
// opens through it too.
package debuglog

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

const (
	// Path is the general debug log.
	Path = "/tmp/tuios-debug.log"
	// EventsPath is the input event log, which records keystrokes.
	EventsPath = "/tmp/tuios-events.log"
)

// Open opens path for appending, creating it with mode 0600. The caller
// closes it.
func Open(path string) (*os.File, error) {
	return openPrivate(path)
}

// on is the internal debug switch. It starts from TUIOS_DEBUG_INTERNAL=1 in
// the environment, and Enable turns it on later, for a --debug flag that is
// read after the packages start.
var on atomic.Bool

func init() { on.Store(os.Getenv("TUIOS_DEBUG_INTERNAL") == "1") }

// Enabled reports whether the internal debug logs are on. It is the one
// place every package asks, so a flag that turns the logs on reaches all of
// them.
func Enabled() bool { return on.Load() }

// Enable turns the internal debug logs on for this process, and sets
// TUIOS_DEBUG_INTERNAL=1 so the processes it starts, such as the daemon,
// turn them on too.
func Enable() {
	on.Store(true)
	_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
}

// Internal is a logger that appends each line to Path, with a timestamp, when
// TUIOS_DEBUG_INTERNAL=1. Otherwise it does nothing.
type Internal struct{}

// Printf appends one formatted line to Path.
func (Internal) Printf(format string, v ...any) {
	if !Enabled() {
		return
	}
	f, err := Open(Path)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, v...))
	_ = f.Close()
}
