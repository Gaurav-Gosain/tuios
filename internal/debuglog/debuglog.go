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

import "os"

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
