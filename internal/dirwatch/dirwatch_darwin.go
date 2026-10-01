package dirwatch

import "golang.org/x/sys/unix"

// openMode opens the directory for events only, so the watch does not hold a
// volume busy and an eject is not refused because of it.
const openMode = unix.O_EVTONLY
