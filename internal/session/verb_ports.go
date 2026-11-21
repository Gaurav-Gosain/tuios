package session

import (
	"cmp"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

const portsCacheTTL = time.Second

const portsTreeMax = 1024

type paneProc struct {
	session, window, worktree string
}

type listenHit struct {
	pid  int
	port int
	addr string
	comm string
}

type portRow struct {
	Session  string `json:"session"`
	Window   string `json:"window"`
	Port     int    `json:"port"`
	Addr     string `json:"addr"`
	PID      int    `json:"pid"`
	Comm     string `json:"comm"`
	Worktree string `json:"worktree"`
}

type portsCache struct {
	mu   sync.Mutex
	at   time.Time
	rows []portRow
}

func (d *Daemon) verbListPorts(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	only := ""
	if p.Session != "" {
		sess, verr := d.resolveVerbSession(cs, p.Session)
		if verr != nil {
			return nil, verr
		}
		only = sess.Name()
	}
	rows := make([]portRow, 0)
	if portsSupported {
		for _, r := range d.readPorts() {
			if only == "" || r.Session == only {
				rows = append(rows, r)
			}
		}
	}
	return map[string]any{"supported": portsSupported, "ports": rows}, nil
}

func (d *Daemon) readPorts() []portRow {
	c := &d.ports
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < portsCacheTTL {
		return c.rows
	}
	roots := map[int]paneProc{}
	for _, sess := range d.manager.AllSessions() {
		wt := ""
		if info := sess.Worktree(); info != nil {
			wt = info.Path
		}
		for _, w := range sess.GetState().Windows {
			if w.Host != "" {
				continue
			}
			if pty := sess.GetPTY(w.PTYID); pty != nil {
				if pid := pty.ShellPID(); pid > 0 {
					roots[pid] = paneProc{session: sess.Name(), window: w.ID, worktree: wt}
				}
			}
		}
	}
	rows := make([]portRow, 0)
	if len(roots) > 0 {
		owner := paneTrees(roots)
		for _, h := range listeningSockets(owner) {
			pp := owner[h.pid]
			rows = append(rows, portRow{
				Session: pp.session, Window: pp.window, Worktree: pp.worktree,
				Port: h.port, Addr: h.addr, PID: h.pid, Comm: h.comm,
			})
		}
	}
	sortx.SortFunc(rows, func(a, b portRow) int {
		return cmp.Or(
			cmp.Compare(a.Session, b.Session),
			cmp.Compare(a.Window, b.Window),
			cmp.Compare(a.Port, b.Port),
			cmp.Compare(a.Addr, b.Addr),
			cmp.Compare(a.PID, b.PID),
		)
	})
	rows = slices.CompactFunc(rows, func(a, b portRow) bool { return a == b })
	c.at, c.rows = time.Now(), rows
	return rows
}

func walkProcTree(roots map[int]paneProc, children func(pid int) []int) map[int]paneProc {
	owner := make(map[int]paneProc, len(roots))
	for root, pp := range roots {
		if _, done := owner[root]; done {
			continue
		}
		owner[root] = pp
		queue := []int{root}
		count := 1
		for len(queue) > 0 && count < portsTreeMax {
			pid := queue[0]
			queue = queue[1:]
			for _, child := range children(pid) {
				if _, done := owner[child]; done {
					continue
				}
				owner[child] = pp
				queue = append(queue, child)
				count++
			}
		}
	}
	return owner
}

type procNetListen struct {
	addr  string
	port  int
	inode uint64
}

const tcpListen = "0A"

func parseProcNetTCP(data string) []procNetListen {
	var out []procNetListen
	for line := range strings.Lines(data) {
		f := strings.Fields(line)
		if len(f) < 10 || f[3] != tcpListen {
			continue
		}
		host, portHex, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil {
			continue
		}
		addr, ok := procNetAddr(host)
		if !ok {
			continue
		}
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil || inode == 0 {
			continue
		}
		out = append(out, procNetListen{addr: addr, port: int(port), inode: inode})
	}
	return out
}

func procNetAddr(h string) (string, bool) {
	raw, err := hex.DecodeString(h)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", false
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	ip, ok := netip.AddrFromSlice(raw)
	if !ok {
		return "", false
	}
	return ip.Unmap().String(), true
}
