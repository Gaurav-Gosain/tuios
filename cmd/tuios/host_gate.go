package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

// Tailscale SSH in check mode, for the commands that run ssh on a host.
//
// The gate is read in internal/federation (sshgate.go). This file is what a
// command does about it. `hosts sync` runs several ssh calls on each host, and
// Tailscale asks for an approval on each new connection, so two things are
// done here:
//
//   - The calls of one run share a connection per host (sshShare), so one
//     approval covers the run.
//   - On a terminal, the run prints every URL at once and offers to wait
//     (approvalDesk). The waiting connection is the one that goes on when the
//     person approves, so nothing is dialed again. Off a terminal, or with
//     --json, the run fails the host at once and reports the URL.

// syncApprovalWait is how long sync waits for the person to approve a login.
const syncApprovalWait = 5 * time.Minute

// approvalGather is how long the desk waits for the other hosts to reach the
// gate before it asks, so the person sees every URL in one list.
const approvalGather = 3 * time.Second

// approvalDesk decides, once per run, whether to wait for Tailscale
// approvals, and tells the person what to open.
type approvalDesk struct {
	out         io.Writer
	in          io.Reader
	interactive bool
	wait        time.Duration

	mu sync.Mutex
	// active counts the ssh calls in flight. When every one of them waits at
	// the gate, nothing else is coming and the desk asks at once.
	active  int
	pending []approvalRequest
	timer   *time.Timer
	decided bool
	waitFor bool
	done    chan struct{}
}

type approvalRequest struct {
	host, url string
}

// newApprovalDesk makes the desk of one run. A desk that is not interactive
// never waits.
func newApprovalDesk(out io.Writer, in io.Reader, interactive bool) *approvalDesk {
	return &approvalDesk{out: out, in: in, interactive: interactive, wait: syncApprovalWait, done: make(chan struct{})}
}

func (d *approvalDesk) enter() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.active++
	d.mu.Unlock()
}

func (d *approvalDesk) leave() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.active--
	ready := !d.decided && len(d.pending) > 0 && len(d.pending) >= d.active
	d.mu.Unlock()
	if ready {
		d.decide()
	}
}

// await is called by an ssh call that Tailscale holds. It returns whether the
// call should keep waiting for the approval.
func (d *approvalDesk) await(host string, g federation.SSHGate) bool {
	if d == nil || !d.interactive {
		return false
	}
	d.mu.Lock()
	if d.decided {
		wait := d.waitFor
		d.mu.Unlock()
		if wait {
			fmt.Fprintf(d.out, "%s: Tailscale needs you to approve the login. Open %s\n", host, cmp.Or(g.URL, "unknown"))
		}
		return wait
	}
	d.pending = append(d.pending, approvalRequest{host: host, url: g.URL})
	ready := len(d.pending) >= d.active
	if d.timer == nil {
		d.timer = time.AfterFunc(approvalGather, d.decide)
	}
	d.mu.Unlock()
	if ready {
		d.decide()
	}
	<-d.done
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.waitFor
}

// decide lists the URLs and asks once.
func (d *approvalDesk) decide() {
	d.mu.Lock()
	if d.decided {
		d.mu.Unlock()
		return
	}
	d.decided = true
	if d.timer != nil {
		d.timer.Stop()
	}
	reqs := slices.Clone(d.pending)
	d.mu.Unlock()

	slices.SortFunc(reqs, func(a, b approvalRequest) int { return strings.Compare(a.host, b.host) })
	width := 0
	for _, r := range reqs {
		width = max(width, len(r.host))
	}
	fmt.Fprintf(d.out, "Tailscale needs you to approve the ssh login to %s.\n", plural.Count(len(reqs), "host"))
	fmt.Fprintln(d.out, "Open each link in a browser and approve the login:")
	for _, r := range reqs {
		fmt.Fprintf(d.out, "  %-*s  %s\n", width, r.host, cmp.Or(r.url, "unknown"))
	}
	minutes := int(d.wait / time.Minute)
	fmt.Fprintf(d.out, "Wait up to %d minutes for the approvals? Each host continues when you approve it. [Y/n] ", minutes)
	line, _ := bufio.NewReader(d.in).ReadString('\n')
	wait := true
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "n", "no":
		wait = false
	}
	if wait {
		fmt.Fprintln(d.out, "Waiting for the approvals. Press Ctrl+C to stop.")
	} else {
		fmt.Fprintln(d.out, "sync does not wait. The rows below show the links.")
	}
	d.mu.Lock()
	d.waitFor = wait
	d.mu.Unlock()
	close(d.done)
}

// approved tells the person a host goes on.
func (d *approvalDesk) approved(host string) {
	if d == nil || !d.interactive {
		return
	}
	fmt.Fprintf(d.out, "%s: Tailscale approved the login.\n", host)
}

// gateBuffer is ssh's stderr, kept bounded and safe to read while ssh still
// writes it.
type gateBuffer struct {
	mu  sync.Mutex
	buf limitedBuffer
}

