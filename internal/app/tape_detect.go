package app

import (
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape/trust"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// tapeDetectDebounce is how long the focused window's working directory must
// hold still before a project tape is evaluated. A `cd a && cd b && cd c` in a
// script, or a fast hop through directories, collapses to one evaluation at the
// destination instead of firing at every intermediate directory.
const tapeDetectDebounce = 400 * time.Millisecond

// tapeBannerDuration is how long the passive "project tape found" banner stays
// on screen. It is longer than an ordinary notification because it is
// informational, not a prompt, and must never steal focus.
const tapeBannerDuration = 8 * time.Second

// CwdChangedMsg carries an OSC 7 working-directory change from a window's PTY
// writer goroutine to the Update loop, which decides (on the focused window
// only) whether to look for a project tape. It mirrors NotificationMsg: the VT
// callback runs off the render goroutine and cannot touch OS state directly.
type CwdChangedMsg struct {
	WindowID string
	Cwd      string
}

// tapeDebounceMsg fires tapeDetectDebounce after a focused cwd change. The
// generation is compared against the latest change so that a burst of cd's only
// evaluates once, at the final directory.
type tapeDebounceMsg struct {
	gen uint64
}

// tapeIndicator is the passive dock badge state: which directory's tape is
// currently in view for the focused window, and its trust status. It carries no
// action and triggers no execution.
type tapeIndicator struct {
	active bool
	status trust.Status
	dir    string
}

// tapeDetectState holds all project-tape detection state on the OS. It lives
// entirely on the Update goroutine; nothing here is touched from a PTY
// goroutine (those only send CwdChangedMsg over the channel).
type tapeDetectState struct {
	store       *trust.Store
	storeLoaded bool
	// handled remembers directories already surfaced this run, so re-entering a
	// project repeats no banner. It is session memory only; the trust store, not
	// this map, is what persists across runs.
	handled   map[string]bool
	pendingD  string
	gen       uint64
	indicator tapeIndicator
}

// ListenForCwdChange waits for the next working-directory change and delivers it
// to the Update loop as a CwdChangedMsg.
func ListenForCwdChange(ch chan CwdChangedMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// ensureCwdChangeChan lazily creates the cwd-change channel. Called only on the
// Update goroutine (from Init and window setup), so the nil check needs no lock.
func (m *OS) ensureCwdChangeChan() chan CwdChangedMsg {
	if m.PendingCwdChange == nil {
		m.PendingCwdChange = make(chan CwdChangedMsg, 16)
	}
	return m.PendingCwdChange
}

// setupCwdWatch wires a window's OSC 7 working-directory callback to the
// cwd-change channel. The callback fires on the window's PTY writer goroutine,
// so it only does a non-blocking send; the actual work happens on the Update
// goroutine where OS state is owned.
func (m *OS) setupCwdWatch(window *terminal.Window) {
	if window == nil {
		return
	}
	ch := m.ensureCwdChangeChan()
	id := window.ID
	window.CwdFunc = func(cwd string) {
		select {
		case ch <- CwdChangedMsg{WindowID: id, Cwd: cwd}:
		default:
			// Channel full: drop. A missed cwd change only defers detection to
			// the next one; it never blocks the PTY reader.
		}
	}
}

// tapeAutorunMode returns the effective tape autorun mode, honoring the
// TUIOS_TAPE_AUTORUN environment override (useful for CI or demos) over the
// config, and falling back to the safe default.
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

// tapeAutorunEnabled reports whether detection should run at all. When off, no
// stat is done and no indicator is shown.
func (m *OS) tapeAutorunEnabled() bool {
	return m.tapeAutorunMode() != config.TapeAutorunOff
}

// onCwdChange handles a raw working-directory change. It filters to the focused
// window and, when the feature is enabled, schedules a debounced evaluation.
// It returns the debounce command (or nil), and is a no-op that returns nil for
// background windows, the off mode, and remote (SSH) directories.
func (m *OS) onCwdChange(msg CwdChangedMsg) tea.Cmd {
	if !m.tapeAutorunEnabled() {
		return nil
	}

	// Suppression during playback: while any tape is executing, the trigger
	// pipeline is disabled entirely, so a tape whose scripted shell cd's onward
	// cannot chain another run.
	if m.ScriptMode {
		return nil
	}

	// Focused window only: a background window changing directory (a build
	// script, a watcher) never triggers detection.
	focused := m.GetFocusedWindow()
	if focused == nil || focused.ID != msg.WindowID {
		return nil
	}

	dir, ok := localCwdPath(msg.Cwd)
	if !ok {
		// Unparsable or remote (non-local host): tuios cannot read or verify a
		// remote file, so it neither prompts nor scans.
		return nil
	}

	m.tapeDetect.gen++
	m.tapeDetect.pendingD = dir
	gen := m.tapeDetect.gen
	return tea.Tick(tapeDetectDebounce, func(time.Time) tea.Msg {
		return tapeDebounceMsg{gen: gen}
	})
}

// handleTapeDebounce evaluates the pending directory once the cwd has been
// stable for the debounce interval, unless a newer change superseded it.
func (m *OS) handleTapeDebounce(gen uint64) {
	if gen != m.tapeDetect.gen {
		// Superseded by a later cwd change; that one owns the evaluation.
		return
	}
	m.evaluateTapeDir(m.tapeDetect.pendingD)
}

// localCwdPath extracts a local filesystem path from an OSC 7 payload. OSC 7
// carries a file://host/path URI; a bare path is also accepted for shells that
// emit one. A non-empty, non-local host means a remote shell, which is ignored.
func localCwdPath(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if !strings.HasPrefix(raw, "file://") {
		// A bare path (no scheme). Only treat an absolute path as usable.
		if filepath.IsAbs(raw) {
			return filepath.Clean(raw), true
		}
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if host := u.Hostname(); host != "" && !isLocalHost(host) {
		return "", false
	}
	if u.Path == "" {
		return "", false
	}
	return filepath.Clean(u.Path), true
}

// isLocalHost reports whether an OSC 7 host refers to this machine.
func isLocalHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" {
		return true
	}
	if h, err := os.Hostname(); err == nil && strings.EqualFold(h, host) {
		return true
	}
	return false
}

// evaluateTapeDir checks whether dir carries a .tuios.tape and, if so, updates
// the passive indicator and (once per directory per run) shows a passive banner.
//
// This is the entire user-visible surface of stage 1. It stats the directory,
// and for an existing tape reads it once to hash and classify it. It never
// parses the tape as a program, never executes it, and never creates a session,
// window, or layout.
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

	// A plain stat, not a walk and not a read: is there a tape file here at all?
	info, err := os.Lstat(tapePath)
	if err != nil || info.IsDir() {
		// No tape in this directory: clear any stale indicator.
		m.tapeDetect.indicator = tapeIndicator{}
		return
	}

	// A tape exists. Check reads it exactly once, hashes it, and classifies it.
	res, checkErr := store.Check(tapePath)
	if checkErr != nil {
		m.LogInfo("tape: %v", checkErr)
	}

	if res.Status == trust.StatusDenied {
		// A denied path produces no prompt and no indicator until the user
		// clears it. Mark it handled so it is not re-evaluated needlessly.
		m.tapeDetect.indicator = tapeIndicator{}
		m.tapeDetect.handled[dir] = true
		return
	}

	// Reflect the current location in the dock badge on every evaluation.
	m.tapeDetect.indicator = tapeIndicator{active: true, status: res.Status, dir: dir}

	// Auto mode: a trusted, unedited tape runs automatically. Untrusted or
	// changed tapes never auto-run; they fall through to the passive indicator
	// and the review dialog exactly as in ask mode. The destination root is
	// marked handled before playback starts so an autorun can never chain.
	if m.tapeAutorunMode() == config.TapeAutorunAuto && res.Status == trust.StatusTrusted {
		if m.tapeDetect.handled[dir] {
			return
		}
		m.tapeDetect.handled[dir] = true
		m.ShowNotification("Running trusted project tape", "info", 2*time.Second)
		m.runProjectTape(res.Content, dir)
		return
	}

	// One banner per directory per run.
	if m.tapeDetect.handled[dir] {
		return
	}
	m.tapeDetect.handled[dir] = true

	message, notifType := tapeBanner(res)
	m.ShowNotification(message, notifType, tapeBannerDuration)
}

