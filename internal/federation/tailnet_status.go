//go:build !js

package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// The half of tailnet discovery that talks to tailscaled. It is left out of
// the browser build, which has no tailscaled to ask. See tailnet_js.go.
//
// The status comes from `tailscale status --json`, the tailscale command's
// own read of the same local API call. tuios used to link tailscale's Go
// client for it, which brought 650 KB of tailscale packages (and most of
// net/http) into the binary for one read-only call. The command also finds
// tailscaled the way the client did, including every macOS variant, since
// it is the code the client's discovery was taken from.
//
// Ways this can differ from the linked client, and what covers each:
//   - The JSON shape. tailnetStatus mirrors the fields of ipnstate.Status that
//     are read here, with the same names; the tests build an ipnstate.Status
//     and decode its JSON through tailnetStatus, so a rename upstream fails a
//     test.
//   - Peer order. Status.Peers sorts by node key bytes; the keys here are
//     "nodekey:" and lower-case hex, whose string order is the same.
//   - No tailscale command on PATH. The macOS app keeps its command inside
//     the bundle, which is tried next. Without either, discovery reports
//     ErrNoTailnet, as it did when the socket could not be reached.
//   - A command that prints JSON and exits non-zero (a stopped tailscaled
//     does this on some versions). The JSON is used when it parses.
//   - A hung command. It runs under ctx, as the local API call did.

// tailnetStatus is the part of ipnstate.Status that discovery reads.
type tailnetStatus struct {
	Self *tailnetPeer
	Peer map[string]*tailnetPeer
	User map[string]struct{ LoginName string }
}

// tailnetPeer is the part of ipnstate.PeerStatus that discovery reads.
type tailnetPeer struct {
	HostName     string
	DNSName      string
	OS           string
	UserID       int64
	TailscaleIPs []netip.Addr
	Tags         *[]string
	Online       bool
	ShareeNode   bool
}

// peerKeys is the peer map's keys in the order Status.Peers gives them.
func (st *tailnetStatus) peerKeys() []string {
	keys := make([]string, 0, len(st.Peer))
	for k := range st.Peer {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// tailscaleCommands are the tailscale commands to try, in order.
func tailscaleCommands() []string {
	cmds := []string{"tailscale"}
	if runtime.GOOS == "darwin" {
		// The App Store and standalone apps put the command in the bundle
		// and add nothing to PATH unless the user asks.
		cmds = append(cmds, "/Applications/Tailscale.app/Contents/MacOS/Tailscale")
	}
	return cmds
}

// TailnetMachines lists the tailnet, marking which machines are offered as
// addresses.
//
// An error means tailscaled could not be asked. Every caller in tuios treats
// that as an empty list: discovery is a convenience, and a machine without
// tailscale still types an address.
func TailnetMachines(ctx context.Context, opt TailnetOptions) ([]TailnetMachine, error) {
	st, err := readTailnetStatus(ctx, opt.Socket)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoTailnet, err)
	}
	return tailnetMachinesFrom(st, opt), nil
}

// readTailnetStatus runs `tailscale status --json` and decodes what it prints.
func readTailnetStatus(ctx context.Context, socket string) (*tailnetStatus, error) {
	args := []string{"status", "--json"}
	if socket != "" {
		args = append([]string{"--socket=" + socket}, args...)
	}
	var lastErr error
	for _, name := range tailscaleCommands() {
		path, err := exec.LookPath(name)
		if err != nil {
			lastErr = err
			continue
		}
		cmd := exec.CommandContext(ctx, path, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr := cmd.Run()
		var st tailnetStatus
		if jsonErr := json.Unmarshal(stdout.Bytes(), &st); jsonErr == nil {
			return &st, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if runErr == nil {
			runErr = fmt.Errorf("tailscale status printed no status")
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			runErr = fmt.Errorf("%w: %s", runErr, msg)
		}
		lastErr = runErr
	}
	return nil, lastErr
}

// tailnetMachinesFrom is the pure half: everything but the call.
func tailnetMachinesFrom(st *tailnetStatus, opt TailnetOptions) []TailnetMachine {
	if st == nil {
		return nil
	}
	out := make([]TailnetMachine, 0, len(st.Peer)+1)
	if st.Self != nil {
		out = append(out, tailnetMachine(st, st.Self, true, opt))
	}
	for _, k := range st.peerKeys() {
		if p := st.Peer[k]; p != nil {
			out = append(out, tailnetMachine(st, p, false, opt))
		}
	}
	// By name, so two runs read the same. The peer keys are already sorted,
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

func tailnetMachine(st *tailnetStatus, p *tailnetPeer, self bool, opt TailnetOptions) TailnetMachine {
	m := TailnetMachine{
		DNSName: strings.TrimSuffix(p.DNSName, "."),
		OS:      p.OS,
		Online:  p.Online,
		Self:    self,
		Shared:  p.ShareeNode,
		User:    st.User[strconv.FormatInt(p.UserID, 10)].LoginName,
	}
	// A machine owned by a person carries no tags at all, and the field is
	// then absent rather than an empty list.
	if p.Tags != nil {
		m.Tags = *p.Tags
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
