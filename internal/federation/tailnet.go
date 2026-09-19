package federation

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
)

// Address discovery on a tailnet: the machines a user could reasonably type as
// a host's addr, read from the tailscaled already running on this machine.
//
// This is the same idea as sshalias.go and obeys the same rule. Nothing is
// added automatically, and a host exists only because the user named it
// (federation design, section 3). What is offered is a list of names to pick
// from, so nobody has to remember what a machine is called or type out a
// MagicDNS name by hand.
//
// It reads and never writes. The only call made is Status, which is the same
// call `tailscale status` makes, and it needs no root and no operator setting:
// the local API grants read to any connection on the socket. If tailscaled is
// not running, or the machine is not on a tailnet, the list is empty and
// nothing else changes.
//
// Nothing here dials a tailnet address. A machine offered from this list is
// reached the same way every other host is, by ssh, which already works over a
// tailnet because MagicDNS names resolve like any other name. The tailnet is
// how the name resolves and how the traffic is carried, not a second transport
// inside tuios.

// DefaultTailnetMax bounds the list. A large tailnet holds hundreds of
// machines and a candidate list that long helps nobody.
const DefaultTailnetMax = 50

// DefaultTailnetOS are the operating systems offered by default: the ones that
// can run a tuios daemon. A phone on the tailnet is a real machine and is
// still listed by `tuios hosts tailnet`, marked with the reason it was not
// offered, so nobody has to wonder where it went.
var DefaultTailnetOS = []string{"linux", "macos", "windows", "freebsd", "openbsd", "netbsd"}

// TailnetAddrKind is what to offer as the address.
const (
	// TailnetAddrDNS is the MagicDNS name, which resolves from anywhere on
	// the tailnet. It is the default because it is the one form that works
	// without depending on a search domain being configured.
	TailnetAddrDNS = "dns"
	// TailnetAddrName is the short name. It resolves when the tailnet's
	// search domain is in place, which is the usual setup, and it is shorter
	// to read in the rail.
	TailnetAddrName = "name"
	// TailnetAddrIP is the 100.x address. It does not depend on DNS at all,
	// which is what makes it the answer on a machine where MagicDNS is off.
	TailnetAddrIP = "ip"
)

// TailnetOptions are the knobs over what gets offered. The zero value is not
// the default: call DefaultTailnetOptions and change what you want, so a new
// knob added later keeps its sensible value rather than reading as "off".
type TailnetOptions struct {
	// Addr is which form of address to offer: TailnetAddrDNS, TailnetAddrName
	// or TailnetAddrIP.
	Addr string
	// User is an ssh login put in front of every address, so a candidate reads
	// ubuntu@box.example.ts.net. Empty offers the address alone.
	User string
	// Users are per-machine logins, keyed by the machine's short name, for a
	// tailnet where the login differs from box to box. A machine named here
	// wins over User.
	Users map[string]string
	// OS are the operating systems to offer, matched case-insensitively
	// against what the tailnet reports. Empty offers every machine.
	OS []string
	// Offline offers machines the control plane says are not connected. They
	// are left out by default because a machine that is not on the tailnet
	// cannot be dialled, but the flag is here because "offline" means "not
	// talking to the control plane" rather than "unreachable from you".
	Offline bool
	// Self offers this machine. It is left out by default: a host entry
	// pointing back at the daemon you are already talking to is the one thing
	// the federation table refuses, since `local` already means that.
	Self bool
	// Shared offers machines other people shared into this tailnet. They are
	// left out by default because a shared node is somebody else's machine.
	Shared bool
	// Include and Exclude are glob patterns matched against both the short
	// name and the MagicDNS name. Exclude wins. Empty Include means everything.
	Include []string
	Exclude []string
	// Max bounds the offered list. Zero means DefaultTailnetMax.
	Max int
	// Socket overrides the path to the tailscaled local API socket, for a
	// machine where it is not in the usual place. Empty finds it the way the
	// tailscale command does, which is what handles every macOS variant.
	Socket string
}

// DefaultTailnetOptions is what tuios offers with nothing configured.
func DefaultTailnetOptions() TailnetOptions {
	return TailnetOptions{
		Addr: TailnetAddrDNS,
		OS:   DefaultTailnetOS,
		Max:  DefaultTailnetMax,
	}
}

