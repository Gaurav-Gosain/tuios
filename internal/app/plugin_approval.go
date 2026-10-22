package app

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The plugin approval dialog: a native plugin waits for the person's answer
// on this machine, and the daemon told this client (MsgPluginApprovals,
// session/plugin_approval.go).
//
// It opens by itself, once per plugin and trust hash in this client: a
// plugin that waits does nothing until someone answers, and only the person
// can. Not now closes it and leaves the plugin waiting. tuios plugins
// approve answers the same question from a terminal. A plugin that asks for
// admin needs the person to type admin before Allow does anything.
//
// The answer is plugin.approve with the trust hash the dialog showed, so an
// answer to a plugin that changed in the meantime is refused, and the
// daemon's next list asks about what it is now.

// overlayKindPluginApproval is the dialog's overlay kind.
const overlayKindPluginApproval = "pluginapproval"

// pluginApprovalWidth is the dialog's preferred inner width.
const pluginApprovalWidth = 64

// pluginApprovalRows are the answers, in drawn order.
var pluginApprovalRows = []string{"Allow", "Deny", "Not now"}

// The rows by index.
const (
	pluginRowAllow = iota
	pluginRowDeny
	pluginRowLater
)

// pluginApprovalState is the open question and what this client was told.
type pluginApprovalState struct {
	// asks are the plugins that wait, as the daemon last said.
	asks []session.PluginApprovalAsk
	// seen are the questions this client showed: id and trust hash. A
	// question is opened by itself once.
	seen map[string]bool
	// open is the question shown, nil when the dialog is closed.
	open *session.PluginApprovalAsk
	// selected is the answer row under the cursor.
	selected int
	// typed is what the person typed to give admin.
	typed string
	// err is the daemon's last refusal.
	err string
	// busy is true while an answer is on its way.
	busy bool
}

// PluginApprovalsMsg carries the daemon's list of plugins that wait.
type PluginApprovalsMsg struct {
	Asks []session.PluginApprovalAsk
}

// pluginApprovalDoneMsg is the daemon's reply to an answer.
type pluginApprovalDoneMsg struct {
	id, trust string
	allow     bool
	err       error
}

// PluginApprovalOpen reports whether the dialog owns the keyboard.
func (m *OS) PluginApprovalOpen() bool { return m.pluginApproval.open != nil }

// PluginApprovalAsks is how many plugins wait for an answer.
func (m *OS) PluginApprovalAsks() int { return len(m.pluginApproval.asks) }

func pluginAskKey(a session.PluginApprovalAsk) string { return a.ID + "\x00" + a.Trust }

func pluginAskAdmin(a *session.PluginApprovalAsk) bool {
	return a != nil && slices.Contains(a.Grants, "admin")
}

// setPluginAsks takes the daemon's list, and opens the first question this
// client has not shown yet.
func (m *OS) setPluginAsks(asks []session.PluginApprovalAsk) {
	st := &m.pluginApproval
	st.asks = asks
	if st.seen == nil {
		st.seen = map[string]bool{}
	}
	if st.open != nil {
		// The question shown is still asked: keep it as it is. A plugin
		// that changed, or that someone answered elsewhere, closes it.
		if slices.ContainsFunc(asks, func(a session.PluginApprovalAsk) bool { return pluginAskKey(a) == pluginAskKey(*st.open) }) {
			return
		}
		st.open = nil
	}
	m.openNextPluginAsk()
}

// openNextPluginAsk opens the first question not shown yet, if any.
func (m *OS) openNextPluginAsk() {
	st := &m.pluginApproval
	for i := range st.asks {
		if a := st.asks[i]; !st.seen[pluginAskKey(a)] {
			st.seen[pluginAskKey(a)] = true
			st.open, st.selected, st.typed, st.err, st.busy = &a, pluginRowLater, "", "", false
			return
		}
	}
}

// ClosePluginApproval closes the dialog. The plugin keeps waiting.
func (m *OS) ClosePluginApproval() {
	m.pluginApproval.open = nil
	m.openNextPluginAsk()
}

