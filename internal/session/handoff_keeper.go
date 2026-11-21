package session

import (
	"os"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

const handoffKeptEnv = "TUIOS_HANDOFF_KEPT"

const (
	keeperConnFD    = 3
	keeperFirstHeld = 4
)

func keeperBinary(prev *prevBinary, hold []int) string {
	if prev.fd() < 0 {
		return prev.path
	}
	return "/proc/self/fd/" + strconv.Itoa(keeperFirstHeld+len(hold)-1)
}

const keeperCommand = "handoff-keeper"

type handoffKeeperRecord struct {
	FD  int `json:"fd"`
	PID int `json:"pid"`
}

type handoffKeeper struct {
	pid  int
	conn *os.File
}

func (d *Daemon) setKeeper(k *handoffKeeper) {
	d.keeperMu.Lock()
	d.keeper = k
	d.keeperMu.Unlock()
}

func (d *Daemon) keeperRecord() handoffKeeperRecord {
	d.keeperMu.Lock()
	defer d.keeperMu.Unlock()
	if d.keeper == nil {
		return handoffKeeperRecord{FD: -1}
	}
	return handoffKeeperRecord{FD: fdNum(d.keeper.conn), PID: d.keeper.pid}
}

func (d *Daemon) releaseKeeper() {
	d.keeperMu.Lock()
	k := d.keeper
	d.keeper = nil
	d.keeperMu.Unlock()
	if k == nil {
		return
	}
	_, _ = k.conn.Write([]byte{1})
	_ = k.conn.Close()
	go reapKeeper(k.pid)
}

func reapKeeper(pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if _, err := p.Wait(); err != nil {
		debugLog("[DEBUG] Live upgrade: reaping the keeper %d: %v", pid, err)
	}
}

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
			return nil, errfmt.Errorf("%s holds %q, which is not a descriptor map", handoffKeptEnv, v)
		}
		out[a] = b
	}
	return out, nil
}

func formatKept(hold []int, first int) string {
	parts := make([]string, len(hold))
	for i, fd := range hold {
		parts[i] = strconv.Itoa(fd) + ":" + strconv.Itoa(first+i)
	}
	return strings.Join(parts, ",")
}

func keptAt(kept map[int]int, fd int) (int, error) {
	if kept == nil || fd < 0 {
		return fd, nil
	}
	to, ok := kept[fd]
	if !ok {
		return -1, errfmt.Errorf("the keeper holds no copy of descriptor %d", fd)
	}
	return to, nil
}

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
			return nil, errfmt.Errorf("%q is not a descriptor", f)
		}
		out = append(out, fd)
	}
	return out, nil
}
