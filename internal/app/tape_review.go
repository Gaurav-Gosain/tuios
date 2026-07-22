package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/tape/trust"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// tapeReviewViewportRows is how many lines of tape content the review dialog
// shows at once before scrolling. The trust store's size cap keeps the whole
// file viewable within a few scrolls.
const tapeReviewViewportRows = 16

// TapeReviewState holds the project-tape review/trust dialog. It is populated by
// a single trust-store read at open time; the same in-memory Content is what the
// dialog displays and, if the user approves, what runs. The file on disk is
// never re-read between review and execution, which is what makes approval
// TOCTOU-safe.
type TapeReviewState struct {
	Path    string
	Dir     string
	Status  trust.Status
	Hash    string
	Content []byte
	Reason  string // why an ineligible tape was rejected
	Header  tape.ProjectHeader
	// Changed is true when the path was trusted before but its content hash no
	// longer matches: the tape was edited since it was trusted.
	Changed bool
	Scroll  int
}

// OpenTapeReview opens the review/trust dialog for the project tape in the
// focused window's current directory. It performs a fresh single-read Check, so
// the content shown is the content that will run and a tape edited since the
// passive banner appeared shows up here as changed and untrusted.
//
// It is the deliberate, user-initiated surface (leader T t, the command palette,
// or the auto-mode fallback). A denied path never opens it.
func (m *OS) OpenTapeReview() {
	dir := m.tapeDetect.indicator.dir
	if !m.tapeDetect.indicator.active || dir == "" {
		m.ShowNotification("No project tape in the current directory", "info", config.NotificationDuration)
		return
	}
	if m.ScriptMode {
		m.ShowNotification("A tape is already running", "warning", config.NotificationDuration)
		return
	}
	m.openTapeReviewForDir(dir)
}

// openTapeReviewForDir builds and shows the dialog for a specific project root.
func (m *OS) openTapeReviewForDir(dir string) {
	store := m.ensureTapeTrust()
	if store == nil {
		m.ShowNotification("Tape trust store unavailable", "error", config.NotificationDuration)
		return
	}

	tapePath := filepath.Join(dir, trust.TapeFileName)
	res, err := store.Check(tapePath)
	if err != nil {
		m.LogInfo("tape review: %v", err)
	}

	if res.Status == trust.StatusDenied {
		// A denied path is inert and never prompts.
		m.ShowNotification("This project tape is denied (never for this path)", "info", config.NotificationDuration)
		return
	}

	changed := false
	if res.Status == trust.StatusUntrusted {
		if stored, ok := store.TrustedHash(res.Path); ok && stored != res.Hash {
			changed = true
		}
	}

	header, _ := tape.ParseProjectHeader(string(res.Content))

	m.TapeReview = &TapeReviewState{
		Path:    res.Path,
		Dir:     dir,
		Status:  res.Status,
		Hash:    res.Hash,
		Content: res.Content,
		Reason:  res.Reason,
		Header:  header,
		Changed: changed,
	}
	m.ShowTapeReview = true
}

// CloseTapeReview dismisses the dialog without acting.
func (m *OS) CloseTapeReview() {
	m.ShowTapeReview = false
	m.TapeReview = nil
}

// tapeReviewRunOnce runs the reviewed content without persisting trust.
func (m *OS) tapeReviewRunOnce() {
	r := m.TapeReview
	if r == nil {
		return
	}
	content, dir := r.Content, r.Dir
	m.CloseTapeReview()
	m.runProjectTape(content, dir)
}

// tapeReviewTrustAndRun persists trust for the reviewed (path, hash) pair and
// then runs the reviewed content.
func (m *OS) tapeReviewTrustAndRun() {
	r := m.TapeReview
	if r == nil {
		return
	}
	store := m.ensureTapeTrust()
	if store != nil {
		if err := store.Trust(r.Path, r.Hash); err != nil {
			m.ShowNotification("Tape: could not persist trust: "+err.Error(), "error", config.NotificationDuration*2)
		} else {
			m.tapeDetect.indicator.status = trust.StatusTrusted
			m.ShowNotification("Trusted "+shortTapePath(r.Path)+" (tip: set tape.autorun = \"auto\" to skip this next time)", "success", config.NotificationDuration*2)
		}
	}
	content, dir := r.Content, r.Dir
	m.CloseTapeReview()
	m.runProjectTape(content, dir)
}

