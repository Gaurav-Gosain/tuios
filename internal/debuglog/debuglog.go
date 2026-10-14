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
	"sync"
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

// enabled reports whether TUIOS_DEBUG_INTERNAL=1 is set, read once on first
// use. The switch is set at startup, before any pane exists.
var enabled = sync.OnceValue(func() bool {
	return os.Getenv("TUIOS_DEBUG_INTERNAL") == "1"
})

// Internal is a logger that appends each line to Path, with a timestamp, when
// TUIOS_DEBUG_INTERNAL=1. Otherwise it does nothing.
type Internal struct{}

// Printf appends one formatted line to Path.
func (Internal) Printf(format string, v ...any) {
	if !enabled() {
		return
	}
	f, err := Open(Path)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, v...))
	_ = f.Close()
}
