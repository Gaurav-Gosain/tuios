//go:build !windows

package tmuxcompat

import (
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// wait-for: tmux's channels, between calls of the shim.
//
// tmux keeps a channel in its server. Each shim call is a process of its own,
// so a channel is a directory in the shim's runtime directory, private to
// the user, and the calls coordinate under an flock on a file in it:
//
//   - wait-for CHANNEL listens on a unix socket in the channel's directory,
//     named w and the time, and blocks until something connects. A channel
//     signalled while nobody waited carries a woken file, and the next wait
//     takes it and returns at once, as in tmux.
//   - wait-for -S connects to every waiter's socket, which wakes it, and
//     removes the sockets. With no waiter it leaves the woken file.
//   - wait-for -L takes the lock (the locked file), or queues a socket named
//     l and the time, and blocks. wait-for -U hands the lock to the oldest
//     queued locker by connecting to it, or frees it.
//
// The waiter listens before it lets go of the flock, so a signal cannot fall
// between its check and its wait. A socket whose process died refuses the
// connection, and is removed as stale. The paths are short, since a unix
// socket path is capped near 104 bytes: tmux/w/HASH/lTIME.

// channelDir is the directory of a channel, named by a hash of the channel,
// so any name is a short, safe path.
func (s *Shim) channelDir(channel string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(channel))
	return filepath.Join(s.Dir, "w", fmt.Sprintf("%08x", h.Sum32()))
}

// lockChannel takes the channel's flock and returns its release.
func lockChannel(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// listenIn makes a socket in dir to be woken on, its name kind (w or l) and
// the time in base 36, so lockers are served in the order they queued.
func listenIn(dir string, kind byte) (net.Listener, string, error) {
	for {
		stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
		path := filepath.Join(dir, string(kind)+strings.Repeat("0", max(0, 13-len(stamp)))+stamp)
		l, err := net.Listen("unix", path)
		if errors.Is(err, syscall.EADDRINUSE) {
			continue
		}
		return l, path, err
	}
}

// wake connects to the socket at path, which wakes the call waiting on it.
// It reports false for a socket nobody listens on any more.
func wake(path string) bool {
	c, err := net.DialTimeout("unix", path, time.Second)
	_ = os.Remove(path)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// sockets lists the sockets of kind queued in dir, oldest first.
func sockets(dir string, kind byte) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Type()&os.ModeSocket != 0 && e.Name()[0] == kind {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(out)
	return out
}

// waitFor answers wait-for: wait on a channel, signal it (-S), or lock (-L)
// and unlock (-U) it.
func (s *Shim) waitFor(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) != 1 {
		return OutcomeError, nil, errors.New("wait-for: give one channel")
	}
	if s.Dir == "" {
		return OutcomeError, nil, errors.New("wait-for: the tmux shim has no runtime directory to keep channels in")
	}
	channel := p.Args[0]
	dir := s.channelDir(channel)
	for _, d := range []string{s.Dir, filepath.Dir(dir), dir} {
		if err := EnsureDir(d); err != nil {
			return OutcomeError, nil, err
		}
	}
	unlock, err := lockChannel(dir)
	if err != nil {
		return OutcomeError, nil, fmt.Errorf("wait-for: %w", err)
	}
	woken := filepath.Join(dir, "woken")
	locked := filepath.Join(dir, "locked")
	switch {
	case p.Has('S'):
		defer unlock()
		woke := false
		for _, w := range sockets(dir, 'w') {
			woke = wake(w) || woke
		}
		if !woke {
			if err := os.WriteFile(woken, nil, 0o600); err != nil {
				return OutcomeError, nil, err
			}
		}
		return OutcomeOK, nil, nil
	case p.Has('U'):
		defer unlock()
		if _, err := os.Stat(locked); err != nil {
			return OutcomeError, nil, fmt.Errorf("channel %s not locked", channel)
		}
		for _, l := range sockets(dir, 'l') {
			if wake(l) {
				// The lock passes to that locker and stays taken.
				return OutcomeOK, nil, nil
			}
		}
		_ = os.Remove(locked)
		return OutcomeOK, nil, nil
	case p.Has('L'):
		if _, err := os.Stat(locked); errors.Is(err, os.ErrNotExist) {
			defer unlock()
			if err := os.WriteFile(locked, nil, 0o600); err != nil {
				return OutcomeError, nil, err
			}
			return OutcomeOK, nil, nil
		}
		return block(dir, 'l', unlock)
	}
	if err := os.Remove(woken); err == nil {
		unlock()
		return OutcomeOK, nil, nil
	}
	return block(dir, 'w', unlock)
}

// block queues a socket of kind in dir, lets go of the channel's flock, and
// waits for a connection on it.
func block(dir string, kind byte, unlock func()) (string, []string, error) {
	l, path, err := listenIn(dir, kind)
	unlock()
	if err != nil {
		return OutcomeError, nil, fmt.Errorf("wait-for: %w", err)
	}
	defer func() {
		_ = l.Close()
		_ = os.Remove(path)
	}()
	c, err := l.Accept()
	if err != nil {
		return OutcomeError, nil, fmt.Errorf("wait-for: %w", err)
	}
	_ = c.Close()
	return OutcomeOK, nil, nil
}
