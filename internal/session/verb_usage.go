package session

import (
	"encoding/json"
	"runtime"
	"sync"
	"time"
)

// pane-usage: the CPU and memory of the processes in each pane, and the
// machine's load, for a client that draws them. A pane's processes are its
// program and every process under it, as list-ports walks them.
//
// Nothing samples on its own: a reading is taken when a client asks, and a
// client asks only while it shows the numbers. CPU is the share of one core
// the pane's processes used since the reading before, so the first reading
// after a pause reads from that one. Readings closer than usageMinGap are
// answered from the last one.

// usageMinGap is how close two readings may be before the second reuses the
// first.
const usageMinGap = 400 * time.Millisecond

// procStat is one process's CPU time so far and its resident memory.
type procStat struct {
	cpu time.Duration
	rss int64
	pct float64 // set where the system reports a share itself (ps)
}

// usageRow is one pane of a pane-usage answer.
type usageRow struct {
	Session string  `json:"session"`
	Window  string  `json:"window"`
	PID     int     `json:"pid"`
	CPU     float64 `json:"cpu_pct"`
	RSS     int64   `json:"rss"`
	Procs   int     `json:"procs"`
}

// usageCache is the last reading, and the CPU times it diffs against.
type usageCache struct {
	mu   sync.Mutex
	at   time.Time
	cpu  map[int]time.Duration
	rows []usageRow
}

func (d *Daemon) verbPaneUsage(cs *connState, params json.RawMessage) (any, *verbError) {
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
	rows := make([]usageRow, 0)
	for _, r := range d.readUsage() {
		if only == "" || r.Session == only {
			rows = append(rows, r)
		}
	}
	load1, memPct, ok := readMachineLoad()
	return map[string]any{
		"supported": usageSupported,
		"panes":     rows,
		"machine":   map[string]any{"cores": runtime.NumCPU(), "load1": load1, "mem_pct": memPct, "load_ok": ok},
	}, nil
}

// readUsage reads every local pane's processes, at most once per usageMinGap.
func (d *Daemon) readUsage() []usageRow {
	c := &d.usage
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if !usageSupported || !c.at.IsZero() && now.Sub(c.at) < usageMinGap {
		return c.rows
	}
	roots := map[int]paneProc{}
	for _, sess := range d.manager.AllSessions() {
		for _, w := range sess.GetState().Windows {
			if w.Host != "" {
				continue
			}
			if pty := sess.GetPTY(w.PTYID); pty != nil {
				if pid := pty.ShellPID(); pid > 0 {
					roots[pid] = paneProc{session: sess.Name(), window: w.ID}
				}
			}
		}
	}
	owner, stats := procUsage(roots)
	byPane := map[paneProc]*usageRow{}
	for pid, pp := range roots {
		byPane[pp] = &usageRow{Session: pp.session, Window: pp.window, PID: pid}
	}
	cpu := make(map[int]time.Duration, len(stats))
	gap := now.Sub(c.at).Seconds()
	for pid, pp := range owner {
		r, st := byPane[pp], stats[pid]
		if r == nil || st == nil {
			continue
		}
		r.Procs++
		r.RSS += st.rss
		cpu[pid] = st.cpu
		switch {
		case st.pct > 0:
			r.CPU += st.pct
		case !c.at.IsZero() && gap > 0:
			if prev, ok := c.cpu[pid]; ok && st.cpu >= prev {
				r.CPU += 100 * (st.cpu - prev).Seconds() / gap
			}
		}
	}
	rows := make([]usageRow, 0, len(byPane))
	for _, r := range byPane {
		rows = append(rows, *r)
	}
	c.at, c.cpu, c.rows = now, cpu, rows
	return rows
}
