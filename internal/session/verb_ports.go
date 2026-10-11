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
)

// list-ports: the TCP ports the processes in each pane listen on, so the GUI
// can offer to open a dev server a pane started.
//
// A pane's processes are its program and every process under it. The listing
// is read from the system each time it is older than portsCacheTTL: on Linux
// from /proc (ports_linux.go), on macOS from lsof (ports_darwin.go). Elsewhere
// the answer is empty with supported false.

// portsCacheTTL is how long one reading of the system answers list-ports.
const portsCacheTTL = time.Second

// portsTreeMax bounds how many processes one pane's tree may hold. A pane
// running a build can fork hundreds; past this the rest are not looked at.
const portsTreeMax = 1024

// paneProc names the pane a process is in.
type paneProc struct {
	session, window, worktree string
}

// listenHit is one listening socket of one process.
type listenHit struct {
	pid  int
	port int
	addr string
	comm string
}

// portRow is one row of a list-ports answer.
type portRow struct {
	Session  string `json:"session"`
	Window   string `json:"window"`
	Port     int    `json:"port"`
	Addr     string `json:"addr"`
	PID      int    `json:"pid"`
	Comm     string `json:"comm"`
	Worktree string `json:"worktree"`
}

// portsCache holds the last reading for portsCacheTTL. The lock is held
// through a reading, so callers that arrive together share one.
type portsCache struct {
	mu   sync.Mutex
	at   time.Time
	rows []portRow
}

func (d *Daemon) verbListPorts(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	only := ""
	if p.Session != "" {
		sess, verr := d.resolveVerbSession(p.Session)
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

// readPorts returns every pane's listening ports, read at most once per
// portsCacheTTL.
func (d *Daemon) readPorts() []portRow {
	c := &d.ports
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < portsCacheTTL {
		return c.rows
	}
	// Each pane's program, by pid, and the pane it is in.
	roots := map[int]paneProc{}
	for _, sess := range d.manager.AllSessions() {
		wt := ""
		if info := sess.Worktree(); info != nil {
			wt = info.Path
		}
		for _, w := range sess.GetState().Windows {
			if w.Host != "" {
				// The process runs on another machine.
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
	slices.SortFunc(rows, func(a, b portRow) int {
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

// walkProcTree maps every process under each root to the root's pane, reading
// children with children. A process is placed in the first pane that reaches
// it.
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

// procNetListen is one LISTEN socket of /proc/net/tcp or /proc/net/tcp6.
type procNetListen struct {
	addr  string
	port  int
	inode uint64
}

// tcpListen is the state /proc/net/tcp writes for a listening socket.
const tcpListen = "0A"

// parseProcNetTCP reads the lines of /proc/net/tcp or /proc/net/tcp6 and
// returns the sockets in LISTEN state. A line it cannot read is skipped.
//
// A line is "sl local_address rem_address st ... inode ...", with the local
// address as hex words in the host's byte order and the port as big-endian
// hex: "0100007F:1F90" is 127.0.0.1:8080 on a little-endian machine.
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

// procNetAddr decodes /proc/net/tcp's address: 8 hex digits for IPv4, 32 for
// IPv6, each 32-bit word in little-endian order. Every machine tuios runs on
// is little-endian.
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
	// An IPv4 address a v6 socket also takes reads as itself.
	return ip.Unmap().String(), true
}
