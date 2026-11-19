// Package panescope puts each pane of a daemon that runs as a systemd user
// service in a transient scope unit of its own.
//
// As a service, every process the daemon starts lives in the daemon's unit.
// When the kernel's OOM killer takes one of them, systemd's default
// OOMPolicy=stop then stops the whole unit: one pane's runaway grep takes
// the daemon and every other pane with it. install-service writes
// OOMPolicy=continue for that, and this package goes further: each pane's
// process tree gets a scope of its own (tuios-pane-<pid>.scope, in the
// daemon's slice), so its memory is counted and its OOM handled on its own.
// The scope has OOMPolicy=continue too, so an OOM kill takes the one process
// and the pane's shell stays.
//
// The scope is made with one StartTransientUnit call to the user manager,
// over its private socket ($XDG_RUNTIME_DIR/systemd/private), else the user
// bus. A small D-Bus client is written out here, so no D-Bus library is
// linked in. The call runs in its own goroutine after the pane's process
// started, so a pane does not start later for it; a process the shell forks
// in the first moment stays in the daemon's unit.
//
// It does nothing when the daemon is not a service of a user manager (its
// cgroup is not a .service under user@), or on a system without one. The
// first failure is logged and turns it off for the daemon's life, so a
// machine without a user manager pays one failed connect.
package panescope

import "sync/atomic"

// off is set after the first failure.
var off atomic.Bool

// Place puts the process pid, a pane's first process, in a scope of its own
// when the daemon runs as a systemd user service. It returns at once. logf
// gets one line for the first scope made and one for the first failure.
func Place(pid int, logf func(string, ...any)) {
	if pid <= 1 || off.Load() {
		return
	}
	slice, ok := serviceSlice()
	if !ok {
		return
	}
	go place(pid, slice, logf)
}
