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

var livePrimary sync.Map

var liveOther sync.Map

var otherDaemonWarned sync.Once

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
	if !sockpath.PrivateDir(otherDir, os.Getuid()) {
		return primary
	}
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

const otherDaemonMark = "other-daemon-warned"

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
