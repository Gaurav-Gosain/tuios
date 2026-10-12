package federation

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

// SSHConfig is what ssh -G says it would use for a host: the options of the
// person's ~/.ssh/config that the link has to respect.
type SSHConfig struct {
	// ForwardAgent is the forwardagent value: "yes", "no" or a socket path.
	// It is "" when ssh -G printed none.
	ForwardAgent string
	// ControlPath is the controlpath value, "" when ssh -G printed none.
	// "none" means no path.
	ControlPath string
	// Target is where ssh connects, through what. Two lines with one target
	// reach one machine as one user.
	Target SSHTarget
}

// SSHTarget is the part of ssh -G's answer that says which machine a line
// reaches and as whom: the host name, the user and the port, and the jump
// hosts or proxy command on the way. The same host name through another
// jump host can be another machine (two private networks).
type SSHTarget struct {
	HostName, User, Port    string
	ProxyJump, ProxyCommand string
}

// Complete reports whether ssh named a host, a user and a port.
func (t SSHTarget) Complete() bool {
	return t.HostName != "" && t.User != "" && t.Port != ""
}

// sshConfigTimeout bounds one ssh -G. It reaches no network, but a Match
// exec line in the config runs a command, and that command can hang.
const sshConfigTimeout = 3 * time.Second

// sshConfigWaitDelay is how long ssh -G's output pipe may stay open after
// ssh has exited or been killed. A Match exec child inherits the pipe, so
// without it the read waits for that child, past the timeout.
const sshConfigWaitDelay = 500 * time.Millisecond

var sshConfigCache struct {
	mu sync.Mutex
	m  map[string]SSHConfig
}

// sshConfigKey names an answer: ssh -G's answer depends on the program, the
// address and the host's options.
func sshConfigKey(h Host) string {
	return SSHBinary() + "\x00" + h.Addr + "\x00" + strings.Join(h.SSHOptions, "\x00")
}

// ReadSSHConfig asks ssh -G what it would do for h's address with h's
// options. ok is false when ssh -G failed or timed out. An answer is cached
// until ForgetSSHConfig. A failure is not cached, and is asked again next
// time.
func ReadSSHConfig(h Host) (SSHConfig, bool) {
	key := sshConfigKey(h)
	sshConfigCache.mu.Lock()
	c, ok := sshConfigCache.m[key]
	sshConfigCache.mu.Unlock()
	if ok {
		return c, true
	}
	c, ok = querySSHConfig(h)
	if !ok {
		return SSHConfig{}, false
	}
	sshConfigCache.mu.Lock()
	if sshConfigCache.m == nil {
		sshConfigCache.m = make(map[string]SSHConfig)
	}
	sshConfigCache.m[key] = c
	sshConfigCache.mu.Unlock()
	return c, true
}

// ForgetSSHConfig drops every cached answer, on a config reload: the hosts
// or ~/.ssh/config may say something else now.
func ForgetSSHConfig() {
	sshConfigCache.mu.Lock()
	sshConfigCache.m = nil
	sshConfigCache.mu.Unlock()
}

func querySSHConfig(h Host) (SSHConfig, bool) {
	return ResolveSSH(append(slices.Clone(h.SSHOptions), "--", h.Addr))
}

// ResolveSSH asks ssh -G what it would do for an ssh command line: args are
// the options and the destination, without the program and without -G. It is
// not cached. ok is false when ssh -G failed or timed out.
func ResolveSSH(args []string) (SSHConfig, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), sshConfigTimeout)
	defer cancel()
	args = append([]string{"-G"}, args...)
	cmd := childproc.CommandContext(ctx, SSHBinary(), args...)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = sshConfigWaitDelay
	out, err := cmd.Output()
	// ErrWaitDelay with a clean exit: ssh answered in full, and a child it
	// left behind held the pipe open.
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return SSHConfig{}, false
	}
	var c SSHConfig
	for line := range strings.SplitSeq(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), " ")
		v = strings.TrimSpace(v)
		switch strings.ToLower(k) {
		case "forwardagent":
			c.ForwardAgent = v
		case "controlpath":
			c.ControlPath = v
		case "hostname":
			c.Target.HostName = strings.ToLower(v)
		case "user":
			c.Target.User = v
		case "port":
			c.Target.Port = v
		case "proxyjump":
			c.Target.ProxyJump = v
		case "proxycommand":
			c.Target.ProxyCommand = v
		}
	}
	return c, true
}