// PluginApprovalSelect puts the cursor on an answer row.
func (m *OS) PluginApprovalSelect(idx int) {
	if idx >= 0 && idx < len(pluginApprovalRows) {
		m.pluginApproval.selected = idx
	}
}

// PluginApprovalKey handles a key while the dialog is open.
func (m *OS) PluginApprovalKey(key, text string) tea.Cmd {
	st := &m.pluginApproval
	if st.open == nil {
		return nil
	}
	switch key {
	case "esc":
		m.ClosePluginApproval()
		return nil
	case "up", "shift+tab":
		st.selected = (st.selected + len(pluginApprovalRows) - 1) % len(pluginApprovalRows)
		return nil
	case "down", "tab":
		st.selected = (st.selected + 1) % len(pluginApprovalRows)
		return nil
	case "enter":
		return m.PluginApprovalActivate(st.selected)
	}
	if !pluginAskAdmin(st.open) {
		return nil
	}
	// The admin field takes what is typed.
	switch key {
	case "backspace":
		if r := []rune(st.typed); len(r) > 0 {
			st.typed = string(r[:len(r)-1])
		}
	case "ctrl+u":
		st.typed = ""
	default:
		if text != "" {
			st.typed += printableRunes(text)
		} else if len(key) == 1 && key[0] > 32 && key[0] <= 126 {
			st.typed += key
		}
	}
	st.err = ""
	return nil
}

// PluginApprovalActivate answers with row idx.
func (m *OS) PluginApprovalActivate(idx int) tea.Cmd {
	st := &m.pluginApproval
	if st.open == nil || st.busy {
		return nil
	}
	st.selected = idx
	if idx == pluginRowLater {
		m.ClosePluginApproval()
		return nil
	}
	allow := idx == pluginRowAllow
	admin := false
	if allow && pluginAskAdmin(st.open) {
		if strings.TrimSpace(st.typed) != "admin" {
			st.err = "Type admin in the field to give the admin grant."
			return nil
		}
		admin = true
	}
	st.busy, st.err = true, ""
	id, trust := st.open.ID, st.open.Trust
	params := map[string]any{"plugin_id": id, "trust": trust, "allow": allow, "admin": admin}
	return func() tea.Msg {
		return pluginApprovalDoneMsg{id: id, trust: trust, allow: allow, err: pluginCaller("plugin.approve", params)}
	}
}

// pluginApprovalDone takes the daemon's reply to an answer.
func (m *OS) pluginApprovalDone(msg pluginApprovalDoneMsg) {
	st := &m.pluginApproval
	if st.open != nil && st.open.ID == msg.id && st.open.Trust == msg.trust {
		st.busy = false
		if msg.err != nil {
			st.err = msg.err.Error()
			return
		}
		st.open = nil
	}
	st.asks = slices.DeleteFunc(st.asks, func(a session.PluginApprovalAsk) bool { return a.ID == msg.id && a.Trust == msg.trust })
	if msg.err == nil {
		text := fmt.Sprintf("You allowed the plugin %s. It runs now.", msg.id)
		if !msg.allow {
			text = fmt.Sprintf("You denied the plugin %s. It does not run.", msg.id)
		}
		m.ShowNotification(text, "info", m.Settings.NotificationDuration)
	}
	m.openNextPluginAsk()
}

// pluginApprovalLines is the dialog's text above the answers. The first line
// is the question.
func pluginApprovalLines(a *session.PluginApprovalAsk) []string {
	name := a.Name
	if name == "" {
		name = a.ID
	}
	lines := []string{
		fmt.Sprintf("Allow the plugin %s to run on this machine?", name),
		fmt.Sprintf("%s %s, from %s", a.ID, a.Version, a.Source),
		"",
		"It runs on its own, with your rights:",
	}
	runs := a.Runs
	if len(runs) == 0 {
		runs = []string{"nothing"}
	}
	const maxRuns = 4
	for i, r := range runs {
		if i == maxRuns {
			lines = append(lines, fmt.Sprintf("  and %d more. Run tuios plugins info %s to see all.", len(runs)-maxRuns, a.ID))
			break
		}
		lines = append(lines, "  "+r)
	}
	lines = append(lines, "")
	if len(a.Grants) == 0 {
		lines = append(lines, "It can use tuios only for what every pane may do.")
	} else {
		lines = append(lines, "It can use tuios with these grants:")
		for _, g := range a.Grants {
			lines = append(lines, fmt.Sprintf("  %-6s %s", g, herdrplugin.GrantMeaning(g)))
		}
	}
	return lines
}

