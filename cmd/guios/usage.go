package main

import (
	"math"
	"slices"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
)

// Resource views: the CPU and memory of each pane's processes, the
// machine's load and the load of linked hosts, as small charts in the
// agents view (the selected agent's pane) and in the sidebar's usage panel.
//
// The numbers come from the daemon's pane-usage verb, which reads the
// system only when asked, and from list-hosts for the hosts. A sampler
// asks only while a view that shows them is open: the agents view or the
// usage panel. It asks once a second while the numbers move, and backs off
// to once every usageMaxGap while they do not; a daemon event (a pane
// starts or ends a command, an agent changes state) brings it back to once
// a second. It hands
// the window a new reading only when a figure the views show changes, as
// they round it; a chart that only moved on waits for that. A still
// machine costs no frames.

const (
	// usageHistory is how many readings a chart shows.
	usageHistory = 60
	// usageGap is the time between readings while the numbers move, and
	// usageMaxGap the longest the back-off goes to.
	usageGap    = time.Second
	usageMaxGap = 30 * time.Second
	// usageQuiet is how many still readings start the back-off.
	usageQuiet = 3
	// usageHostsEvery is how often the hosts' loads are read: their
	// daemons send them on the link's heartbeat, every 15 s.
	usageHostsEvery = 15 * time.Second
)

// series is a chart's last readings, oldest first.
type series []float32

// push adds v, dropping the oldest past usageHistory.
func (s series) push(v float32) series {
	if len(s) >= usageHistory {
		s = append(s[:0:0], s[len(s)-usageHistory+1:]...)
	}
	return append(s, v)
}

// paneUsage is one pane's readings.
type paneUsage struct {
	cpu, mem series
	cpuNow   float64 // percent of one core
	rss      int64   // bytes
	procs    int
}

// hostLoad is one linked host's load.
type hostLoad struct {
	name          string
	cores         int
	load1, memPct float64
	ok, connected bool
	working       int
}

// usageView is one reading as the views draw it. The sampler builds a new
// one for each change and the window takes it whole, so the views never
// see one half built.
type usageView struct {
	supported bool
	at        time.Time
	panes     map[string]*paneUsage // by session name and window id
	cores     int
	load1     float64
	memPct    float64
	loadOK    bool
	load, mem series // the machine's load in percent of its cores, and memory in use
	hosts     []hostLoad
}

// usageKey names a pane in usageView.panes.
func usageKey(session, window string) string { return session + "\x00" + window }

// usageSampler is the sampler's state shared with the window.
type usageSampler struct {
	want    atomic.Bool // a view that shows the numbers is open
	running atomic.Bool
	wake    chan struct{}
	// cl is this machine's client, as the window last saw it.
	cl atomic.Pointer[client.Client]
}

// usageWanted reports whether a view that shows the numbers is open.
func (a *App) usageWanted() bool { return a.usageOpen || a.ck.open }

// syncUsage starts the sampler when a view that shows the numbers opened,
// and lets it stop when none is open. The view calls it on every frame;
// it changes nothing while nothing opened or closed.
func (a *App) syncUsage() {
	want := a.usageWanted()
	was := a.sampler.want.Swap(want)
	a.sampler.cl.Store(a.cockpitClient())
	if a.sampler.wake == nil {
		a.sampler.wake = make(chan struct{}, 1)
	}
	switch {
	case want && a.sampler.running.CompareAndSwap(false, true):
		go a.usageLoop(a.sampler.wake)
	case want && !was:
		// A view opened while the sampler waits out its back-off.
		a.nudgeUsage()
	}
}

// nudgeUsage brings the sampler back to a reading a second, as after a
// daemon event.
func (a *App) nudgeUsage() {
	if w := a.sampler.wake; w != nil {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// usageLoop reads until no view wants the numbers.
func (a *App) usageLoop(wake chan struct{}) {
	defer a.sampler.running.Store(false)
	// last is the last reading, which the charts go on from, and drawn the
	// last one the window took, which a new one is compared with.
	var last, drawn *usageView
	gap, quiet := usageGap, 0
	var hostsAt time.Time
	var hosts []hostLoad
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
		case <-wake:
			gap, quiet = usageGap, 0
			timer.Stop()
		}
		if !a.sampler.want.Load() {
			return
		}
		cl := a.sampler.cl.Load()
		if cl == nil {
			timer.Reset(usageMaxGap)
			continue
		}
		if time.Since(hostsAt) >= usageHostsEvery {
			hostsAt, hosts = time.Now(), readHostLoads(cl)
		}
		next := readUsage(cl, last, hosts)
		if next != nil {
			draw, busy := usageChanged(drawn, next)
			if draw {
				v := next
				drawn = next
				a.update(func() { a.usage = v })
			}
			// The pace follows the panes: the machine's load is a minute's
			// average, which a reading every usageMaxGap shows well enough.
			if busy {
				quiet = 0
			} else {
				quiet++
			}
			last = next
		}
		if quiet >= usageQuiet {
			gap = min(gap*2, usageMaxGap)
		} else {
			gap = usageGap
		}
		timer.Reset(gap)
	}
}