// TailnetMachine is one machine on the tailnet.
//
// Every machine the tailnet reports is returned, offered or not, with Skipped
// saying why one is not. A list that silently drops rows leaves a person
// looking for a machine they can see in `tailscale status` with nothing to
// read; this way the answer is on the row.
type TailnetMachine struct {
	// Name is the machine's short name, which is the first label of its
	// MagicDNS name.
	//
	// It is taken from there rather than from the hostname the machine
	// reports, because that one is whatever the box calls itself: a phone
	// reports "localhost", a tablet reports "OnePlus Pad 3" with spaces in it.
	// The DNS label is the name the tailnet actually knows it by, and it is
	// the name `tailscale status` prints.
	Name string
	// DNSName is the full MagicDNS name, without the trailing dot.
	DNSName string
	// IP is the machine's tailnet IPv4 address.
	IP string
	// Addr is what to type as a host address, built per the options: one of
	// the three forms, with a login in front when one is configured.
	Addr string
	// OS is what the tailnet reports the machine runs ("linux", "macOS").
	OS string
	// User is the login of whoever owns the machine.
	User string
	// Tags are the machine's ACL tags, empty for a machine owned by a person.
	Tags []string
	// Online is whether the control plane says the machine is connected. It is
	// not a reachability test: a machine can be online here and still refuse a
	// connection, and the host listing is what answers that.
	Online bool
	// Self marks this machine.
	Self bool
	// Shared marks a machine somebody else shared into this tailnet.
	Shared bool
	// Offered is whether this machine is in the candidate list.
	Offered bool
	// Skipped is why it is not, empty when it is.
	Skipped string
}

// ErrNoTailnet reports that this machine is not on a tailnet, or that
// tailscaled is not running. It is not a failure of tuios and callers treat it
// as an empty list.
var ErrNoTailnet = errors.New("no tailnet on this machine")

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

// TailnetAddrs is the offered addresses alone, which is what a completion and
// a candidate list want. A machine with no tailnet yields none and no error.
func TailnetAddrs(ctx context.Context, opt TailnetOptions) []string {
	machines, err := TailnetMachines(ctx, opt)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(machines))
	for _, m := range machines {
		if m.Offered {
			out = append(out, m.Addr)
		}
	}
	return out
}

// TailnetAddrFor is the address of one machine by its short name, for
// `tuios hosts add NAME --tailnet`. It reports whether the tailnet has it,
// and it ignores the offer filters: a name the user typed is a decision, and
// refusing it because a filter would not have suggested it would be tuios
// arguing with an instruction.
func TailnetAddrFor(ctx context.Context, name string, opt TailnetOptions) (string, bool) {
	machines, err := TailnetMachines(ctx, opt)
	if err != nil {
		return "", false
	}
	for _, m := range machines {
		if strings.EqualFold(m.Name, name) || strings.EqualFold(m.DNSName, name) {
			return m.Addr, true
		}
	}
	return "", false
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

// tailnetAddr builds what a person would type for this machine.
func tailnetAddr(m TailnetMachine, opt TailnetOptions) string {
	addr := m.DNSName
	switch opt.Addr {
	case TailnetAddrName:
		addr = m.Name
	case TailnetAddrIP:
		addr = m.IP
	}
	if addr == "" {
		return ""
	}
	if user := tailnetUser(m, opt); user != "" {
		return user + "@" + addr
	}
	return addr
}

func tailnetUser(m TailnetMachine, opt TailnetOptions) string {
	if u, ok := opt.Users[m.Name]; ok {
		return u
	}
	return opt.User
}

// tailnetSkipReason is why a machine is not offered, in the words a person
// would want to read on the row. Empty means it is offered.
func tailnetSkipReason(m TailnetMachine, opt TailnetOptions) string {
	switch {
	case m.Self && !opt.Self:
		return "this machine"
	case m.Shared && !opt.Shared:
		return "shared from another tailnet"
	case !m.Online && !opt.Offline:
		return "offline"
	case !tailnetOSAllowed(m.OS, opt.OS):
		return "cannot run tuios (" + m.OS + ")"
	case tailnetMatchesAny(m, opt.Exclude):
		return "excluded"
	case len(opt.Include) > 0 && !tailnetMatchesAny(m, opt.Include):
		return "not included"
	}
	return ""
}

func tailnetOSAllowed(os string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), os) {
			return true
		}
	}
	return false
}

// tailnetMatchesAny matches a pattern against both names a person might have
// written it for. A pattern that does not compile matches nothing rather than
// everything: a typo in a filter must not quietly widen it.
func tailnetMatchesAny(m TailnetMachine, patterns []string) bool {
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		for _, name := range []string{m.Name, m.DNSName} {
			if ok, err := path.Match(pat, name); err == nil && ok {
				return true
			}
		}
	}
	return false
}
