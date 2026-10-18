package app

import (
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/restartplan"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// overlayKindRestart is the restart dialog's overlay kind.
const overlayKindRestart = "restartdaemon"

// restartInnerWidth is the dialog's preferred inner width.
const restartInnerWidth = 66

// renderDaemonRestart draws the restart dialog in its phase: the plan and the
// question, the steps while they run, or the failure. The plan's words are
// restartplan.Lines, the ones `tuios restart-server` prints.
func (m *OS) renderDaemonRestart() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(restartInnerWidth, m.GetRenderWidth())
	r := &m.restart

	base := overlay.Style(bg)
	pad := base.Render("  ")
	var body []string
	line := func(s string) { body = append(body, overlay.Fill(s, width, bg)) }
	text := func(s string, ink color.Color, bold bool) {
		st := base.Foreground(ink).Bold(bold)
		for _, l := range wrapPlain(s, max(width-3, 1)) {
			line(pad + st.Render(l))
		}
	}

	var rows []string
	cancel := overlay.Hint{Key: "esc", Label: "cancel"}
	hints := []overlay.Hint{cancel}
	switch r.phase {
	case restartReading:
		text("Reading what a restart stops...", pal.FgDim, false)
		rows = append(rows, dialogAnswerRow("Cancel", true, false, width, pal))

	case restartConfirm:
		client := ""
		if m.DaemonClient != nil {
			client = m.DaemonClient.ClientVersion()
		}
		for _, l := range restartplan.Lines(&r.plan, client) {
			switch l.Kind {
			case restartplan.KindRow:
				label := l.Label + strings.Repeat(" ", max(restartplan.RowLabelWidth-len(l.Label), 1))
				line(pad + base.Foreground(pal.FgDim).Render(label) +
					base.Foreground(pal.Fg).Render(overlay.Truncate(l.Text, max(width-3-len(label), 1))))
			case restartplan.KindHeading:
				text(l.Text, pal.Fg, true)
			case restartplan.KindStop, restartplan.KindWarn:
				text(l.Text, pal.Warn, false)
			case restartplan.KindBack:
				text(l.Text, pal.Success, false)
			case restartplan.KindBlank:
				line("")
			default:
				text(l.Text, pal.FgDim, false)
			}
		}
		rows = append(rows,
			dialogAnswerRow("Restart the daemon", r.selected == RestartRowRestart, true, width, pal),
			dialogAnswerRow("Cancel", r.selected == RestartRowCancel, false, width, pal))
		hints = []overlay.Hint{{Key: "r", Label: "restart"}, cancel}

	case restartStopping, restartStarting, restartReattaching:
		steps := [...]string{"Stop the daemon", "Start the daemon " + r.plan.Version, "Attach to session " + m.SessionLabel(m.SessionName)}
		for i, step := range steps {
			phase := restartStopping + restartPhase(i)
			mark, ink := "  ", pal.FgMute
			switch {
			case phase < r.phase:
				mark, ink = "+ ", pal.Success
			case phase == r.phase:
				mark, ink, step = overlay.Sigil(), pal.Accent, step+"..."
			}
			text(mark+step, ink, phase == r.phase)
		}
		hints = nil

	case restartFailed:
		text(r.err, pal.Warn, false)
		text(r.fix, pal.Fg, false)
		label := "Close"
		if r.lost {
			label = "Quit tuios"
		}
		rows = append(rows, dialogAnswerRow(label, true, false, width, pal))
		hints = []overlay.Hint{{Key: "esc", Label: strings.ToLower(label)}}
	}

	rowStart := len(body)
	if len(rows) > 0 {
		line(base.Render(" ") + overlay.DashRule(max(width-2, 0), bg, pal))
		rowStart = len(body)
		body = append(body, rows...)
	}

	content, geo := overlay.Dialog{
		Title: "Restart the daemon",
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: hints,
	}.Render(pal)

	hits := make([]overlayRowHit, 0, len(rows))
	for i := range rows {
		y := geo.BodyY + rowStart + i
		idx := i
		if r.phase != restartConfirm {
			// The one row of the other phases closes the dialog.
			idx = RestartRowCancel
		}
		hits = append(hits, overlayRowHit{Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.Width, Y1: y + 1}, Idx: idx})
	}
	return content, geo, hits
}
