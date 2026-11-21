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

type configWatchHub struct {
	mu            sync.Mutex
	watcher       *config.Watcher
	subs          map[chan tea.Msg]chan ThemeFilesReloadedMsg
	themeProblems []string
}

// ThemeFilesReloadedMsg says the theme and glyph files were read again and the
// result is in force. Problems are the theme files that could not be used.
type ThemeFilesReloadedMsg struct {
	Problems []string
}

var configWatch configWatchHub

var configWatchPath = config.GetConfigPath

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

func (h *configWatchHub) unsubscribe(ch chan tea.Msg) {
	h.mu.Lock()
	themes, held := h.subs[ch]
	if !held {
		h.mu.Unlock()
		return
	}
	delete(h.subs, ch)
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

type configWatchMsg struct {
	msg tea.Msg
}

func (m *OS) watchesConfig() bool {
	return m.Client != ClientUnknown && !m.ScriptMode && !m.LearnMode
}

func (m *OS) startConfigWatch() tea.Cmd {
	if !m.watchesConfig() || m.configReloads != nil {
		return nil
	}
	m.configReloads, m.themeReloads, m.stopConfigWatch = configWatch.subscribe()
	return tea.Batch(listenForConfigReload(m.configReloads), listenForThemeReload(m.themeReloads))
}

type themeWatchMsg struct {
	msg ThemeFilesReloadedMsg
}

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

func (m *OS) endConfigWatch() {
	if m.stopConfigWatch != nil {
		m.stopConfigWatch()
		m.stopConfigWatch = nil
	}
	m.configReloads, m.themeReloads = nil, nil
}
