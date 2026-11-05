// Package hostsetup sets up another machine for tuios in one flow: it reads
// the machine over ssh, names any ssh failure with its one fix, installs
// tuios when it is missing, starts the daemon, adds the machine to the
// [hosts] table here, tests the link, and can add this machine on the far
// side too.
//
// It is one engine with three front ends: `tuios hosts setup` with flags,
// the same command on a terminal, and the add sheet in the app. A front end
// implements Asker, and Run asks it for each choice the flags did not make.
// Nothing changes either machine before the person agreed: Run asks, or the
// choice came as a flag with Yes.
//
// Every ssh call goes through this machine's ssh, with BatchMode on and one
// shared connection for the whole run, so a Tailscale SSH check asks once.
// The [hosts.NAME] table is written only after the far daemon answers, so a
// setup that stops early leaves the config file as it was.
package hostsetup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
)

// Options say what to set up, and how this machine is reached and changed.
type Options struct {
	// Dest is the far machine as ssh takes it: user@host, or an alias.
	Dest string
	// Name is the name here. Empty picks one from Dest.
	Name string
	// SSHOptions are extra ssh arguments, as in hosts add.
	SSHOptions []string
	// Version is the version of this tuios, and Executable its binary.
	Version    string
	Executable string
	// ConfigPath is the config file here.
	ConfigPath string

	// Apply asks the daemon here to read the [hosts.NAME] table it was
	// given. Nil when no daemon runs: the next start reads the file.
	Apply func(name string) error
	// TestLink dials the machine called name, as the daemon here would, and
	// returns the report. Nil uses a dial in this process.
	TestLink func(ctx context.Context, h federation.Host) (federation.HostReport, error)
	// Source makes the release binaries. Nil makes one when a release is
	// picked.
	Source *hostsync.Source
	// Here describes this machine for the both-ways step. Nil reads it.
	Here *Here
	// Desk decides what a Tailscale SSH check waits for. Nil fails such a
	// call at once with the sign-in page, which the front end shows.
	Desk *hostsync.ApprovalDesk
}

// Setup is one run.
type Setup struct {
	opts   Options
	name   string
	host   federation.Host
	share  *hostsync.Share
	runner hostsync.Runner
	probe  *Probe
	// existing is the address the [hosts] table already holds for name.
	existing string
	// daemonFact is the daemon state the start step found, in the words
	// of hostsync.
	daemonFact string
	// gate and round take turns at Tailscale sign-ins in a bulk run.
	gate  *signInGate
	round int
	// res and asker are the run's result and front end.
	res   *Result
	asker Asker
}

// New checks the destination and the name. It opens nothing.
func New(opts Options) (*Setup, error) {
	opts.Dest = strings.TrimSpace(opts.Dest)
	if opts.Dest == "" {
		return nil, errors.New("give the machine to set up, as ssh takes it: user@machine, or an ssh config name")
	}
	if strings.HasPrefix(opts.Dest, "-") {
		return nil, errfmt.Errorf("the address %q starts with a dash, which ssh reads as an option", opts.Dest)
	}
	if err := federation.CheckSSHOptions(opts.SSHOptions); err != nil {
		return nil, err
	}
	hosts, err := ConfiguredHosts(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = pickName(opts.Dest, hosts)
	}
	if err := federation.ValidHostName(name); err != nil {
		return nil, err
	}
	s := &Setup{opts: opts, name: name}
	if h, ok := hosts[name]; ok {
		if h.Addr != opts.Dest {
			return nil, errfmt.Errorf("a machine called %s is already added, at %s. Pass another name with --name", name, h.Addr)
		}
		s.existing = h.Addr
	}
	s.host = federation.Host{Name: name, Addr: opts.Dest, SSHOptions: opts.SSHOptions, ConnectTimeout: federation.DefaultConnectTimeout}
	return s, nil
}

// Name is the name the machine gets here.
func (s *Setup) Name() string { return s.name }

// Close ends the shared ssh connection.
func (s *Setup) Close() {
	if s.share != nil {
		s.share.Close()
		s.share = nil
	}
}

// open starts sharing one ssh connection for the run.
func (s *Setup) open() {
	if s.share != nil {
		return
	}
	s.share = hostsync.NewShare(federation.SSHBinary())
	s.share.Share(&s.host)
	s.runner = hostsync.SSHRunner{Host: s.host, Desk: s.opts.Desk}
}

// pickName is a name for dest: the ssh config alias as written, or the host
// part of user@host, cut to the first label of a domain name and to the name
// rules. A name in use gets a number.
func pickName(dest string, hosts map[string]federation.Host) string {
	h := dest
	if i := strings.LastIndexByte(h, '@'); i >= 0 {
		h = h[i+1:]
	}
	h = strings.TrimPrefix(h, "ssh://")
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if net.ParseIP(h) == nil {
		if first, _, ok := strings.Cut(h, "."); ok && first != "" {
			h = first
		}
	}
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == '.' || r == ':':
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-_")
	if name == "" || name == federation.LocalHostName {
		name = "machine"
	}
	base := name
	for i := 2; ; i++ {
		h, taken := hosts[name]
		if !taken || h.Addr == dest {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}
