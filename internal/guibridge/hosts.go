package guibridge

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Hosts is the "hosts" event.
type Hosts struct {
	// Live is false until the bridge holds a listing, and while the daemon
	// cannot be reached.
	Live  bool      `json:"live"`
	Hosts []HostRow `json:"hosts"`
}

// HostRow is one machine.
type HostRow struct {
	Name string `json:"name"`
	Addr string `json:"addr"`
	// Status is the link's own word (up, connecting, unreachable, ...), and
	// Reason the plain sentence that says why it is not up.
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Health is good, slow, stalled, reconnecting, signin or down.
	Health string `json:"health"`
	// RTTMs is the smoothed round trip, 0 before the first answer.
	RTTMs int `json:"rtt_ms,omitempty"`
	// SilentSince is when the probe that has had no answer went out, in Unix
	// milliseconds, while Health is stalled.
	SilentSince int64 `json:"silent_since,omitempty"`
	// ApprovalURL is the Tailscale sign-in page the link waits on.
	ApprovalURL string `json:"approval_url,omitempty"`
	// Sessions are the host's sessions, from the last answer.
	Sessions []HostSession `json:"sessions,omitempty"`
	detail   string
}

// HostSession is one session on a host.
type HostSession struct {
	Name       string `json:"name"`
	ID         string `json:"id,omitempty"`
	Windows    int    `json:"windows"`
	Attached   bool   `json:"attached,omitempty"`
	LastActive int64  `json:"last_active,omitempty"`
}

const (
	hostsPeriod  = time.Second
	probePeriod  = 5 * time.Second
	stallAfter   = 5 * time.Second
	probeTimeout = 30 * time.Second
	slowEnter    = 300.0
	slowLeave    = 200.0
)

type prober struct {
	host    string
	version string
	stop    chan struct{}
	poke    chan struct{}

	mu       sync.Mutex
	rtt      float64
	slow     bool
	out      time.Time
	sessions []HostSession
}

func (p *prober) run() {
	var c *session.VerbClient
	defer func() {
		if c != nil {
			_ = c.Close()
		}
	}()
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		start := time.Now()
		p.mu.Lock()
		p.out = start
		p.mu.Unlock()
		var raw []byte
		var err error
		if c == nil {
			c, _, err = session.DialVerbClientThroughHost(p.host, p.version)
			if err == nil {
				start = time.Now()
			}
		}
		if err == nil {
			raw, err = c.CallWithTimeout("list-sessions", nil, probeTimeout)
		}
		took := float64(time.Since(start).Microseconds()) / 1000
		p.mu.Lock()
		p.out = time.Time{}
		if err != nil {
			p.mu.Unlock()
			if c != nil {
				_ = c.Close()
				c = nil
			}
			if !p.wait(time.Second) {
				return
			}
			continue
		}
		stalled := time.Duration(took*float64(time.Millisecond)) >= stallAfter
		if !stalled {
			if p.rtt == 0 {
				p.rtt = took
			} else {
				p.rtt += (took - p.rtt) / 8
			}
			if p.slow {
				p.slow = p.rtt > slowLeave
			} else {
				p.slow = p.rtt > slowEnter
			}
		}
		p.sessions = hostSessions(raw)
		p.mu.Unlock()
		next := probePeriod
		if stalled {
			next = 0
		}
		if !p.wait(next) {
			return
		}
	}
}

func (p *prober) wait(d time.Duration) bool {
	select {
	case <-p.stop:
		return false
	case <-p.poke:
		return true
	case <-time.After(d):
		return true
	}
}

var probers sync.Map

func pokeHost(host string) {
	if v, ok := probers.Load(host); ok {
		select {
		case v.(*prober).poke <- struct{}{}:
		default:
		}
	}
}

func (p *prober) row(r *HostRow, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r.RTTMs = int(p.rtt + 0.5)
	if p.rtt > 0 && r.RTTMs == 0 {
		r.RTTMs = 1
	}
	r.Sessions = p.sessions
	switch {
	case !p.out.IsZero() && now.Sub(p.out) >= stallAfter:
		r.Health = "stalled"
		r.SilentSince = p.out.UnixMilli()
	case p.slow:
		r.Health = "slow"
	default:
		r.Health = "good"
	}
}