// tapeReviewNever records a deny entry for the path and clears the indicator.
func (m *OS) tapeReviewNever() {
	r := m.TapeReview
	if r == nil {
		return
	}
	store := m.ensureTapeTrust()
	if store != nil {
		if err := store.Deny(r.Path); err != nil {
			m.ShowNotification("Tape: could not deny: "+err.Error(), "error", config.NotificationDuration*2)
		} else {
			m.ShowNotification("Denied "+shortTapePath(r.Path)+" (never for this path)", "info", config.NotificationDuration)
		}
	}
	m.tapeDetect.indicator = tapeIndicator{}
	if m.tapeDetect.handled == nil {
		m.tapeDetect.handled = make(map[string]bool)
	}
	m.tapeDetect.handled[r.Dir] = true
	m.CloseTapeReview()
}

// tapeReviewRevoke removes trust for a trusted tape, returning it to the
// untrusted-but-promptable state.
func (m *OS) tapeReviewRevoke() {
	r := m.TapeReview
	if r == nil {
		return
	}
	store := m.ensureTapeTrust()
	if store != nil {
		if err := store.Forget(r.Path); err != nil {
			m.ShowNotification("Tape: could not revoke trust: "+err.Error(), "error", config.NotificationDuration*2)
		} else {
			m.ShowNotification("Revoked trust for "+shortTapePath(r.Path), "info", config.NotificationDuration)
		}
	}
	m.tapeDetect.indicator.status = trust.StatusUntrusted
	m.CloseTapeReview()
}

// HandleTapeReviewInput handles a keypress while the review dialog is open. It
// returns true when the key was consumed.
func (m *OS) HandleTapeReviewInput(key string) bool {
	r := m.TapeReview
	if r == nil {
		return false
	}

	// Scrolling is available in every mode.
	switch key {
	case "up", "k":
		if r.Scroll > 0 {
			r.Scroll--
		}
		return true
	case "down", "j":
		if r.Scroll < m.tapeReviewMaxScroll() {
			r.Scroll++
		}
		return true
	case "esc", "q":
		m.CloseTapeReview()
		return true
	}

	if r.Status == trust.StatusIneligible {
		// An ineligible tape offers no run or trust option; only dismissal.
		return true
	}

	if r.Status == trust.StatusTrusted {
		switch key {
		case "r", "enter":
			m.tapeReviewRunOnce()
			return true
		case "n":
			m.tapeReviewRevoke()
			return true
		}
		return true
	}

	// Untrusted (including changed-since-trusted).
	switch key {
	case "r":
		m.tapeReviewRunOnce()
		return true
	case "t", "enter":
		m.tapeReviewTrustAndRun()
		return true
	case "n":
		m.tapeReviewNever()
		return true
	}
	return true
}

// tapeContentLines returns the reviewed content split into display lines.
func (r *TapeReviewState) tapeContentLines() []string {
	if len(r.Content) == 0 {
		return nil
	}
	return strings.Split(strings.ReplaceAll(string(r.Content), "\t", "    "), "\n")
}

// tapeReviewMaxScroll is the furthest the content can scroll.
func (m *OS) tapeReviewMaxScroll() int {
	if m.TapeReview == nil {
		return 0
	}
	n := len(m.TapeReview.tapeContentLines())
	if n <= tapeReviewViewportRows {
		return 0
	}
	return n - tapeReviewViewportRows
}

