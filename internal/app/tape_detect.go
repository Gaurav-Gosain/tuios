package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape/trust"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const tapeDetectDebounce = 400 * time.Millisecond

func (m *OS) tapeBannerDuration() time.Duration {
	return time.Duration(m.Settings.TapeBannerMS) * time.Millisecond
}

// CwdChangedMsg carries an OSC 7 working-directory change from a window's PTY
// writer goroutine to the Update loop, which decides (on the focused window
// only) whether to look for a project tape. It mirrors NotificationMsg: the VT
// callback runs off the render goroutine and cannot touch OS state directly.
type CwdChangedMsg struct {
	WindowID string
	Cwd      string
}

type tapeDebounceMsg struct {
	gen uint64
}

type tapeIndicator struct {
	active bool
	status trust.Status
	dir    string
}

type tapeDetectState struct {
	store       *trust.Store
	storeLoaded bool
	handled     map[string]bool
	pendingD    string
	gen         uint64
	indicator   tapeIndicator
}

func listenCwdChangeUntil(stop <-chan struct{}, ch chan CwdChangedMsg) tea.Cmd {
	return listenOnce(stop, ch, func(msg CwdChangedMsg) tea.Msg { return msg })
}

func (m *OS) ensureCwdChangeChan() chan CwdChangedMsg {
	if m.PendingCwdChange == nil {
		m.PendingCwdChange = make(chan CwdChangedMsg, 16)
	}
	return m.PendingCwdChange
}

func (m *OS) setupCwdWatch(window *terminal.Window) {
	if window == nil {
		return
	}
	ch := m.ensureCwdChangeChan()
	id := window.ID
	window.LockIO()
	window.CwdFunc = func(cwd string) {
		select {
		case ch <- CwdChangedMsg{WindowID: id, Cwd: cwd}:
		default:
		}
	}
	window.UnlockIO()
}

func (m *OS) tapeAutorunMode() string {
	if env := strings.TrimSpace(os.Getenv("TUIOS_TAPE_AUTORUN")); env != "" {
		if slices.Contains(config.TapeAutorunModes, env) {
			return env
		}
	}
	if m.UserConfig != nil && m.UserConfig.Tape.Autorun != "" {
		return m.UserConfig.Tape.Autorun
	}
	return config.TapeAutorunAsk
}

func (m *OS) tapeAutorunEnabled() bool {
	return m.tapeAutorunMode() != config.TapeAutorunOff
}

func (m *OS) onCwdChange(msg CwdChangedMsg) tea.Cmd {
	m.recordWindowCwd(msg.WindowID, msg.Cwd)

	if !m.tapeAutorunEnabled() {
		return nil
	}

	if m.ScriptMode {
		return nil
	}

	focused := m.GetFocusedWindow()
	if focused == nil || focused.ID != msg.WindowID {
		return nil
	}

	dir, ok := localCwdPath(msg.Cwd)
	if !ok {
		return nil
	}

	m.tapeDetect.gen++
	m.tapeDetect.pendingD = dir
	gen := m.tapeDetect.gen
	return tea.Tick(tapeDetectDebounce, func(time.Time) tea.Msg {
		return tapeDebounceMsg{gen: gen}
	})
}

func (m *OS) handleTapeDebounce(gen uint64) {
	if gen != m.tapeDetect.gen {
		return
	}
	m.evaluateTapeDir(m.tapeDetect.pendingD)
}

func localCwdPath(raw string) (string, bool) {
	path, host, ok := session.ParseCwdAnnouncement(raw)
	if !ok || host != "" {
		return "", false
	}
	return path, true
}

func isLocalHost(host string) bool {
	return session.IsLocalHostName(host)
}

func (m *OS) evaluateTapeDir(dir string) {
	if !m.tapeAutorunEnabled() || dir == "" {
		return
	}

	store := m.ensureTapeTrust()
	if store == nil {
		return
	}
	if m.tapeDetect.handled == nil {
		m.tapeDetect.handled = make(map[string]bool)
	}

	tapePath := filepath.Join(dir, trust.TapeFileName)

	info, err := os.Lstat(tapePath)
	if err != nil || info.IsDir() {
		m.tapeDetect.indicator = tapeIndicator{}
		return
	}

	res, checkErr := store.Check(tapePath)
	if checkErr != nil {
		m.LogInfo("tape: %v", checkErr)
	}

	if res.Status == trust.StatusDenied {
		m.tapeDetect.indicator = tapeIndicator{}
		m.tapeDetect.handled[dir] = true
		return
	}

	m.tapeDetect.indicator = tapeIndicator{active: true, status: res.Status, dir: dir}

	if m.tapeAutorunMode() == config.TapeAutorunAuto && res.Status == trust.StatusTrusted {
		if m.tapeDetect.handled[dir] {
			return
		}
		m.tapeDetect.handled[dir] = true
		m.ShowNotification("Running trusted project tape", "info", 2*time.Second)
		m.runProjectTape(res.Content, dir)
		return
	}

	if m.tapeDetect.handled[dir] {
		return
	}
	m.tapeDetect.handled[dir] = true

	if m.tapeAutoReviewEnabled() && (res.Status == trust.StatusUntrusted || res.Status == trust.StatusTrusted) {
		m.openTapeReviewForDir(dir)
		return
	}

	message, notifType := tapeBanner(res, &m.Settings)
	m.ShowNotification(message, notifType, m.tapeBannerDuration())
}

func (m *OS) tapeStatusShown() bool {
	return m.UserConfig == nil || m.UserConfig.Tape.TapeStatusShown()
}

func (m *OS) tapeAutoReviewEnabled() bool {
	return m.UserConfig != nil && m.UserConfig.Tape.AutoReview
}

func tapeBanner(res trust.Result, s *config.Settings) (string, string) {
	hint := s.LeaderKey + " T t"
	switch res.Status {
	case trust.StatusTrusted:
		return "Project tape ready (trusted). Press " + hint + " to run.", "info"
	case trust.StatusUntrusted:
		return "Project tape found (untrusted). Press " + hint + " to review.", "info"
	case trust.StatusIneligible:
		reason := res.Reason
		if reason == "" {
			reason = "failed a safety check"
		}
		return "Project tape ignored: " + reason, "warning"
	default:
		return "Project tape found. Press " + hint + " to review.", "info"
	}
}

func (m *OS) ensureTapeTrust() *trust.Store {
	if m.tapeDetect.store != nil {
		return m.tapeDetect.store
	}
	if m.tapeDetect.storeLoaded {
		return nil
	}
	m.tapeDetect.storeLoaded = true

	store, err := trust.Load()
	if err != nil {
		m.LogWarn("tape trust store: %v", err)
		return nil
	}
	if store.Warning != "" {
		m.LogWarn("%s", store.Warning)
		m.ShowNotification(store.Warning, "warning", m.tapeBannerDuration())
	}
	m.tapeDetect.store = store
	return store
}

func (m *OS) tapeDockBadge() string {
	if !m.tapeDetect.indicator.active {
		return ""
	}
	switch m.tapeDetect.indicator.status {
	case trust.StatusTrusted:
		return "tape " + tapeGlyphTrusted
	case trust.StatusUntrusted:
		return "tape " + tapeGlyphUntrusted
	case trust.StatusIneligible:
		return "tape " + tapeGlyphIneligible
	default:
		return ""
	}
}

const (
	tapeGlyphTrusted    = "✓"
	tapeGlyphUntrusted  = "?"
	tapeGlyphIneligible = "!"
)
