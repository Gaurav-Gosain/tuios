package hostsync

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

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

const syncApprovalWait = 5 * time.Minute

const approvalGather = 3 * time.Second

// ApprovalDesk decides, once per run, whether to wait for Tailscale
// approvals, and tells the person what to open.
type ApprovalDesk struct {
	out         io.Writer
	in          io.Reader
	interactive bool
	wait        time.Duration

	mu      sync.Mutex
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

// NewApprovalDesk makes the desk of one run. A desk that is not interactive
// never waits.
func NewApprovalDesk(out io.Writer, in io.Reader, interactive bool) *ApprovalDesk {
	return &ApprovalDesk{out: out, in: in, interactive: interactive, wait: syncApprovalWait, done: make(chan struct{})}
}

func (d *ApprovalDesk) enter() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.active++
	d.mu.Unlock()
}

func (d *ApprovalDesk) leave() {
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

func (d *ApprovalDesk) await(host string, g federation.SSHGate) bool {
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

func (d *ApprovalDesk) decide() {
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

	sortx.SortFunc(reqs, func(a, b approvalRequest) int { return strings.Compare(a.host, b.host) })
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

func (d *ApprovalDesk) approved(host string) {
	if d == nil || !d.interactive {
		return
	}
	fmt.Fprintf(d.out, "%s: Tailscale approved the login.\n", host)
}

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

const gatePoll = 100 * time.Millisecond

// RunGatedSSH runs one ssh call and handles a Tailscale check on the way. The
// context bounds the call. Time spent waiting for an approval does not count
// against it: the budget starts again when the approval lands.
func RunGatedSSH(ctx context.Context, cmd *exec.Cmd, stdin io.Reader, host string, desk *ApprovalDesk) (string, string, error) {
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
			return out.String(), stderr(), errfmt.Errorf("%w: %w", context.DeadlineExceeded, err)
		}
	}
}

// Share is the private folder of one run's shared ssh connections.
type Share struct {
	dir   string
	bin   string
	hosts []federation.Host
}

// NewShare makes the folder, under the user's runtime folder when there is
// one. It returns nil where ssh cannot share a connection, and the run then
// opens one connection per call as before.
func NewShare(bin string) *Share {
	if runtime.GOOS == "windows" {
		return nil
	}
	base := sockpath.RuntimeDir()
	if base == "" {
		base = "/tmp"
		if st, err := os.Stat(base); err != nil || !st.IsDir() {
			base = os.TempDir()
		}
	}
	dir, err := os.MkdirTemp(base, "tuios-ssh-")
	if err != nil {
		return nil
	}
	if strings.Contains(dir, "%") || len(dir)+4 > 100 {
		_ = os.Remove(dir)
		return nil
	}
	if bin == "" {
		bin = "ssh"
	}
	return &Share{dir: dir, bin: bin}
}

// Share gives the host a socket of its own in the folder.
func (s *Share) Share(h *federation.Host) {
	if s == nil {
		return
	}
	h.ControlPath = filepath.Join(s.dir, strconv.Itoa(len(s.hosts)))
	s.hosts = append(s.hosts, *h)
}

// Close stops each master connection and removes the folder.
func (s *Share) Close() {
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

// GateErrorOf is the Tailscale gate that stopped err, or nil.
func GateErrorOf(err error) *federation.GateError {
	if ge, ok := errors.AsType[*federation.GateError](err); ok {
		return ge
	}
	return nil
}
