//go:build !js

package federation

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
)

// The half of tailnet discovery that talks to tailscaled. It is left out of
// the browser build, which has no tailscaled to ask, and whose download would
// otherwise carry the tailscale client for nothing. See tailnet_js.go.

// TailnetMachines lists the tailnet, marking which machines are offered as
// addresses.
//
// An error means the local API could not be asked. Every caller in tuios
// treats that as an empty list: discovery is a convenience, and a machine
// without tailscale still types an address.
func TailnetMachines(ctx context.Context, opt TailnetOptions) ([]TailnetMachine, error) {
	c := &local.Client{Socket: opt.Socket}
	st, err := c.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoTailnet, err)
	}
	return tailnetMachines(st, opt), nil
}

// tailnetMachines is the pure half: everything but the call.
func tailnetMachines(st *ipnstate.Status, opt TailnetOptions) []TailnetMachine {
	if st == nil {
		return nil
	}
	out := make([]TailnetMachine, 0, len(st.Peer)+1)
	if st.Self != nil {
		out = append(out, tailnetMachine(st, st.Self, true, opt))
	}
	for _, k := range st.Peers() {
		if p := st.Peer[k]; p != nil {
			out = append(out, tailnetMachine(st, p, false, opt))
		}
	}
	// By name, so two runs read the same. Peers() is already sorted by key,
	// which is stable but says nothing to a person.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	// The cap is applied to the offered machines only, and after the sort, so
	// a large tailnet offers the first N by name rather than whichever N the
	// control plane happened to list first.
	max := opt.Max
	if max <= 0 {
		max = DefaultTailnetMax
	}
	offered := 0
	for i := range out {
		if !out[i].Offered {
			continue
		}
		offered++
		if offered > max {
			out[i].Offered = false
			out[i].Skipped = fmt.Sprintf("past the first %d", max)
		}
	}
	return out
}

func tailnetMachine(st *ipnstate.Status, p *ipnstate.PeerStatus, self bool, opt TailnetOptions) TailnetMachine {
	m := TailnetMachine{
		DNSName: strings.TrimSuffix(p.DNSName, "."),
		OS:      p.OS,
		Online:  p.Online,
		Self:    self,
		Shared:  p.ShareeNode,
		User:    st.User[p.UserID].LoginName,
	}
	// A machine owned by a person carries no tags at all, and the field is
	// then a nil pointer rather than an empty list.
	if p.Tags != nil {
		m.Tags = p.Tags.AsSlice()
	}
	// Self is always connected to itself; the control plane does not report an
	// Online for it, and a row reading "offline" for the machine you are on
	// would be nonsense.
	if self {
		m.Online = true
	}
	m.Name = p.HostName
	if label, _, ok := strings.Cut(m.DNSName, "."); ok && label != "" {
		m.Name = label
	}
	for _, ip := range p.TailscaleIPs {
		if ip.Is4() {
			m.IP = ip.String()
			break
		}
	}
	m.Addr = tailnetAddr(m, opt)
	m.Skipped = tailnetSkipReason(m, opt)
	m.Offered = m.Skipped == "" && m.Addr != ""
	return m
}