// tapeBanner returns the passive banner text and notification type for a check
// result. It is informational and never steals focus: it states what was found,
// its trust status, and how to act on it (open the review dialog). Nothing runs
// from the banner.
func tapeBanner(res trust.Result) (string, string) {
	// The tape prefix chord that opens the review dialog. The leader is
	// configurable, so read it rather than hard-coding ctrl+b.
	hint := config.LeaderKey + " T t"
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

// ensureTapeTrust lazily loads the trust store the first time detection needs
// it. A load failure is logged once; a store whose file failed its integrity
// checks surfaces its warning. Tests may pre-set m.tapeDetect.store to avoid
// touching the real XDG location.
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
		m.ShowNotification(store.Warning, "warning", tapeBannerDuration)
	}
	m.tapeDetect.store = store
	return store
}

// tapeDockBadge returns a short badge for the dock reflecting the focused
// window's project tape, or the empty string when there is none to show. It is
// passive: a status marker, not a control.
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

// Dock badge glyphs. Plain marks (not font-specific icons) so the badge is
// legible on any terminal.
const (
	tapeGlyphTrusted    = "✓" // check mark: trusted
	tapeGlyphUntrusted  = "?" // untrusted, reviewable in a later stage
	tapeGlyphIneligible = "!" // ineligible, cannot be trusted
)

// tapeIndicatorStatus exposes the current indicator status for tests and
// callers that need to assert what the passive surface is showing. The bool is
// false when no indicator is active.
func (m *OS) tapeIndicatorStatus() (trust.Status, bool) {
	if !m.tapeDetect.indicator.active {
		return trust.StatusUntrusted, false
	}
	return m.tapeDetect.indicator.status, true
}
