package session

import (
	"errors"
	"os"
	"strconv"
	"sync"
	"time"
)

// The fallback of a live handoff.
//
// The image that adopts the panes is the only process left holding them: the
// one that handed them over has been replaced by the exec, and the command
// that asked for the upgrade holds no descriptor. So when adoption fails,
// panics or hangs, this image re-execs the binary the previous image ran (kept
// at its start, see handoff_prev.go) with the same manifest, the same
// command line and the same environment. That binary reads the manifest it
// wrote and adopts the panes as it would have.
//
// The fallback is possible only until the panes are read and the sockets
// served, which is why adoption disarms it just before. An image started as a
// fallback does not fall back again: it carries handoffFallbackEnv, and on
// failure it ends, and the panes hang up as they would in a crash.

// handoffFallbackEnv marks an image started as a fallback.
const handoffFallbackEnv = "TUIOS_HANDOFF_FALLBACK"

// adoptWatchdogDefault is how long adoption may take before the fallback
// runs. An adoption takes well under a second.
const adoptWatchdogDefault = 30 * time.Second

// adoptFallback is the fallback of one adoption.
type adoptFallback struct {
	mu         sync.Mutex
	state      int // 0 armed, 1 disarmed, 2 falling back
	manifestFD int
	path       string
	fds        []int
	// held keeps every wrapper around an inherited descriptor reachable: an
	// os.File that is collected closes its descriptor, and the fallback
	// needs every one of them.
	held []any
}

// prepare records what the fallback re-execs and hands on, from the manifest.
func (f *adoptFallback) prepare(m *handoffManifest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.path = m.Daemon.Fallback
	f.fds = append(f.fds[:0], f.manifestFD)
	for _, fd := range m.Daemon.Listeners {
		f.fds = append(f.fds, fd)
	}
	if m.Daemon.LockFD >= 0 {
		f.fds = append(f.fds, m.Daemon.LockFD)
	}
	for _, p := range m.Panes {
		f.fds = append(f.fds, p.FD)
	}
}

// keep holds v for as long as the fallback may run.
func (f *adoptFallback) keep(v any) {
	f.mu.Lock()
	f.held = append(f.held, v)
	f.mu.Unlock()
}

// disarm passes the point of no return. It reports false when the fallback
// has already started.
func (f *adoptFallback) disarm() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == 2 {
		return false
	}
	f.state = 1
	f.held = nil
	return true
}

// watch runs the fallback if adoption has not disarmed it within d.
func (f *adoptFallback) watch(d time.Duration) (stop func()) {
	t := time.AfterFunc(d, func() {
		LogError("Live upgrade: adopting the panes took longer than %s", d)
		f.reexec(errors.New("the adoption watchdog fired"))
	})
	return func() { t.Stop() }
}

// reexec execs the previous binary to adopt the panes. It returns only when
// that cannot be done, with every descriptor where it was.
func (f *adoptFallback) reexec(why error) {
	f.mu.Lock()
	if f.state != 0 {
		f.mu.Unlock()
		return
	}
	f.state = 2
	path, fds := f.path, f.fds
	f.mu.Unlock()
	if path == "" {
		LogError("Live upgrade: no fallback, the manifest was not read (%v). The panes end with this process", why)
		return
	}
	if os.Getenv(handoffFallbackEnv) != "" {
		LogError("Live upgrade: the fallback image could not adopt the panes either (%v). They end with this process", why)
		return
	}
	if err := rewindDescriptor(f.manifestFD); err != nil {
		LogError("Live upgrade: rewinding the manifest for the fallback: %v", err)
		return
	}
	argv := append([]string{path}, os.Args[1:]...)
	env := append(withoutEnv(os.Environ(), handoffFallbackEnv), handoffFallbackEnv+"=1")
	LogError("Live upgrade: falling back to %s (%v)", path, why)
	err := execHandoff(path, argv, env, fds, nil)
	LogError("Live upgrade: the fallback exec of %s failed: %v. The panes end with this process", path, err)
}

// withoutEnv is env without key.
func withoutEnv(env []string, key string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if len(kv) > len(key) && kv[:len(key)] == key && kv[len(key)] == '=' {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// adoptWatchdogTimeout is adoptWatchdogDefault, or the test's override.
func adoptWatchdogTimeout() time.Duration {
	if os.Getenv(handoffTestEnv) == "1" {
		if ms, err := strconv.Atoi(os.Getenv("TUIOS_TEST_HANDOFF_WATCHDOG_MS")); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return adoptWatchdogDefault
}

// handoffTestFailure fails an adoption on purpose when a test asks, in the
// image a handoff execs but never in its fallback: TUIOS_TEST_HANDOFF_FAIL is
// error, hang or panic.
func handoffTestFailure() error {
	if os.Getenv(handoffTestEnv) != "1" || os.Getenv(handoffFallbackEnv) != "" {
		return nil
	}
	switch os.Getenv("TUIOS_TEST_HANDOFF_FAIL") {
	case "error":
		return errors.New("adoption failed on purpose (TUIOS_TEST_HANDOFF_FAIL=error)")
	case "hang":
		select {}
	case "panic":
		panic("adoption panicked on purpose (TUIOS_TEST_HANDOFF_FAIL=panic)")
	}
	return nil
}
