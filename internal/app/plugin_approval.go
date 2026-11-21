package app

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const overlayKindPluginApproval = "pluginapproval"

const pluginApprovalWidth = 64

var pluginApprovalRows = []string{"Allow", "Deny", "Not now"}

const (
	pluginRowAllow = iota
	pluginRowDeny
	pluginRowLater
)

type pluginApprovalState struct {
	asks      []session.PluginApprovalAsk
	seen      map[string]bool
	open      *session.PluginApprovalAsk
	selected  int
	typed     string
	denyArmed bool
	openedAt  time.Time
	err       string
	busy      bool
}

// PluginApprovalsMsg carries the daemon's list of plugins that wait.
type PluginApprovalsMsg struct {
	Asks []session.PluginApprovalAsk
}

type pluginApprovalDoneMsg struct {
	id, trust string
	allow     bool
	err       error
}

// PluginApprovalOpen reports whether the dialog owns the keyboard.
func (m *OS) PluginApprovalOpen() bool { return m.pluginApproval.open != nil }

func pluginAskKey(a session.PluginApprovalAsk) string { return a.ID + "\x00" + a.Trust }

func pluginAskAdmin(a *session.PluginApprovalAsk) bool {
	return a != nil && slices.Contains(a.Grants, "admin")
}

func (m *OS) setPluginAsks(asks []session.PluginApprovalAsk) {
	st := &m.pluginApproval
	st.asks = asks
	if st.seen == nil {
		st.seen = map[string]bool{}
	}
	if st.open != nil {
		if slices.ContainsFunc(asks, func(a session.PluginApprovalAsk) bool { return pluginAskKey(a) == pluginAskKey(*st.open) }) {
			return
		}
		st.open = nil
	}
	m.openNextPluginAsk()
}

func (m *OS) openNextPluginAsk() {
	st := &m.pluginApproval
	for i := range st.asks {
		if a := st.asks[i]; !st.seen[pluginAskKey(a)] {
			st.seen[pluginAskKey(a)] = true
			st.open, st.selected, st.typed, st.err, st.busy = &a, pluginRowLater, "", "", false
			st.openedAt = pluginApprovalNow()
			st.denyArmed = false
			return
		}
	}
}

// ClosePluginApproval closes the dialog. The plugin keeps waiting.
func (m *OS) ClosePluginApproval() {
	m.pluginApproval.open = nil
	m.openNextPluginAsk()
}

const pluginApprovalSettle = 500 * time.Millisecond

var pluginApprovalNow = time.Now

func (m *OS) pluginApprovalSettled() bool {
	return pluginApprovalNow().Sub(m.pluginApproval.openedAt) >= pluginApprovalSettle
}

func pluginApprovalWord(a *session.PluginApprovalAsk) string {
	if pluginAskAdmin(a) {
		return "admin"
	}
	return "allow"
}

// PluginApprovalSelect puts the cursor on an answer row, as a hover does.
func (m *OS) PluginApprovalSelect(idx int) {
	if idx >= 0 && idx < len(pluginApprovalRows) && m.pluginApprovalSettled() {
		m.pluginApproval.selected = idx
	}
}

// PluginApprovalKey handles a key while the dialog is open.
//
// Allow takes a typed word: allow, or admin for a plugin that asks for it,
// then Enter. No row and no key alone allows, so keys meant for a pane, such
// as Tab and Enter or Up, Up and Enter, cannot. The cursor does not wrap.
func (m *OS) PluginApprovalKey(key, text string) tea.Cmd {
	st := &m.pluginApproval
	if st.open == nil || !m.pluginApprovalSettled() || m.ProcessingRemoteKeys || m.pressingTapeKeys {
		return nil
	}
	if key != "enter" {
		st.denyArmed = false
	}
	switch key {
	case "esc":
		m.ClosePluginApproval()
		return nil
	case "up":
		st.selected = max(st.selected-1, 0)
		return nil
	case "down":
		st.selected = min(st.selected+1, len(pluginApprovalRows)-1)
		return nil
	case "enter":
		if strings.TrimSpace(st.typed) == pluginApprovalWord(st.open) {
			return m.pluginApprovalAnswer(true)
		}
		return m.pluginApprovalChoose(st.selected)
	case "backspace":
		if r := []rune(st.typed); len(r) > 0 {
			st.typed = string(r[:len(r)-1])
		}
		st.err = ""
		return nil
	case "ctrl+u":
		st.typed, st.err = "", ""
		return nil
	}
	if text != "" {
		st.typed += printableRunes(text)
	} else if len(key) == 1 && key[0] > 32 && key[0] <= 126 {
		st.typed += key
	}
	st.err = ""
	return nil
}