// renderPluginApproval draws the dialog.
func (m *OS) renderPluginApproval() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.pluginApproval
	a := st.open
	pal := theme.UI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(pluginApprovalWidth, m.GetRenderWidth())

	var body []string
	for i, l := range pluginApprovalLines(a) {
		for _, wrapped := range wrapPlain(l, max(width-2, 1)) {
			if i == 0 {
				body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+
					overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(wrapped), width, bg))
				continue
			}
			ink := pal.FgDim
			if strings.HasPrefix(l, "  admin") {
				ink = pal.Warn
			}
			body = append(body, dialogBodyLine(wrapped, width, ink, bg))
		}
	}
	if pluginAskAdmin(a) {
		body = append(body, dialogBodyLine("", width, pal.FgDim, bg))
		body = append(body, dialogBodyLine("To give it admin, type admin here:", width, pal.Fg, bg))
		field := fieldTail(printableRunes(st.typed), max(width-4, 1))
		body = append(body, overlay.Fill(
			overlay.Style(bg).Render(" ")+
				overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil())+
				overlay.Style(bg).Foreground(pal.Fg).Render(field)+
				overlay.Cursor(" ", bg, pal.Fg), width, bg))
	}
	note, ink := "Not now leaves it off. You can answer later with tuios plugins approve.", pal.FgMute
	switch {
	case st.busy:
		note = "Sending your answer..."
	case st.err != "":
		note, ink = st.err, pal.Warn
	case len(st.asks) > 1:
		note = fmt.Sprintf("%d plugins wait for your answer.", len(st.asks))
	}
	body = append(body, dialogBodyLine("", width, pal.FgDim, bg))
	for _, wrapped := range wrapPlain(note, max(width-2, 1)) {
		body = append(body, dialogBodyLine(wrapped, width, ink, bg))
	}
	body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg))
	top := len(body)
	for i := range pluginApprovalRows {
		body = append(body, m.pluginApprovalRow(i, width, pal))
	}
	hints := []overlay.Hint{{Key: overlay.EnterKey(), Label: "choose"}, {Key: "esc", Label: "not now"}}
	content, geo := overlay.Dialog{Title: "plugin approval", Width: width, Body: strings.Join(body, "\n"), Hints: hints}.Render(pal)
	var hits []overlayRowHit
	for i := range pluginApprovalRows {
		y := geo.BodyY + top + i
		hits = append(hits, overlayRowHit{Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.Width, Y1: y + 1}, Idx: i})
	}
	return content, geo, hits
}

// pluginApprovalRow draws one answer. The cursor row is on the Surface
// ground with the sigil, as the copy dialog's are. Allow on a plugin that
// asks for admin takes the warn ink under the cursor.
func (m *OS) pluginApprovalRow(idx, width int, pal overlay.Palette) string {
	st := &m.pluginApproval
	bg := pal.Canvas
	cursor := st.selected == idx
	if cursor {
		bg = pal.Surface
	}
	marker := " "
	if cursor {
		marker = overlay.SigilMark()
	}
	ink := pal.FgDim
	if cursor {
		ink = pal.Fg
		if idx == pluginRowAllow && pluginAskAdmin(st.open) {
			ink = pal.Warn
		}
	}
	row := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(marker) +
		overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(ink).Bold(cursor).Render(overlay.Truncate(pluginApprovalRows[idx], max(width-4, 1)))
	return pal.Mark(overlay.Fill(row, width, bg), overlay.RowState{Cursor: cursor, Focused: true})
}
