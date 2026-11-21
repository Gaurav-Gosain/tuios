//go:build !js

package federation

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

type tailnetStatus struct {
	Self *tailnetPeer
	Peer map[string]*tailnetPeer
	User map[string]struct{ LoginName string }
}

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

func (st *tailnetStatus) peerKeys() []string {
	keys := make([]string, 0, len(st.Peer))
	for k := range st.Peer {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func tailscaleCommands() []string {
	cmds := []string{"tailscale"}
	if runtime.GOOS == "darwin" {
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
		return nil, errfmt.Errorf("%w: %w", ErrNoTailnet, err)
	}
	return tailnetMachinesFrom(st, opt), nil
}

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
		cmd := childproc.CommandContext(ctx, path, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr := cmd.Run()
		var st tailnetStatus
		if jsonErr := jsonx.Unmarshal(stdout.Bytes(), &st); jsonErr == nil {
			return &st, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if runErr == nil {
			runErr = errfmt.Errorf("tailscale status printed no status")
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			runErr = errfmt.Errorf("%w: %s", runErr, msg)
		}
		lastErr = runErr
	}
	return nil, lastErr
}

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
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

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
	if p.Tags != nil {
		m.Tags = *p.Tags
	}
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