// PluginApprovalActivate is a click on row idx. Not now closes at once.
// Deny takes a second click or Enter. Allow never answers to a click: it
// asks for the typed word.
func (m *OS) PluginApprovalActivate(idx int) tea.Cmd {
	st := &m.pluginApproval
	if st.open == nil || st.busy || !m.pluginApprovalSettled() {
		return nil
	}
	if idx != pluginRowDeny {
		st.denyArmed = false
	}
	return m.pluginApprovalChoose(idx)
}

func (m *OS) pluginApprovalChoose(idx int) tea.Cmd {
	st := &m.pluginApproval
	st.selected = idx
	switch idx {
	case pluginRowLater:
		m.ClosePluginApproval()
		return nil
	case pluginRowAllow:
		st.err = "Type " + pluginApprovalWord(st.open) + " in the field, then press Enter."
		return nil
	}
	if !st.denyArmed {
		st.denyArmed = true
		st.err = "Press Enter or click Deny again to deny the plugin."
		return nil
	}
	return m.pluginApprovalAnswer(false)
}

func (m *OS) pluginApprovalAnswer(allow bool) tea.Cmd {
	st := &m.pluginApproval
	if st.open == nil || st.busy {
		return nil
	}
	admin := allow && pluginAskAdmin(st.open)
	st.busy, st.err = true, ""
	id, trust := st.open.ID, st.open.Trust
	params := map[string]any{"plugin_id": id, "trust": trust, "allow": allow, "admin": admin}
	return func() tea.Msg {
		return pluginApprovalDoneMsg{id: id, trust: trust, allow: allow, err: pluginCaller("plugin.approve", params)}
	}
}

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

const pluginApprovalMaxRuns = 4

func (m *OS) renderPluginApproval() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.pluginApproval
	a := st.open
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(pluginApprovalWidth, m.modalRoomWidth())
	inner := max(width-2, 1)

	var body []string
	line := func(text string, ink color.Color) {
		body = append(body, dialogBodyLine(text, width, ink, bg))
	}
	name := herdrplugin.ShownName(a.Name)
	if name == "" {
		name = a.ID
	}
	body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+
		overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(overlay.Truncate("Allow the plugin "+name+"?", inner)), width, bg))
	line(a.ID+" "+herdrplugin.ShownName(a.Version), pal.FgDim)
	from, src := "from ", a.Source
	if p, ok := strings.CutPrefix(src, "folder "); ok {
		from, src = "from the folder ", p
	}
	line(from+truncPathLeft(src, max(inner-len(from), 1)), pal.FgDim)
	line("", pal.FgDim)
	line("It runs on its own, with your rights:", pal.FgDim)
	runs := a.Runs
	if len(runs) == 0 {
		runs = []string{"nothing"}
	}
	for i, r := range runs {
		if i == pluginApprovalMaxRuns {
			line(fmt.Sprintf("  and %d more. Run tuios plugins info %s to see all.", len(runs)-pluginApprovalMaxRuns, a.ID), pal.FgDim)
			break
		}
		line("  "+r, pal.FgDim)
	}
	line("", pal.FgDim)
	if len(a.Grants) == 0 {
		line("It can use tuios only for what every pane may do.", pal.FgDim)
	} else {
		line("It can use tuios with these grants:", pal.FgDim)
		for _, g := range a.Grants {
			ink := pal.FgDim
			if g == "admin" || g == "write" {
				ink = pal.Warn
			}
			for i, w := range wrapPlain(herdrplugin.GrantMeaning(g), max(inner-9, 10)) {
				label := ""
				if i == 0 {
					label = g
				}
				line(fmt.Sprintf("  %-6s %s", label, w), ink)
			}
		}
	}
	line("Grants are not a sandbox. It can read your files.", pal.FgDim)
	line("", pal.FgDim)
	word := pluginApprovalWord(a)
	line("To allow it, type "+word+" and press Enter:", pal.Fg)
	field := fieldTail(printableRunes(st.typed), max(width-4, 1))
	body = append(body, overlay.Fill(
		overlay.Style(bg).Render(" ")+
			overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil())+
			overlay.Style(bg).Foreground(pal.Fg).Render(field)+
			overlay.Cursor(" ", bg, pal.Fg), width, bg))
	note, ink := "Not now leaves it off. You can answer later with tuios plugins approve.", pal.FgMute
	switch {
	case st.busy:
		note = "Sending your answer..."
	case st.err != "":
		note, ink = st.err, pal.Warn
	case len(st.asks) > 1:
		note = fmt.Sprintf("%d plugins wait for your answer.", len(st.asks))
	}
	for _, wrapped := range wrapPlain(note, inner) {
		line(wrapped, ink)
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