// RenderTapeReview renders the review/trust dialog box. The caller centers it.
func (m *OS) RenderTapeReview() string {
	r := m.TapeReview
	if r == nil {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Foreground(theme.WelcomeTitle()).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(theme.WelcomeSubtitle())
	pathStyle := lipgloss.NewStyle().Foreground(theme.WelcomeText())
	dimStyle := lipgloss.NewStyle().Foreground(theme.HelpGray())
	keyStyle := lipgloss.NewStyle().Foreground(theme.HelpKeyBadge()).Bold(true)
	codeStyle := lipgloss.NewStyle().Foreground(theme.WelcomeText())
	warnStyle := lipgloss.NewStyle().Foreground(theme.NotificationWarning())

	var lines []string
	lines = append(lines, titleStyle.Render("Project Tape"))
	lines = append(lines, "")

	// Header: path + trust status.
	lines = append(lines, labelStyle.Render("Path:  ")+pathStyle.Render(shortTapePath(r.Path)))
	lines = append(lines, labelStyle.Render("Trust: ")+tapeStatusLabel(r.Status, r.Changed))

	// What running it will do, from a cheap header parse (no execution).
	lines = append(lines, labelStyle.Render("Runs:  ")+pathStyle.Render(tapeRunSummary(r.Header, r.Dir)))
	if r.Status == trust.StatusIneligible && r.Reason != "" {
		lines = append(lines, warnStyle.Render("Ignored: "+r.Reason))
	}
	lines = append(lines, "")

	// Body: the full tape content, scrollable.
	if r.Status != trust.StatusIneligible {
		content := r.tapeContentLines()
		start := min(r.Scroll, m.tapeReviewMaxScroll())
		end := min(start+tapeReviewViewportRows, len(content))
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.HelpBorder()).
			Padding(0, 1)
		var body []string
		for i := start; i < end; i++ {
			body = append(body, codeStyle.Render(truncateString(content[i], 72)))
		}
		if len(body) == 0 {
			body = append(body, dimStyle.Render("(empty)"))
		}
		lines = append(lines, box.Render(strings.Join(body, "\n")))
		if len(content) > tapeReviewViewportRows {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("lines %d-%d of %d  (↑/↓ scroll)", start+1, end, len(content))))
		}
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(tapeReviewFooter(r, keyStyle, dimStyle)))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.HelpBorder()).
		Padding(1, 2).
		Background(theme.LogViewerBg())
	return boxStyle.Render(content)
}

// tapeReviewFooter builds the action hint line for the dialog's current status.
func tapeReviewFooter(r *TapeReviewState, keyStyle, dimStyle lipgloss.Style) string {
	switch r.Status {
	case trust.StatusIneligible:
		return keyStyle.Render("Esc") + " Dismiss"
	case trust.StatusTrusted:
		return keyStyle.Render("r") + " Run   " +
			keyStyle.Render("n") + " Revoke trust   " +
			keyStyle.Render("Esc") + " Close"
	default:
		return keyStyle.Render("r") + " Run once   " +
			keyStyle.Render("t") + " Trust and run   " +
			keyStyle.Render("n") + " Never   " +
			keyStyle.Render("Esc") + " Not now"
	}
}

// tapeStatusLabel renders a colored trust-status word for the dialog header.
func tapeStatusLabel(status trust.Status, changed bool) string {
	switch status {
	case trust.StatusTrusted:
		return lipgloss.NewStyle().Foreground(theme.NotificationSuccess()).Render("trusted")
	case trust.StatusIneligible:
		return lipgloss.NewStyle().Foreground(theme.NotificationError()).Render("ineligible")
	default:
		if changed {
			return lipgloss.NewStyle().Foreground(theme.NotificationWarning()).Render("untrusted (changed since you trusted it)")
		}
		return lipgloss.NewStyle().Foreground(theme.NotificationWarning()).Render("untrusted")
	}
}

// tapeRunSummary describes, from the parsed header alone, what running the tape
// will do. It executes nothing.
func tapeRunSummary(h tape.ProjectHeader, dir string) string {
	if h.Scope == tape.ScopeCurrent {
		return "in the current session (Scope current)"
	}
	name := h.Session
	if name == "" {
		name = sanitizeSessionName(filepath.Base(dir))
	}
	if name == "" {
		name = "project"
	}
	return "session \"" + name + "\""
}

// shortTapePath abbreviates a home-rooted path with ~ for the dialog header.
func shortTapePath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel := strings.TrimPrefix(path, home); rel != path {
			return "~" + rel
		}
	}
	return path
}
