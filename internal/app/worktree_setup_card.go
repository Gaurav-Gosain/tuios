package app

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// The worktree setup card: a small box at the top right of the pane area
// that shows the stages of the attached session's worktree setup. The
// checkout, then the setup with its last four lines, then the agent.
//
// It is drawn from the setup record that arrives with the session state, so
// it changes only when a state push does, and it arms no timer. A running
// setup's card stays up. A finished one goes on the person's next key, and a
// passed one also stops being drawn once it is setupCardPassedFor old, which
// the next frame for any other reason notices.

// setupCardPassedFor is how long after a pass the card still shows, when no
// key has hidden it before. It is read at render time only.
const setupCardPassedFor = time.Minute

// setupCardWidth is the card's inner width, at most.
const setupCardWidth = 56

// setupCardLayerID names the card's layer.
const setupCardLayerID = "worktree-setup-card"

// setupCardState is what the client keeps about the card: the setup a key
// hid, by its start time, and the layer last built.
type setupCardState struct {
	hidden int64
	key    string
	layer  *lipgloss.Layer
}

// setupCardRecord is the setup the card shows, nil when it shows none.
func (m *OS) setupCardRecord() *session.WorktreeSetup {
	wt := m.SessionWorktree
	if wt == nil || wt.Setup == nil {
		return nil
	}
	s := wt.Setup
	switch s.State {
	case session.SetupPending, session.SetupRunning:
		return s
	}
	if m.setupCard.hidden == s.StartedAt {
		return nil
	}
	if s.State == session.SetupPassed && s.FinishedAt > 0 && time.Since(time.Unix(0, s.FinishedAt)) > setupCardPassedFor {
		return nil
	}
	return s
}

// hideSetupCard hides a finished setup's card. A key is the person moving
// on, and the key still goes where it was going.
func (m *OS) hideSetupCard() {
	if s := m.setupCardRecord(); s != nil && s.State != session.SetupPending && s.State != session.SetupRunning {
		m.setupCard.hidden = s.StartedAt
	}
}

// renderSetupCard draws the card, nil when there is none to draw or no room.
func (m *OS) renderSetupCard() *lipgloss.Layer {
	s := m.setupCardRecord()
	if s == nil {
		return nil
	}
	region := m.pipRegion()
	inner := min(setupCardWidth, region.Dx()-4)
	if inner < 24 || region.Dy() < 6 {
		return nil
	}
	branch := ""
	if wt := m.SessionWorktree; wt != nil {
		branch = wt.Branch
	}
	pal := theme.UI()
	rows := setupCardRows(s, branch, inner, pal)
	x := region.Max.X - (inner + 2) - 1
	y := region.Min.Y + 1
	key := fmt.Sprintf("%d:%d:%d", x, y, inner) + "\x00" + strings.Join(rows, "\n")
	if m.setupCard.layer == nil || m.setupCard.key != key {
		m.setupCard.key = key
		m.setupCard.layer = lipgloss.NewLayer(strings.Join(rows, "\n")).X(x).Y(y).Z(config.ZIndexFloatingTop).ID(setupCardLayerID)
	}
	return m.setupCard.layer
}

// setupCardRows draws the card's rows: a framed box of inner width w.
func setupCardRows(s *session.WorktreeSetup, branch string, w int, pal overlay.Palette) []string {
	bg := pal.Surface
	label := func(stage string) string { return fmt.Sprintf("%-9s", stage) }
	line := func(mark string, ink color.Color, stage, text string, textInk color.Color) string {
		head := overlay.Style(bg).Foreground(overlay.Readable(ink, bg)).Render(mark) +
			overlay.Style(bg).Render(" ") +
			overlay.Style(bg).Foreground(pal.Fg).Render(label(stage))
		room := max(w-2-lipgloss.Width(head), 1)
		return overlay.Style(bg).Render(" ") + head + overlay.Style(bg).Foreground(textInk).Render(ansi.Truncate(text, room, overlay.Ellipsis()))
	}
	done, failed, working, waiting := setupMarks()

	var lines []string
	lines = append(lines, line(done, pal.Success, "Checkout", branch, pal.FgDim))

	switch s.State {
	case session.SetupPending, session.SetupRunning:
		lines = append(lines, line(working, pal.Busy, "Setup", s.Command, pal.FgDim))
	case session.SetupPassed:
		lines = append(lines, line(done, pal.Success, "Setup", "Passed"+setupTook(s), pal.FgDim))
	default:
		what := "Failed"
		if s.Exit != nil {
			what = fmt.Sprintf("Failed, exit %d", *s.Exit)
		}
		lines = append(lines, line(failed, pal.Warn, "Setup", what, pal.Warn))
	}
	tailInk := pal.FgDim
	for _, t := range s.Tail {
		lines = append(lines, overlay.Style(bg).Render("   ")+overlay.Style(bg).Foreground(tailInk).Render(ansi.Truncate(printableTitle(t), max(w-4, 1), overlay.Ellipsis())))
	}

	stage := "Agent"
	if s.Shell {
		stage = "Shell"
	}
	switch s.Agent {
	case session.SetupAgentStarted:
		lines = append(lines, line(done, pal.Success, stage, "Started", pal.FgDim))
	case session.SetupAgentNotStarted:
		lines = append(lines, line(failed, pal.Warn, stage, "Not started", pal.FgDim))
	default:
		lines = append(lines, line(waiting, pal.FgMute, stage, "Waits for the setup", pal.FgMute))
	}
	if s.Note != "" {
		for _, l := range strings.Split(ansi.Wordwrap(s.Note, max(w-4, 8), ""), "\n") {
			lines = append(lines, overlay.Style(bg).Render("   ")+overlay.Style(bg).Foreground(pal.FgMute).Render(l))
		}
	}

	footer := ""
	if s.State != session.SetupPending && s.State != session.SetupRunning {
		footer = "Press a key to hide"
	}
	box := overlay.Box{Title: "Worktree setup", Note: setupStateWord(s), Footer: footer, Width: w, Lines: lines}
	return box.Render(bg, pal)
}

// setupMarks are the stage marks: done, failed, working and waiting, in
// ASCII when the chrome asks for it.
func setupMarks() (done, failed, working, waiting string) {
	if overlay.UseASCII() {
		return "+", "x", "*", "-"
	}
	return "✓", "✗", "●", "○"
}

// setupStateWord is the word in the card's top border.
func setupStateWord(s *session.WorktreeSetup) string {
	switch s.State {
	case session.SetupPending:
		return "starting"
	case session.SetupRunning:
		if s.Async {
			return "running, agent started"
		}
		return "running"
	}
	return s.State
}

// setupTook is " in 12s" for a setup that has both times, "" otherwise.
func setupTook(s *session.WorktreeSetup) string {
	if s.StartedAt == 0 || s.FinishedAt < s.StartedAt {
		return ""
	}
	d := time.Duration(s.FinishedAt - s.StartedAt).Round(time.Second)
	if d < time.Second {
		return " in under 1s"
	}
	return " in " + d.String()
}
