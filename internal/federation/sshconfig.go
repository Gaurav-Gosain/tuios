package federation

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
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
	ctx, cancel := context.WithTimeout(context.Background(), sshConfigTimeout)
	defer cancel()
	args := append(append([]string{"-G"}, h.SSHOptions...), "--", h.Addr)
	cmd := exec.CommandContext(ctx, SSHBinary(), args...)
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
		}
	}
	return c, true
}
