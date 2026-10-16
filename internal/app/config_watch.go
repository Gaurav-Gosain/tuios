package app

import (
	"log"
	"path/filepath"
	"slices"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The config file watcher, for every client.
//
// The watcher is started here, by the model, in Init, so a client cannot be
// built without it: bare `tuios`, `tuios attach` (which is what
// startup.daemon = true turns bare `tuios` into), a session over SSH and a
// browser tab all get it. What it delivers is applied through
// ApplyReloadedConfig, which writes this session's settings and never the
// process globals, which is what makes it safe in a server that holds several
// sessions.
//
// One watcher per process, not per session. A server holds one session per
// connection, and an inotify instance per connection is a limit waiting to be
// hit. The first session to subscribe starts the watcher, the last to leave
// stops it, and every reload reaches every session.
//
// A model nobody named (ClientUnknown, which is what the tests and the
// benchmarks build) starts no watcher, and neither does tape playback: a
// scripted run must not change under an edit halfway through.

// configWatchHub fans one file watcher out to the sessions of this process.
type configWatchHub struct {
	mu      sync.Mutex
	watcher *config.Watcher
	// subs maps each session's config channel to its theme channel.
	subs map[chan tea.Msg]chan ThemeFilesReloadedMsg
	// themeProblems are the problems the last theme reload reported. A save
	// that leaves them as they were does not warn again, so one broken file
	// does not raise a warning on every save of another.
	themeProblems []string
}

// ThemeFilesReloadedMsg says the theme and glyph files were read again and the
// result is in force. Problems are the theme files that could not be used.
type ThemeFilesReloadedMsg struct {
	Problems []string
}

var configWatch configWatchHub

// configWatchPath resolves the file to follow. A variable so a test can point
// the hub at a file of its own instead of the user's.
var configWatchPath = config.GetConfigPath

// subscribe returns a channel the hub delivers reloads on and the function
// that ends the subscription. The channel holds one message: a reload that
// arrives while the last is still waiting replaces it, because the newest file
// is the only one worth applying.
func (h *configWatchHub) subscribe() (<-chan tea.Msg, <-chan ThemeFilesReloadedMsg, func()) {
	ch := make(chan tea.Msg, 1)
	themes := make(chan ThemeFilesReloadedMsg, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = make(map[chan tea.Msg]chan ThemeFilesReloadedMsg)
	}
	h.subs[ch] = themes
	if h.watcher == nil {
		h.start()
	}
	return ch, themes, func() { h.unsubscribe(ch) }
}

// start opens the watcher. Called with the lock held. A watcher that cannot be
// opened is logged once and the subscribers simply hear nothing, which is what
// the standalone client did before.
func (h *configWatchHub) start() {
	path, err := configWatchPath()
	if err != nil {
		log.Printf("Config watcher unavailable, edits need a restart: %v", err)
		return
	}
	dir := filepath.Dir(path)
	w, err := config.NewWatcherWithOptions(path, h.deliver, config.WatcherOptions{
		AssetDirs: []string{filepath.Join(dir, "themes"), filepath.Join(dir, "glyphs")},
		OnAssets:  h.reloadThemeFiles,
	})
	if err != nil {
		log.Printf("Config watcher unavailable, edits need a restart: %v", err)
		return
	}
	h.watcher = w
}

// deliver runs on the watcher goroutine and hands the reload to every session.
// A file that cannot be used is delivered too, as a failure, so the client can
// say why the save changed nothing.
func (h *configWatchHub) deliver(cfg *config.UserConfig, err error) {
	var msg tea.Msg
	if err != nil {
		msg = ConfigReloadFailedMsg{Err: err}
	} else {
		msg = ConfigReloadedMsg{Config: cfg}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
			continue
		default:
		}
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- msg:
		default:
		}
	}
}

// reloadThemeFiles runs on the watcher goroutine when a theme or glyph file
// changes. The files are read once for the process, since the theme is
// process-wide, and then every session is told to repaint. A session that has
// not taken the last message yet gets this one in its place.
func (h *configWatchHub) reloadThemeFiles() {
	problems := theme.ReloadThemeFiles()
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := ThemeFilesReloadedMsg{}
	if !slices.Equal(problems, h.themeProblems) {
		msg.Problems = problems
	}
	h.themeProblems = problems
	for _, ch := range h.subs {
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- msg:
		default:
		}
	}
}

// unsubscribe drops one session. The watcher is stopped outside the lock:
// its goroutine takes the lock to deliver, and a Stop that waited for it
// under the lock would wait for ever.
func (h *configWatchHub) unsubscribe(ch chan tea.Msg) {
	h.mu.Lock()
	themes := h.subs[ch]
	delete(h.subs, ch)
	// Closed so the listener the session armed returns instead of waiting on
	// a channel nothing will ever send on again.
	close(ch)
	close(themes)
	var w *config.Watcher
	if len(h.subs) == 0 {
		w, h.watcher = h.watcher, nil
	}
	h.mu.Unlock()
	if w != nil {
		w.Stop()
	}
}

// configWatchMsg is a reload the watcher delivered, as opposed to one the
// palette's "Reload config" row raised. The wrapper is what lets Update re-arm
// the listener for exactly the messages that came through it.
type configWatchMsg struct {
	msg tea.Msg
}

// watchesConfig reports whether this model follows the config file. The
// tests build models with no kind and get no watcher.
func (m *OS) watchesConfig() bool {
	return m.Client != ClientUnknown && !m.ScriptMode
}

// startConfigWatch subscribes this session to the file and returns the
// command that waits for the first delivery.
func (m *OS) startConfigWatch() tea.Cmd {
	if !m.watchesConfig() || m.configReloads != nil {
		return nil
	}
	m.configReloads, m.themeReloads, m.stopConfigWatch = configWatch.subscribe()
	return tea.Batch(listenForConfigReload(m.configReloads), listenForThemeReload(m.themeReloads))
}

// themeWatchMsg is a theme reload the watcher delivered. The wrapper is what
// lets Update re-arm the listener.
type themeWatchMsg struct {
	msg ThemeFilesReloadedMsg
}

// listenForThemeReload waits for one theme reload from the watcher.
func listenForThemeReload(ch <-chan ThemeFilesReloadedMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return themeWatchMsg{msg: msg}
	}
}

// listenForConfigReload waits for one delivery from the watcher.
func listenForConfigReload(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return configWatchMsg{msg: msg}
	}
}

// endConfigWatch ends this session's subscription. The last session out stops
// the watcher.
func (m *OS) endConfigWatch() {
	if m.stopConfigWatch != nil {
		m.stopConfigWatch()
		m.stopConfigWatch = nil
	}
	m.configReloads, m.themeReloads = nil, nil
}
