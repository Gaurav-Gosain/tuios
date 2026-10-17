package session

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The keeper of a live handoff.
//
// The exec is the commit point, and after it the new image is the only
// process holding the panes' masters. An image that dies hard before it is
// up (a fatal runtime error, a panic in a goroutine no recover covers, the
// out-of-memory killer, SIGKILL) would take every master with it, and every
// pane's program would get its hangup, as in a daemon crash. The fallback of
// handoff_fallback.go cannot help there: it is code in the image that died.
//
// So before the panes stop, the old image starts a keeper: a small process of
// the previous binary that inherits a copy of every descriptor the exec
// carries (the manifest, the masters, the sockets, the start lock) and one end
// of a socket pair. The daemon holds the other end, across the exec and across
// the fallback's exec. The image that is up writes one byte on it, and the
// keeper exits. If the daemon's end closes with no byte, every copy of it is
// gone, so the daemon's process is gone, and the keeper execs the previous
// binary in its own place with the descriptors it holds. That image adopts the
// panes from the manifest as a fallback does, as a daemon with a new pid. See
// docs/design/live-handoff.md, section 7, for each way this can end.

// handoffKeptEnv is set by a keeper on the image it execs. It maps each
// descriptor number the manifest names to the number the keeper holds it at.
const handoffKeptEnv = "TUIOS_HANDOFF_KEPT"

// keeperFirstHeld is the first descriptor a keeper holds the carried ones at:
// after stdin, stdout, stderr and its end of the socket.
const (
	keeperConnFD    = 3
	keeperFirstHeld = 4
)

// keeperBinary is the path the keeper runs the previous binary by, and
// restores the daemon with. On Linux that binary is a descriptor
// (handoff_prev.go), the last one in hold, which the keeper holds at
// keeperFirstHeld+len(hold)-1: the daemon's /proc/self/fd/N names another
// file in the keeper.
func keeperBinary(prev *prevBinary, hold []int) string {
	if prev.fd() < 0 {
		return prev.path
	}
	return "/proc/self/fd/" + strconv.Itoa(keeperFirstHeld+len(hold)-1)
}

// keeperCommand is the daemon subcommand a keeper runs as.
const keeperCommand = "handoff-keeper"

// handoffKeeperRecord is the keeper as the manifest names it: its process and
// the daemon's end of the socket to it. PID 0 is a handoff with no keeper.
type handoffKeeperRecord struct {
	FD  int `json:"fd"`
	PID int `json:"pid"`
}

// handoffKeeper is a running keeper, from the daemon's side.
type handoffKeeper struct {
	pid  int
	conn *os.File
}

// setKeeper records the keeper this image answers to.
func (d *Daemon) setKeeper(k *handoffKeeper) {
	d.keeperMu.Lock()
	d.keeper = k
	d.keeperMu.Unlock()
}

// keeperRecord is the keeper for the manifest, and the descriptor the exec
// must keep for it.
func (d *Daemon) keeperRecord() handoffKeeperRecord {
	d.keeperMu.Lock()
	defer d.keeperMu.Unlock()
	if d.keeper == nil {
		return handoffKeeperRecord{FD: -1}
	}
	return handoffKeeperRecord{FD: int(d.keeper.conn.Fd()), PID: d.keeper.pid} //nolint:gosec // a descriptor number
}

// releaseKeeper tells the keeper to stand down, and reaps it once it has. The
// image that is up calls it, and so do a handoff put back and a shutdown, so a
// daemon stopped on purpose is not brought back.
func (d *Daemon) releaseKeeper() {
	d.keeperMu.Lock()
	k := d.keeper
	d.keeper = nil
	d.keeperMu.Unlock()
	if k == nil {
		return
	}
	// A keeper that died already makes this write fail, which changes
	// nothing: its job was to outlive this image, not the other way round.
	_, _ = k.conn.Write([]byte{1})
	_ = k.conn.Close()
	go reapKeeper(k.pid)
}

// reapKeeper waits for the keeper to exit. It is this pid's child, also after
// the exec, and nothing else waits for it.
func reapKeeper(pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if _, err := p.Wait(); err != nil {
		debugLog("[DEBUG] Live upgrade: reaping the keeper %d: %v", pid, err)
	}
}

// adoptKeeper takes over the keeper the manifest names, so this image can
// release it once it is up. The descriptor stays open until then: closing it
// is what tells the keeper this image is gone.
func (d *Daemon) adoptKeeper(rec handoffKeeperRecord, fb *adoptFallback) {
	if rec.PID <= 0 || rec.FD < 0 {
		return
	}
	f := adoptedFile(rec.FD, "handoff-keeper")
	if f == nil {
		return
	}
	fb.keep(f)
	d.setKeeper(&handoffKeeper{pid: rec.PID, conn: f})
}

// keptDescriptors reads the map a keeper leaves in handoffKeptEnv, nil when
// this image was not started by one.
func keptDescriptors() (map[int]int, error) {
	v := os.Getenv(handoffKeptEnv)
	if v == "" {
		return nil, nil
	}
	out := map[int]int{}
	for pair := range strings.SplitSeq(v, ",") {
		from, to, ok := strings.Cut(pair, ":")
		a, errA := strconv.Atoi(from)
		b, errB := strconv.Atoi(to)
		if !ok || errA != nil || errB != nil {
			return nil, fmt.Errorf("%s holds %q, which is not a descriptor map", handoffKeptEnv, v)
		}
		out[a] = b
	}
	return out, nil
}

// formatKept is the value of handoffKeptEnv for descriptors held at
// first+i, each where the manifest names hold[i].
func formatKept(hold []int, first int) string {
	parts := make([]string, len(hold))
	for i, fd := range hold {
		parts[i] = strconv.Itoa(fd) + ":" + strconv.Itoa(first+i)
	}
	return strings.Join(parts, ",")
}

// keptAt is fd as the keeper holds it.
func keptAt(kept map[int]int, fd int) (int, error) {
	if kept == nil || fd < 0 {
		return fd, nil
	}
	to, ok := kept[fd]
	if !ok {
		return -1, fmt.Errorf("the keeper holds no copy of descriptor %d", fd)
	}
	return to, nil
}

// remapKept rewrites every descriptor the manifest names to where the keeper
// holds it. The keeper is this process now, so there is none to answer.
func (m *handoffManifest) remapKept(kept map[int]int) error {
	if kept == nil {
		return nil
	}
	var err error
	for name, fd := range m.Daemon.Listeners {
		if m.Daemon.Listeners[name], err = keptAt(kept, fd); err != nil {
			return err
		}
	}
	if m.Daemon.LockFD, err = keptAt(kept, m.Daemon.LockFD); err != nil {
		return err
	}
	if m.Daemon.FallbackFD, err = keptAt(kept, m.Daemon.FallbackFD); err != nil {
		return err
	}
	for i := range m.Panes {
		if m.Panes[i].FD, err = keptAt(kept, m.Panes[i].FD); err != nil {
			return err
		}
	}
	m.Daemon.Keeper = handoffKeeperRecord{FD: -1}
	return nil
}

// ParseKeeperHold parses a keeper's --hold value: the descriptor numbers the
// manifest names, in the order the keeper holds them.
func ParseKeeperHold(s string) ([]int, error) {
	if s == "" {
		return nil, nil
	}
	var out []int
	for f := range strings.SplitSeq(s, ",") {
		fd, err := strconv.Atoi(f)
		if err != nil || fd < 0 {
			return nil, fmt.Errorf("%q is not a descriptor", f)
		}
		out = append(out, fd)
	}
	return out, nil
}