func hostSessions(raw []byte) []HostSession {
	var out struct {
		Sessions []struct {
			Name        string `json:"name"`
			ID          string `json:"id"`
			WindowCount int    `json:"window_count"`
			Attached    bool   `json:"attached"`
			LastActive  int64  `json:"last_active"`
		} `json:"sessions"`
	}
	if jsonx.Unmarshal(raw, &out) != nil {
		return nil
	}
	rows := make([]HostSession, 0, len(out.Sessions))
	for _, s := range out.Sessions {
		rows = append(rows, HostSession{Name: s.Name, ID: s.ID, Windows: s.WindowCount, Attached: s.Attached, LastActive: s.LastActive})
	}
	return rows
}

func linkHealth(status, detail string) string {
	switch status {
	case "connecting", "reconnecting":
		return "reconnecting"
	case "tailscale_check":
		return "signin"
	}
	if needsSignIn(detail) {
		return "signin"
	}
	return "down"
}

func needsSignIn(detail string) bool {
	d := strings.ToLower(detail)
	return strings.Contains(d, "permission denied") || strings.Contains(d, "no more authentication methods") || strings.Contains(d, "too many authentication failures")
}

func watchHosts(stop <-chan struct{}, wake <-chan struct{}, out *frameWriter, version string) {
	running := map[string]*prober{}
	defer func() {
		for name, p := range running {
			close(p.stop)
			probers.Delete(name)
		}
	}()
	var c *session.VerbClient
	defer func() {
		if c != nil {
			_ = c.Close()
		}
	}()
	var last []byte
	tick := time.NewTicker(hostsPeriod)
	defer tick.Stop()
	for {
		ev := Hosts{Hosts: []HostRow{}}
		if c == nil {
			c, _ = session.DialVerbClientAs(version)
		}
		if c != nil {
			raw, err := c.CallWithTimeout("list-hosts", nil, 10*time.Second)
			if err != nil {
				log.Printf("gui-bridge: hosts: %v", err)
				_ = c.Close()
				c = nil
			} else {
				ev.Live = true
				ev.Hosts = hostRows(raw)
			}
		}
		now := time.Now()
		seen := map[string]bool{}
		for i := range ev.Hosts {
			r := &ev.Hosts[i]
			if r.Status != "up" {
				r.Health = linkHealth(r.Status, r.detail)
				if p := running[r.Name]; p != nil {
					close(p.stop)
					delete(running, r.Name)
					probers.Delete(r.Name)
				}
				continue
			}
			seen[r.Name] = true
			p := running[r.Name]
			if p == nil {
				p = &prober{host: r.Name, version: version, stop: make(chan struct{}), poke: make(chan struct{}, 1)}
				running[r.Name] = p
				probers.Store(r.Name, p)
				go p.run()
			}
			p.row(r, now)
		}
		for name, p := range running {
			if !seen[name] {
				close(p.stop)
				delete(running, name)
				probers.Delete(name)
			}
		}
		if b, err := jsonx.Marshal(Event{Type: "hosts", Hosts: &ev}); err == nil && !bytes.Equal(b, last) {
			last = b
			out.Frame(KindJSON, b)
		}
		wait := hostsPeriod
		for _, p := range running {
			p.mu.Lock()
			if !p.out.IsZero() {
				wait = 250 * time.Millisecond
			}
			p.mu.Unlock()
		}
		var timer <-chan time.Time
		if !ev.Live || len(ev.Hosts) > 0 {
			tick.Reset(wait)
			timer = tick.C
		} else {
			tick.Stop()
		}
		select {
		case <-stop:
			return
		case <-wake:
		case <-timer:
		}
	}
}

func hostRows(raw []byte) []HostRow {
	var out struct {
		Hosts []struct {
			Host        string `json:"host"`
			Addr        string `json:"addr"`
			Status      string `json:"status"`
			Reason      string `json:"reason"`
			Detail      string `json:"detail"`
			ApprovalURL string `json:"approval_url"`
		} `json:"hosts"`
	}
	if jsonx.Unmarshal(raw, &out) != nil {
		return []HostRow{}
	}
	rows := make([]HostRow, 0, len(out.Hosts))
	for _, h := range out.Hosts {
		rows = append(rows, HostRow{Name: h.Host, Addr: h.Addr, Status: h.Status, Reason: h.Reason, ApprovalURL: h.ApprovalURL, detail: h.Detail})
	}
	return rows
}
