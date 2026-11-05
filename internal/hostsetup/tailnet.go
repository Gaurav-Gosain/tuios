package hostsetup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
)

// A tailnet in bulk: every peer listed with what it has, the person ticks
// the machines they want, and Run goes over each with one set of answers.

// The states of a peer in the checklist.
const (
	PeerSame        = "same"        // tuios of this version runs there
	PeerOlder       = "older"       // tuios of another version
	PeerNoTuios     = "no_tuios"    // ssh works, tuios is missing
	PeerSignIn      = "sign_in"     // Tailscale SSH waits for a sign-in
	PeerSSHFailed   = "ssh_failed"  // ssh failed with a fix the person can make
	PeerNoSSH       = "no_ssh"      // nothing answers ssh there
	PeerOffline     = "offline"     // the tailnet says it is not connected
	PeerUnsupported = "unsupported" // tuios does not run on its system
	PeerAdded       = "added"       // already a machine here
	PeerUnknown     = "unknown"     // the probe did not finish
)

// Peer is one machine on the tailnet, as the checklist shows it.
type Peer struct {
	Name    string   `json:"name"`
	Addr    string   `json:"addr"`
	DNSName string   `json:"dns_name,omitempty"`
	OS      string   `json:"os,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	State   string   `json:"state"`
	// Version is the tuios there, for PeerSame and PeerOlder.
	Version string `json:"version,omitempty"`
	// Why says the state in one line.
	Why string `json:"why"`
	// CanSetUp says the peer can be ticked.
	CanSetUp bool `json:"can_set_up"`
	// Failure is the ssh failure, for PeerSignIn and PeerSSHFailed.
	Failure *Failure `json:"failure,omitempty"`
}

// peerProbeTimeout bounds the probe of one peer for the list.
const peerProbeTimeout = 8 * time.Second

// Parallel bounds how many machines are probed or set up at once.
const Parallel = 4

// ListTailnet lists the tailnet's peers with their state. Each online peer
// tuios runs on gets the setup's probe, Parallel at a time. An offline peer
// is never dialed. added is the [hosts] table here, by name.
func ListTailnet(ctx context.Context, opt federation.TailnetOptions, added map[string]federation.Host, version string) ([]Peer, error) {
	opt.Offline, opt.OS, opt.Max = true, nil, 1<<16
	ms, err := federation.TailnetMachines(ctx, opt)
	if err != nil {
		return nil, err
	}
	addrs := map[string]bool{}
	for n, h := range added {
		addrs[n] = true
		addrs[h.Addr[strings.LastIndexByte(h.Addr, '@')+1:]] = true
	}
	var peers []Peer
	for _, m := range ms {
		if m.Self {
			continue
		}
		p := Peer{Name: m.Name, Addr: m.Addr, DNSName: m.DNSName, OS: m.OS, Tags: m.Tags, State: PeerUnknown}
		switch os := strings.ToLower(m.OS); {
		case addrs[m.Name] || addrs[m.DNSName] || addrs[m.IP]:
			p.State, p.Why = PeerAdded, "already a machine here"
		case !m.Online:
			p.State, p.Why = PeerOffline, "offline"
		case os != "linux" && os != "macos" && os != "freebsd":
			p.State, p.Why = PeerUnsupported, m.OS+": tuios does not run there"
		case !federation.SafeRemoteArg(m.Addr):
			p.Why = "tuios has no address for it"
		}
		peers = append(peers, p)
	}
	sem := make(chan struct{}, Parallel)
	var wg sync.WaitGroup
	for i := range peers {
		if peers[i].State == PeerUnknown && peers[i].Why == "" {
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				peers[i].read(ctx, version)
			})
		}
	}
	wg.Wait()
	return peers, nil
}

// read probes one peer the way a setup does, and says its state.
func (p *Peer) read(ctx context.Context, version string) {
	pctx, cancel := context.WithTimeout(ctx, peerProbeTimeout)
	defer cancel()
	r := hostsync.SSHRunner{Host: federation.Host{Name: p.Name, Addr: p.Addr, ConnectTimeout: 5 * time.Second}}
	pr, err := probe(pctx, r)
	if err != nil {
		f, ok := errors.AsType[*Failure](failureOf(err, p.Name, p.Addr, nil))
		if !ok {
			p.Why = "the probe did not finish"
			return
		}
		p.Failure, p.State, p.Why = f, PeerNoSSH, strings.TrimSuffix(lowerFirst(f.Reason), ".")
		switch f.Class {
		case FailTailscaleCheck:
			p.State, p.Why, p.CanSetUp = PeerSignIn, "Tailscale asks you to sign in", true
		case string(federation.FailAuth), string(federation.FailHostKeyNew):
			p.State, p.CanSetUp = PeerSSHFailed, true
		}
		return
	}
	p.Version = pr.TuiosVersion
	switch {
	case pr.OS == "":
		p.State, p.Why = PeerUnsupported, pr.Uname+": tuios does not run there"
	case pr.Tuios == "":
		p.State, p.Why, p.CanSetUp = PeerNoTuios, "no tuios", true
	case strings.TrimPrefix(p.Version, "v") == strings.TrimPrefix(version, "v"):
		p.State, p.Why, p.CanSetUp = PeerSame, "tuios "+p.Version+", the same as here", true
	default:
		p.State, p.Why, p.CanSetUp = PeerOlder, "tuios "+p.Version, true
	}
}

// BulkAsker is a front end for a bulk run: an Asker for each machine.
type BulkAsker interface {
	// For is the Asker of one machine. Its questions name the machine.
	For(name string) Asker
}

// BulkResult is what a bulk run did.
type BulkResult struct {
	OK       bool      `json:"ok"`
	DryRun   bool      `json:"dry_run,omitempty"`
	Machines []*Result `json:"machines"`
	Skipped  []Skip    `json:"skipped,omitempty"`
	Summary  Summary   `json:"summary"`
}

// Skip is a peer the run did not set up.
type Skip struct {
	Name string `json:"name"`
	Why  string `json:"why"`
}

// Summary counts a bulk run.
type Summary struct {
	Ready   int `json:"ready"`
	NeedYou int `json:"need_you"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// RunBulk sets up each peer, Parallel at a time, with c, or with own[name]
// for a machine that has its own answers. One failure never stops the
// others. A Tailscale sign-in goes one machine at a time.
func RunBulk(ctx context.Context, base Options, peers []Peer, c Choices, own map[string]Choices, a BulkAsker) *BulkResult {
	res := &BulkResult{DryRun: c.DryRun, Machines: make([]*Result, len(peers))}
	gate := &signInGate{}
	sem := make(chan struct{}, Parallel)
	var wg sync.WaitGroup
	for i, p := range peers {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			opts := base
			opts.Dest, opts.Name = p.Addr, p.Name
			choices := c
			if o, ok := own[p.Name]; ok {
				choices = o
			}
			choices.bulk = true
			s, err := New(opts)
			if err != nil {
				res.Machines[i] = &Result{Dest: p.Addr, Name: p.Name, Error: err.Error()}
				return
			}
			defer s.Close()
			s.gate = gate
			r, _ := s.Run(ctx, choices, a.For(s.Name()))
			res.Machines[i] = r
		})
	}
	wg.Wait()
	res.OK = true
	for _, r := range res.Machines {
		switch {
		case r.OK:
			res.Summary.Ready++
		case r.Person != nil || (r.Failure != nil && r.Failure.NeedsPerson):
			res.Summary.NeedYou++
			res.OK = false
		default:
			res.Summary.Failed++
			res.OK = false
		}
	}
	res.Summary.Skipped = len(res.Skipped)
	return res
}

// signInGate lets one machine at a time wait on a person for a Tailscale
// sign-in, so two sign-in pages never open at once. A machine whose turn
// comes after another one's sign-in tests again first: one approval often
// covers the whole tailnet.
type signInGate struct {
	mu   sync.Mutex
	seen int
}

// fix is a.Fix for a machine of a bulk run. round is the gate's count
// when the machine last had its turn.
func (g *signInGate) fix(ctx context.Context, a Asker, f *Failure, round *int) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen > *round {
		*round = g.seen
		return true, nil
	}
	retry, err := a.Fix(ctx, f)
	g.seen++
	*round = g.seen
	return retry, err
}
