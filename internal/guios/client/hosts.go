package client

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// The machines this machine's daemon links (tuios hosts). The daemon holds
// one ssh link to each, and says how each stands: list-hosts, and the
// host-changed event when a link moves. The app shows a host's sessions
// through ConnectHost once its link is up.

// HostReport is a linked machine as list-hosts reports it. Every field but
// Host and Status may be empty: a link that never came up knows no version
// and no load.
type HostReport struct {
	Host            string  `json:"host"`
	Addr            string  `json:"addr"`
	Status          string  `json:"status"`
	Reason          string  `json:"reason"`
	Detail          string  `json:"detail"`
	ApprovalURL     string  `json:"approval_url"`
	ApprovalRefused bool    `json:"approval_refused"`
	DaemonVersion   string  `json:"daemon_version"`
	Protocol        int     `json:"protocol"`
	Instance        string  `json:"instance"`
	Command         string  `json:"command"`
	Sessions        int     `json:"sessions"`
	LastOK          int64   `json:"last_ok"`
	LastTry         int64   `json:"last_try"`
	Drops           int     `json:"drops"`
	DropReason      string  `json:"drop_reason"`
	Cores           int     `json:"cores"`
	Load1           float64 `json:"load1"`
	MemPct          float64 `json:"mem_pct"`
	AgentsWorking   int     `json:"agents_working"`
	LoadOK          *bool   `json:"load_ok"`
	LoadAt          int64   `json:"load_at"`
	RTTMillis       int64   `json:"rtt_ms"`
	Queued          int     `json:"queued"`
	Events          string  `json:"events"`
}

// Link states, as list-hosts says them.
const (
	HostUp           = "up"
	HostConnecting   = "connecting"
	HostReconnecting = "reconnecting"
	HostUnreachable  = "unreachable"
	HostNoDaemon     = "no_daemon"
	HostNoTuios      = "no_tuios"
	HostIncompatible = "incompatible"
	HostSignIn       = "tailscale_check"
)

// LoadKnown reports whether the report carries the far machine's load.
func (r HostReport) LoadKnown() bool {
	return r.Cores > 0 && (r.LoadOK == nil || *r.LoadOK)
}

// Hosts lists the machines the daemon links, in its order, and this
// machine's own load.
func (c *Client) Hosts() ([]HostReport, HostReport, error) {
	raw, err := c.verb("list-hosts", nil)
	if err != nil {
		return nil, HostReport{}, err
	}
	var res struct {
		Hosts []HostReport `json:"hosts"`
		Load  *HostReport  `json:"load"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return nil, HostReport{}, err
	}
	var local HostReport
	if res.Load != nil {
		local = *res.Load
	}
	return res.Hosts, local, nil
}

// RetryHost has the daemon dial a host's link again now.
func (c *Client) RetryHost(name string) error {
	_, err := c.verb("retry-host", map[string]any{"host": name})
	return err
}

// WatchHosts calls fn once a burst of host-changed events settles, on a
// connection of its own, until the client closes or the connection ends.
// Nothing polls here: a load moves without an event, so the app asks again
// on its own slow clock.
func (c *Client) WatchHosts(fn func()) {
	go func() {
		vc, err := c.dial()
		if err != nil {
			return
		}
		defer vc.Close()
		go func() {
			<-c.closed
			vc.Close()
		}()
		if _, err := vc.Call("subscribe", map[string]any{"types": []string{"host-changed"}}); err != nil {
			return
		}
		kick := make(chan struct{}, 1)
		go func() {
			for {
				select {
				case <-kick:
				case <-c.closed:
					return
				}
				time.Sleep(50 * time.Millisecond)
				select {
				case <-kick:
				default:
				}
				fn()
			}
		}()
		for {
			if _, err := vc.ReadEventLine(0); err != nil {
				return
			}
			select {
			case kick <- struct{}{}:
			default:
			}
		}
	}()
}
