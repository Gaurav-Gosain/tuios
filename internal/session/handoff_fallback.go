package session

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

const handoffFallbackEnv = "TUIOS_HANDOFF_FALLBACK"

const adoptWatchdogDefault = 30 * time.Second

type adoptFallback struct {
	mu         sync.Mutex
	state      int
	manifestFD int
	path       string
	fds        []int
	held       []any
}

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
	if m.Daemon.FallbackFD >= 0 {
		f.fds = append(f.fds, m.Daemon.FallbackFD)
	}
	for _, p := range m.Panes {
		f.fds = append(f.fds, p.FD)
	}
	if m.Daemon.Keeper.PID > 0 && m.Daemon.Keeper.FD >= 0 {
		f.fds = append(f.fds, m.Daemon.Keeper.FD)
	}
}

func (f *adoptFallback) keep(v any) {
	f.mu.Lock()
	f.held = append(f.held, v)
	f.mu.Unlock()
}

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

func (f *adoptFallback) watch(d time.Duration) (stop func()) {
	t := time.AfterFunc(d, func() {
		LogError("Live upgrade: adopting the panes took longer than %s", d)
		if f.reexec(errors.New("the adoption watchdog fired")) {
			os.Exit(1)
		}
	})
	return func() { t.Stop() }
}

func (f *adoptFallback) reexec(why error) bool {
	f.mu.Lock()
	if f.state != 0 {
		f.mu.Unlock()
		return false
	}
	f.state = 2
	path, fds := f.path, f.fds
	f.mu.Unlock()
	if path == "" {
		LogError("Live upgrade: no fallback, the manifest names no binary to fall back to (%v). The panes end with this process", why)
		return true
	}
	if os.Getenv(handoffFallbackEnv) != "" {
		LogError("Live upgrade: the fallback image could not adopt the panes either (%v). They end with this process", why)
		return true
	}
	if err := rewindDescriptor(f.manifestFD); err != nil {
		LogError("Live upgrade: rewinding the manifest for the fallback: %v", err)
		return true
	}
	argv := append([]string{path}, os.Args[1:]...)
	env := append(withOldCopies(withoutEnv(os.Environ(), handoffFallbackEnv)), handoffFallbackEnv+"=1")
	LogError("Live upgrade: falling back to %s (%v)", path, why)
	worktree.RemoveTemp(tempRemoveWait)
	err := execHandoff(path, argv, env, fds, nil)
	LogError("Live upgrade: the fallback exec of %s failed: %v. The panes end with this process", path, err)
	return true
}

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

func adoptWatchdogTimeout() time.Duration {
	if handoffTestOn() {
		if d := testknob.Millis("handoff_watchdog_ms"); d > 0 {
			return d
		}
	}
	return adoptWatchdogDefault
}

func handoffTestFailure() error {
	if !handoffTestOn() || os.Getenv(handoffFallbackEnv) != "" {
		return nil
	}
	switch testknob.String("handoff_fail") {
	case "error":
		return errors.New("adoption failed on purpose (handoff_fail=error)")
	case "hang":
		select {}
	case "panic":
		panic("adoption panicked on purpose (handoff_fail=panic)")
	}
	return nil
}

var (
	startedAsFallback = os.Getenv(handoffFallbackEnv) != ""
	startedByKeeper   = os.Getenv(handoffKeptEnv) != ""
)

const (
	crashAtStart       = "start"
	crashWhileAdopting = "adopt"
	crashServing       = "serve"
)

func handoffTestCrash(point string) {
	if !handoffTestOn() || startedByKeeper {
		return
	}
	want := testknob.String("handoff_crash")
	here := want == point && !startedAsFallback
	inTheFallback := want == "fallback" && point == crashWhileAdopting && startedAsFallback
	if !here && !inTheFallback {
		return
	}
	LogError("Live upgrade: crashing on purpose at %s (handoff_crash=%s)", point, want)
	if testknob.String("handoff_crash_mode") == "panic" {
		go func() { panic("the new image crashed on purpose (handoff_crash)") }()
		select {}
	}
	if self, err := os.FindProcess(os.Getpid()); err == nil {
		_ = self.Kill()
	}
	select {}
}