func (b *gateBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *gateBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// gatePoll is how often a running ssh call reads its stderr for the gate.
const gatePoll = 100 * time.Millisecond

// runGatedSSH runs one ssh call and handles a Tailscale check on the way. The
// context bounds the call. Time spent waiting for an approval does not count
// against it: the budget starts again when the approval lands.
func runGatedSSH(ctx context.Context, cmd *exec.Cmd, stdin io.Reader, host string, desk *approvalDesk) (string, string, error) {
	var out limitedBuffer
	errb := &gateBuffer{buf: limitedBuffer{limit: 8 << 10}}
	out.limit = syncOutputLimit
	cmd.Stdout, cmd.Stderr = &out, errb
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.WaitDelay = 5 * time.Second
	desk.enter()
	defer desk.leave()
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stderr := func() string { return strings.TrimSpace(errb.String()) }

	budget := time.Duration(0)
	if dl, ok := ctx.Deadline(); ok {
		budget = time.Until(dl)
	}
	// waitCtx is the call's own bound until Tailscale holds the login. Then
	// it is the approval wait, and after the approval the budget again.
	waitCtx := ctx
	var cancelWait context.CancelFunc = func() {}
	defer func() { cancelWait() }()
	rebound := func(d time.Duration) {
		cancelWait()
		waitCtx, cancelWait = context.WithTimeout(context.WithoutCancel(ctx), d)
	}
	tick := time.NewTicker(gatePoll)
	defer tick.Stop()
	var gate *federation.SSHGate
	approved := false
	stop := func() error {
		_ = cmd.Process.Kill()
		return <-done
	}
	for {
		select {
		case err := <-done:
			if gate != nil && !approved {
				// The call can end between two reads of stderr.
				if g := federation.ParseSSHGate(errb.String()); g != nil && g.Approved {
					desk.approved(host)
				}
			}
			return out.String(), stderr(), err
		case <-tick.C:
			g := federation.ParseSSHGate(errb.String())
			switch {
			case g == nil || g.Kind != federation.GateTailscaleCheck:
			case gate == nil && !g.Approved:
				gate = g
				if !desk.await(host, *g) {
					_ = stop()
					return out.String(), stderr(), &federation.GateError{Gate: *g}
				}
				rebound(desk.wait)
			case gate != nil && g.Approved && !approved:
				approved = true
				desk.approved(host)
				if budget > 0 {
					rebound(budget)
				} else {
					cancelWait()
					waitCtx = ctx
				}
			}
		case <-waitCtx.Done():
			err := stop()
			if gate != nil && !approved {
				return out.String(), stderr(), &federation.GateError{Gate: *gate}
			}
			if err == nil {
				err = context.DeadlineExceeded
			}
			return out.String(), stderr(), fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
		}
	}
}

// sshShare is the private folder of one run's shared ssh connections.
type sshShare struct {
	dir   string
	bin   string
	hosts []federation.Host
}

// newSSHShare makes the folder, under the user's runtime folder when there is
// one. It returns nil where ssh cannot share a connection, and the run then
// opens one connection per call as before.
func newSSHShare(bin string) *sshShare {
	if runtime.GOOS == "windows" {
		return nil
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		// The socket path is limited to about 100 bytes, and the temp
		// folder of macOS is long, so /tmp comes first.
		base = "/tmp"
		if st, err := os.Stat(base); err != nil || !st.IsDir() {
			base = os.TempDir()
		}
	}
	dir, err := os.MkdirTemp(base, "tuios-ssh-")
	if err != nil {
		return nil
	}
	// ssh reads % in a ControlPath as a token, and a socket path has a
	// length limit. A folder that breaks either is not used.
	if strings.Contains(dir, "%") || len(dir)+4 > 100 {
		_ = os.Remove(dir)
		return nil
	}
	if bin == "" {
		bin = "ssh"
	}
	return &sshShare{dir: dir, bin: bin}
}

// share gives the host a socket of its own in the folder.
func (s *sshShare) share(h *federation.Host) {
	if s == nil {
		return
	}
	h.ControlPath = filepath.Join(s.dir, strconv.Itoa(len(s.hosts)))
	s.hosts = append(s.hosts, *h)
}

// close stops each master connection and removes the folder.
func (s *sshShare) close() {
	if s == nil {
		return
	}
	var wg sync.WaitGroup
	for _, h := range s.hosts {
		if _, err := os.Stat(h.ControlPath); err != nil {
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, s.bin, federation.StopSharingArgs(h)...)
			cmd.WaitDelay = time.Second
			_ = cmd.Run()
		})
	}
	wg.Wait()
	_ = os.RemoveAll(s.dir)
}

// gateErrorOf is the Tailscale gate that stopped err, or nil.
func gateErrorOf(err error) *federation.GateError {
	if ge, ok := errors.AsType[*federation.GateError](err); ok {
		return ge
	}
	return nil
}
