package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// watchTempConfig points the hub at a file of the test's own and returns it.
func watchTempConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[appearance]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := configWatchPath
	configWatchPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { configWatchPath = prev })
	return path
}

// TestEveryClientKindFollowsTheConfigFile: a client of any kind subscribes to
// the file in Init, and a model nobody named or a tape run does not. The
// watcher used to be installed by bare `tuios` alone.
func TestEveryClientKindFollowsTheConfigFile(t *testing.T) {
	watchTempConfig(t)
	cfg := config.DefaultConfig()
	for _, kind := range []ClientKind{ClientLocal, ClientSSH, ClientBrowser} {
		t.Run(kind.String(), func(t *testing.T) {
			o := NewOS(OSOptions{Client: kind, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
			if o.startConfigWatch() == nil || o.configReloads == nil {
				t.Errorf("a %s client does not follow the config file", kind)
			}
			o.Cleanup()
		})
	}
	t.Run("unknown", func(t *testing.T) {
		o := NewOS(OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
		if o.startConfigWatch() != nil {
			t.Error("a model with no kind started a watcher; every test would")
		}
	})
	t.Run("tape", func(t *testing.T) {
		o := NewOS(OSOptions{Client: ClientLocal, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
		o.ScriptMode = true
		if o.startConfigWatch() != nil {
			t.Error("a tape run follows the file; an edit would change the script halfway")
		}
	})
}

// TestConfigEditReachesEverySession writes the file and expects every
// subscribed session to be handed the reload, through the listener Init arms,
// as the message the palette's own reload uses.
func TestConfigEditReachesEverySession(t *testing.T) {
	path := watchTempConfig(t)
	cfg := config.DefaultConfig()
	var models []*OS
	for range 2 {
		o := NewOS(OSOptions{Client: ClientSSH, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
		o.startConfigWatch()
		t.Cleanup(o.Cleanup)
		models = append(models, o)
	}
	if configWatch.watcher == nil {
		t.Fatal("two sessions subscribed and no watcher is open")
	}

	if err := os.WriteFile(path, []byte("[appearance]\nscroll_lines = 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var first []configWatchMsg
	for i, o := range models {
		done := make(chan struct{})
		var got any
		go func() {
			got = listenForConfigReload(o.configReloads)()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("session %d never heard the edit", i)
		}
		wrapped, ok := got.(configWatchMsg)
		if !ok {
			t.Fatalf("session %d got %T, want the watcher's wrapper", i, got)
		}
		reload, ok := wrapped.msg.(ConfigReloadedMsg)
		if !ok {
			t.Fatalf("session %d got %T inside the wrapper", i, wrapped.msg)
		}
		if reload.Config == nil || reload.Config.Appearance.ScrollLines != 7 {
			t.Errorf("session %d got a reload that does not carry the edit", i)
		}
		first = append(first, wrapped)
	}

	for i, o := range models {
		// Applying it re-arms the listener, so the next edit is heard too. The
		// command Update returns is run the way the runtime runs it, and one of
		// the messages it produces has to be the second edit.
		_, cmd := o.Update(first[i])
		if cmd == nil {
			t.Fatalf("session %d applied the reload and armed no listener for the next", i)
		}
		heard := make(chan tea.Msg, 8)
		runCmd(cmd, heard)
		if err := os.WriteFile(path, fmt.Appendf(nil, "[appearance]\nscroll_lines = %d\n", 8+i), 0o600); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(5 * time.Second)
	wait:
		for {
			select {
			case msg := <-heard:
				w, ok := msg.(configWatchMsg)
				if !ok {
					continue
				}
				if r, ok := w.msg.(ConfigReloadedMsg); ok && r.Config != nil && r.Config.Appearance.ScrollLines == 8+i {
					break wait
				}
				// An earlier edit this session had queued. The runtime would
				// apply it and re-arm, so do the same and keep waiting.
				_, next := o.Update(w)
				runCmd(next, heard)
			case <-deadline:
				t.Fatalf("session %d applied the reload and armed no listener for the next; the second edit was not heard", i)
			}
		}
	}
}

// runCmd runs a command the way the runtime does, batches included, and
// delivers what it produces on out.
func runCmd(cmd tea.Cmd, out chan<- tea.Msg) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				runCmd(c, out)
			}
			return
		}
		out <- msg
	}()
}

// TestLastSessionOutStopsTheWatcher: one watcher per process, closed when the
// last session leaves.
func TestLastSessionOutStopsTheWatcher(t *testing.T) {
	watchTempConfig(t)
	cfg := config.DefaultConfig()
	a := NewOS(OSOptions{Client: ClientLocal, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	b := NewOS(OSOptions{Client: ClientBrowser, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	a.startConfigWatch()
	b.startConfigWatch()
	a.Cleanup()
	if configWatch.watcher == nil {
		t.Fatal("the first session out closed the watcher under the second")
	}
	b.Cleanup()
	if configWatch.watcher != nil {
		t.Fatal("the last session out left the watcher open")
	}
}

// chanClosed reports whether a subscription's channel was closed. An open one
// has nothing to read and says so at once.
func chanClosed(ch <-chan tea.Msg) bool {
	select {
	case _, ok := <-ch:
		return !ok
	default:
		return false
	}
}

// TestReleasingASubscriptionTwiceIsHarmless holds the hub's release to the ways
// a cleanup runs more than once or late. A cleanup that runs twice must never
// take a client down, and must not touch the sessions that are still there:
//
//   - the same release twice in a row (a model cleaned up by two owners);
//   - the same release from several goroutines at once;
//   - a release that comes after the watcher stopped and a new session
//     started it again (a stale cleanup must not close the new session's
//     channels, or stop its watcher);
//   - a channel the hub never held.
func TestReleasingASubscriptionTwiceIsHarmless(t *testing.T) {
	watchTempConfig(t)
	t.Run("twice in a row", func(t *testing.T) {
		var h configWatchHub
		_, _, relA := h.subscribe()
		chB, _, relB := h.subscribe()
		relA()
		relA()
		if chanClosed(chB) || h.watcher == nil {
			t.Fatal("the second release of one session took the other down")
		}
		relB()
	})
	t.Run("at once from several goroutines", func(t *testing.T) {
		var h configWatchHub
		_, _, rel := h.subscribe()
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(rel)
		}
		wg.Wait()
		if h.watcher != nil || len(h.subs) != 0 {
			t.Fatal("the last release did not leave the hub empty")
		}
	})
	t.Run("late, after the watcher started again", func(t *testing.T) {
		var h configWatchHub
		_, _, old := h.subscribe()
		old()
		if h.watcher != nil {
			t.Fatal("the last session out left the watcher open")
		}
		chNew, _, relNew := h.subscribe()
		old()
		if chanClosed(chNew) || h.watcher == nil {
			t.Fatal("a stale release closed the new session or stopped its watcher")
		}
		relNew()
		if h.watcher != nil {
			t.Fatal("the new session's own release did not stop the watcher")
		}
	})
	t.Run("a channel the hub never held", func(t *testing.T) {
		var h configWatchHub
		ch, _, rel := h.subscribe()
		h.unsubscribe(make(chan tea.Msg, 1))
		if chanClosed(ch) || h.watcher == nil {
			t.Fatal("a release of a stranger took a session down")
		}
		rel()
	})
	t.Run("OS.Cleanup twice", func(t *testing.T) {
		cfg := config.DefaultConfig()
		o := NewOS(OSOptions{Client: ClientBrowser, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
		o.startConfigWatch()
		o.Cleanup()
		o.Cleanup()
		if configWatch.watcher != nil {
			t.Fatal("the cleanup left the watcher open")
		}
	})
}
