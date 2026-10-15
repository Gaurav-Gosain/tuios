//go:build !windows

package federation

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
)

// SharedMasterDir is where a client that can ask the person for a secret
// keeps the ssh master connections it opened: tuios-gpui opens the first
// connection to a machine itself, with its passphrase, password or second
// factor prompts in its own window, as a master socket in this folder. The
// daemon's links never prompt (BatchMode), so this is how a machine that
// needs a password or a code is reached at all, and no secret is stored.
//
// It is the folder "cm" beside the daemon's socket, as the stash store's
// folder is: <sockpath.Dir>/cm, which is $XDG_RUNTIME_DIR/tuios/cm (found at
// /run/user/<uid>/tuios/cm when the variable is unset), or /tmp/tuios-<uid>/cm,
// and <that folder>/servers/<name>/cm for a named server. Each master
// is named by ssh's %C, a hash of the local host, the remote host, the port
// and the user, so the client and the daemon name the same socket for the
// same destination without agreeing on more.
func SharedMasterDir() string {
	return filepath.Join(sockpath.Dir(), "cm")
}

// reuseOptions make ssh use a live master in SharedMasterDir, or are nil
// when the link must not:
//
//   - A socket path in the folder would be too long. ssh refuses a
//     ControlPath that does not fit sun_path and exits, so every link would
//     fail.
//   - The folder or its parent is not the user's alone, as ensureSocketDir
//     in internal/session demands of the socket folder. Whoever can write
//     there can put a socket that is not the person's connection in place.
//   - The person's ssh config gives the host a ControlPath of its own.
//     Options on the command line win over the config, so these would take
//     the link off the master the person opens in a terminal.
//   - The host forwards the agent. A connection through a master gets the
//     master's forwarding, not the link's, so the agent the link forwards
//     (the daemon's per-host agent link among them) would silently stop.
//   - ssh -G cannot answer, so neither of the last two is known.
func reuseOptions(h Host) []string {
	dir := SharedMasterDir()
	if len(dir)+1+masterNameLen > sockpath.MaxLen() {
		return nil
	}
	if !privateDir(filepath.Dir(dir)) || !privateDir(dir) {
		return nil
	}
	c, ok := ReadSSHConfig(h)
	if !ok {
		return nil
	}
	if c.ControlPath != "" && c.ControlPath != "none" {
		return nil
	}
	if c.ForwardAgent != "" && !strings.EqualFold(c.ForwardAgent, "no") {
		return nil
	}
	return reuseArgs(dir)
}

// freshOptions make ssh open a connection of its own, past any master the
// person's ssh config or SharedMasterDir names. The link uses them for one
// dial after its heartbeat found the connection dead.
//
// A master outlives the path under it. After a laptop sleeps, the master
// process still runs and still answers on its socket, so a new ssh that rides
// it waits on a TCP connection that is gone. The link's ServerAlive options
// do not reach the master, so nothing ends that wait until the master's own
// keepalives do, which can take many minutes or never happen.
//
// ControlPath=none is not used on every dial. A host that needs a password
// or a second factor is reached through a master the person opened, because
// the link runs ssh with BatchMode and cannot prompt. For that host the fresh
// dial fails at once with ssh's exit code, and the next dial rides the master
// again.
func freshOptions() []string {
	return []string{"-o", "ControlPath=none"}
}

// privateDir reports whether path is a directory, not a symbolic link,
// owned by this user and closed to everyone else.
func privateDir(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsDir() {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(sys.Uid) != os.Getuid() {
		return false
	}
	return st.Mode().Perm()&0o077 == 0
}