// paneUsageReply is a pane-usage answer.
type paneUsageReply struct {
	Supported bool `json:"supported"`
	Panes     []struct {
		Session string  `json:"session"`
		Window  string  `json:"window"`
		CPU     float64 `json:"cpu_pct"`
		RSS     int64   `json:"rss"`
		Procs   int     `json:"procs"`
	} `json:"panes"`
	Machine struct {
		Cores  int     `json:"cores"`
		Load1  float64 `json:"load1"`
		MemPct float64 `json:"mem_pct"`
		LoadOK bool    `json:"load_ok"`
	} `json:"machine"`
}

// readUsage takes a reading and adds it to the charts of last. nil when the
// daemon did not answer: it may predate pane-usage.
func readUsage(cl *client.Client, last *usageView, hosts []hostLoad) *usageView {
	var r paneUsageReply
	if err := cl.VerbInto("pane-usage", nil, false, &r); err != nil {
		return nil
	}
	v := &usageView{
		supported: r.Supported, at: time.Now(), panes: make(map[string]*paneUsage, len(r.Panes)),
		cores: r.Machine.Cores, load1: r.Machine.Load1, memPct: r.Machine.MemPct, loadOK: r.Machine.LoadOK,
		hosts: hosts,
	}
	if last != nil {
		v.load, v.mem = last.load, last.mem
	}
	if v.loadOK && v.cores > 0 {
		v.load = v.load.push(float32(loadPct(v)))
	}
	v.mem = v.mem.push(float32(math.Round(v.memPct)))
	for _, p := range r.Panes {
		k := usageKey(p.Session, p.Window)
		pu := &paneUsage{cpuNow: p.CPU, rss: p.RSS, procs: p.Procs}
		if last != nil {
			if old := last.panes[k]; old != nil {
				pu.cpu, pu.mem = old.cpu, old.mem
			}
		}
		pu.cpu = pu.cpu.push(float32(math.Round(p.CPU*10) / 10))
		pu.mem = pu.mem.push(float32(p.RSS >> 20))
		v.panes[k] = pu
	}
	return v
}

// readHostLoads reads the linked hosts' loads from list-hosts.
func readHostLoads(cl *client.Client) []hostLoad {
	var r struct {
		Hosts []struct {
			Host    string  `json:"host"`
			Status  string  `json:"status"`
			Cores   int     `json:"cores"`
			Load1   float64 `json:"load1"`
			MemPct  float64 `json:"mem_pct"`
			LoadOK  *bool   `json:"load_ok"`
			Working int     `json:"agents_working"`
		} `json:"hosts"`
	}
	if cl.VerbInto("list-hosts", nil, false, &r) != nil {
		return nil
	}
	out := make([]hostLoad, 0, len(r.Hosts))
	for _, h := range r.Hosts {
		ok := h.Cores > 0 && (h.LoadOK == nil || *h.LoadOK)
		out = append(out, hostLoad{name: h.Host, cores: h.Cores, load1: h.Load1, memPct: h.MemPct, ok: ok,
			connected: h.Status == "connected", working: h.Working})
	}
	return out
}

// usageChanged reports whether next shows a figure other than last does, as
// the views round them (draw), and whether a pane's did (busy). A chart
// that only moved on is not drawn again until a figure changes.
func usageChanged(last, next *usageView) (draw, busy bool) {
	if last == nil || last.supported != next.supported || len(last.panes) != len(next.panes) {
		return true, true
	}
	for k, p := range next.panes {
		o := last.panes[k]
		if o == nil || cpuText(o.cpuNow) != cpuText(p.cpuNow) || bytesText(o.rss) != bytesText(p.rss) || o.procs != p.procs {
			return true, true
		}
	}
	// The machine's load moves a point either way all the time on a busy
	// machine: two points make a change worth a frame.
	if iabs(loadPct(last)-loadPct(next)) >= 2 || math.Round(last.memPct) != math.Round(next.memPct) || !slices.Equal(last.hosts, next.hosts) {
		return true, false
	}
	return false, false
}

func iabs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// loadPct is the machine's load in whole percent of its cores.
func loadPct(v *usageView) int {
	return int(math.Round(100 * v.load1 / float64(max(v.cores, 1))))
}

// cpuText is a CPU share as the views show it: one decimal below 10 %.
func cpuText(pct float64) string {
	if pct < 9.95 {
		return trimZero(math.Round(pct*10)/10) + "%"
	}
	return itoa(int(math.Round(pct))) + "%"
}

// bytesText is a memory size in binary units, as the views show it.
func bytesText(b int64) string {
	const k = 1024
	switch {
	case b >= k*k*k:
		return trimZero(math.Round(float64(b)/(k*k*k)*10)/10) + " GB"
	case b >= k*k:
		return itoa(int(math.Round(float64(b)/(k*k)))) + " MB"
	default:
		return itoa(int(math.Round(float64(b)/k))) + " KB"
	}
}

// trimZero is v with one decimal, without a trailing ".0".
func trimZero(v float64) string {
	i := int(v)
	d := int(math.Round((v - float64(i)) * 10))
	if d == 10 {
		i, d = i+1, 0
	}
	if d == 0 {
		return itoa(i)
	}
	return itoa(i) + "." + itoa(d)
}
