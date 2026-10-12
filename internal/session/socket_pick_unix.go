//go:build !windows

package session

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
	"golang.org/x/term"
)

// One user on one machine reaches one daemon. sockpath.Dir picks the same
// folder whether or not XDG_RUNTIME_DIR is set, but a daemon an older tuios
// started over ssh still listens in /tmp/tuios-<uid>, and a client that went
// only by the folder would start a second daemon beside it. So before the
// socket in Dir is used, the other folder is looked at:
//
//   - the daemon in Dir answers: it wins. When the other one answers too, a
//     person at a terminal is told once, with the way to stop it.
//   - only the other one answers: it is used, so its sessions stay reachable
//     until it is stopped. The next daemon then starts in Dir.
//   - neither answers: Dir, where a new daemon starts.
//
// One exception: TUIOS_SOCKET names the other socket and a daemon answers
// there. The command runs in a pane of that daemon and stays with it.

// livePrimary remembers the sockets in Dir found answering, so a process
// that resolves the path many times dials the other folder once.
var livePrimary sync.Map

// liveOther remembers the other socket found answering for a pane of its
// daemon. See pickLiveSocket.
var liveOther sync.Map

// otherDaemonWarned makes the warning once per process.
var otherDaemonWarned sync.Once

// pickLiveSocket returns the socket a client should use, given primary, the
// socket in sockpath.Dir.
func pickLiveSocket(primary string) string {
	otherDir := sockpath.OtherDir()
	if otherDir == "" {
		return primary
	}
	other := filepath.Join(otherDir, "tuios.sock")
	if filepath.Clean(other) == filepath.Clean(primary) {
		return primary
	}
	if _, err := os.Lstat(other); err != nil {
		return primary
	}
	// The same checks ensureSocketDir makes on its own folder, without
	// creating or changing anything: a socket in a folder someone else
	// controls is never dialled.
	if !sockpath.PrivateDir(otherDir, os.Getuid()) {
		return primary
	}
	// A pane exports the socket of the daemon that runs it. A command in a
	// pane of the daemon in the other folder belongs to that daemon, even
	// when the one in Dir runs too: its session, its hooks, its notify.
	if env := os.Getenv(SocketEnv); env != "" && filepath.Clean(env) == filepath.Clean(other) {
		if _, ok := liveOther.Load(other); ok || isDaemonRunningAt(other) {
			liveOther.Store(other, true)
			return other
		}
	}
	if _, ok := livePrimary.Load(primary); ok {
		return primary
	}
	if isDaemonRunningAt(primary) {
		livePrimary.Store(primary, true)
		otherDaemonWarned.Do(func() { warnOtherDaemon(primary, other) })
		return primary
	}
	if isDaemonRunningAt(other) {
		return other
	}
	return primary
}

// otherDaemonMark is the file, beside the winning socket, that records which
// other daemon a person was already told about.
const otherDaemonMark = "other-daemon-warned"

// warnOtherDaemon tells a person at a terminal, once per other daemon, that a
// second daemon runs and its sessions do not show here. A process with no
// terminal on stderr (stdio-proxy over ssh) says nothing, and the daemon only
// logs it, so neither can use up the one warning.
func warnOtherDaemon(primary, other string) {
	if !isDaemonRunningAt(other) {
		return
	}
	pid := 0
	if data, err := os.ReadFile(other + ".pid"); err == nil {
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}
	msg := otherDaemonMessage(primary, other, pid)
	if daemonLoggingInstalled.Load() {
		// Only the daemon's log: in any other process the standard logger
		// writes to stderr, and the warning would then show on every run.
		log.Print("Warning: " + msg)
		return
	}
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return
	}
	mark := filepath.Join(filepath.Dir(primary), otherDaemonMark)
	seen := fmt.Sprintf("%s %d", other, pid)
	if data, err := os.ReadFile(mark); err == nil && string(data) == seen {
		return
	}
	fmt.Fprintln(os.Stderr, "Warning: "+msg)
	_ = os.WriteFile(mark, []byte(seen), 0o600)
}

// otherDaemonMessage is the warning text.
func otherDaemonMessage(primary, other string, pid int) string {
	stop := "stop the other daemon"
	if pid > 0 {
		stop = fmt.Sprintf("run \"kill %d\" to stop the other daemon", pid)
	}
	return fmt.Sprintf("A second tuios daemon runs for this user at %s. Its sessions do not show here. "+
		"This command uses the daemon at %s. To move the other sessions here, %s. "+
		"That daemon saves its sessions when it stops. Then run \"tuios resurrect\" to restore them. "+
		"See \"Two daemons on one machine\" in docs/CLI_REFERENCE.md.",
		other, primary, stop)
}
