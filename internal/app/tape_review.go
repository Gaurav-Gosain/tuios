package app

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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

// tapeReviewRows is how many lines of the tape the dialog shows on the screen
// it is actually drawn on: the preferred viewport, or fewer when the screen is
// short. The header, the content box's own frame and the footer come off first.
func (m *OS) tapeReviewRows() int {
	return m.dialogRows(tapeReviewViewportRows, 14)
}

// tapeReviewMaxScroll is the furthest the content can scroll.
func (m *OS) tapeReviewMaxScroll() int {
	if m.TapeReview == nil {
		return 0
	}
	n := len(m.TapeReview.tapeContentLines())
	rows := m.tapeReviewRows()
	if n <= rows {
		return 0
	}
	return n - rows
}

// RenderTapeReview renders the review/trust dialog box. The caller centers it.
func (m *OS) RenderTapeReview() string {
	r := m.TapeReview
	if r == nil {
		return ""
	}

	pal := theme.UI()
	bg := pal.Surface
	width := m.panelWidth(tapeReviewWidth)
	rows := m.tapeReviewRows()

	label := func(s string) string { return overlay.Style(bg).Foreground(pal.FgDim).Render(s) }
	value := func(s string) string { return overlay.Style(bg).Foreground(pal.Fg).Render(s) }

	var lines []string
	// Header: path + trust status. A long path keeps its tail, which is the
	// part that says which project this is.
	lines = append(lines, label("path  ")+value(tailFit(shortTapePath(r.Path), max(width-6, 1))))
	lines = append(lines, label("trust ")+tapeStatusLabel(r.Status, r.Changed, bg, pal))

	// What running it will do, from a cheap header parse (no execution).
	lines = append(lines, label("runs  ")+value(overlay.Truncate(tapeRunSummary(r.Header, r.Dir), max(width-6, 1))))
	if r.Status == trust.StatusIneligible && r.Reason != "" {
		lines = append(lines, overlay.Style(bg).Foreground(pal.Warning).Render("ignored: "+r.Reason))
	}
	lines = append(lines, "")

	// Body: the full tape content, scrollable. It rests on the Card step, the
	// same one a key chip uses, so the tape reads as quoted material without a
	// second border inside the panel.
	if r.Status != trust.StatusIneligible {
		content := r.tapeContentLines()
		start := min(r.Scroll, m.tapeReviewMaxScroll())
		end := min(start+rows, len(content))
		code := pal.Card
		if end <= start {
			lines = append(lines, overlay.Fill(
				overlay.Style(code).Foreground(theme.Readable(pal.FgMute, code)).Italic(true).Render(" (empty)"), width, code))
		}
		for i := start; i < end; i++ {
			lines = append(lines, overlay.Fill(
				overlay.Style(code).Foreground(pal.Fg).Render(" "+overlay.Truncate(content[i], width-2)), width, code))
		}
		if len(content) > rows {
			lines = append(lines, overlay.Style(bg).Foreground(pal.FgDim).Italic(true).
				Render(fmt.Sprintf("  lines %d-%d of %d", start+1, end, len(content))))
		}
	}

	panel := overlay.Panel{
		Title: "project tape",
		Width: width,
		Body:  clipStyledLines(strings.Join(lines, "\n"), width),
		Hints: tapeReviewHints(r),
	}
	out, _ := panel.Render(pal)
	return out
}

// tapeReviewWidth is the dialog's preferred inner width: enough for a tape's
// own lines without wrapping the ones that matter.
const tapeReviewWidth = 72

// tapeReviewHints are the actions the dialog offers for its current status.
// They are the point of the dialog, so every one of them is named at every
// width and the panel wraps them rather than dropping any.
func tapeReviewHints(r *TapeReviewState) []overlay.Hint {
	switch r.Status {
	case trust.StatusIneligible:
		return []overlay.Hint{{Key: "esc", Label: "dismiss"}}
	case trust.StatusTrusted:
		return []overlay.Hint{
			{Key: "r", Label: "run"},
			{Key: "n", Label: "revoke trust"},
			{Key: "esc", Label: "close"},
		}
	default:
		return []overlay.Hint{
			{Key: "r", Label: "run once"},
			{Key: "t", Label: "trust and run"},
			{Key: "n", Label: "never"},
			{Key: "esc", Label: "not now"},
		}
	}
}

// tailFit shortens s from the left, keeping its last width cells behind an
// ellipsis. Paths read from the right.
func tailFit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 3 {
		return string(runes[len(runes)-width:])
	}
	return "..." + string(runes[len(runes)-(width-3):])
}

// tapeStatusLabel renders the trust status in the status token that matches it.
func tapeStatusLabel(status trust.Status, changed bool, bg color.Color, pal overlay.Palette) string {
	word, fg := "untrusted", pal.Warning
	switch {
	case status == trust.StatusTrusted:
		word, fg = "trusted", pal.Success
	case status == trust.StatusIneligible:
		word, fg = "ineligible", pal.Warn
	case changed:
		word = "untrusted (changed since you trusted it)"
	}
	return overlay.Style(bg).Foreground(fg).Render(word)
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
