package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The prompt: a text field or a list that an action, a script or a plugin
// asked the person for (session/prompt.go). The daemon pushes it to every
// client attached on this machine, and this client shows it in the command
// palette: the field is the palette's search line, and a pick's rows are the
// palette's rows, filtered as the person types.
//
// Enter answers and Esc cancels, each sent with this client's attach nonce,
// so only the person's own keys answer: keys that send-keys or a tape put
// through the client send nothing. Enter in the first moment after the
// prompt opens does nothing, since it was typed for what was in front of the
// person before. The first answer from any client wins, and the daemon's push
// that closes the prompt takes it off every other client.

// promptSettle is how long a prompt ignores Enter after it opens.
const promptSettle = 400 * time.Millisecond

// promptHeadMax is how many lines the question may take.
const promptHeadMax = 3

// promptState is the prompt the palette shows.
type promptState struct {
	// req is the open prompt, nil when none shows.
	req *session.PromptRequest
	// openedAt is when it opened. Enter waits promptSettle.
	openedAt time.Time
	// marked are the ids Tab marked in a pick that takes more than one row.
	marked map[string]bool
	// done is the id of the prompt this client last answered, cancelled or
	// closed, so a push of it that crossed the answer does not open it again.
	done string
}

// Marks a pick row can carry: none, or the off and on marks of a pick that
// takes more than one row.
const (
	promptMarkNone int8 = iota
	promptMarkOff
	promptMarkOn
)

// PromptMsg carries the daemon's prompt push.
type PromptMsg struct {
	Payload session.PromptPayload
}

// PromptOpen reports whether a prompt shows in the palette.
func (m *OS) PromptOpen() bool { return m.prompt.req != nil }

// setPrompt takes the daemon's push: it opens the prompt, or closes it and
// says how it ended when this client did not end it.
func (m *OS) setPrompt(p session.PromptPayload) {
	st := &m.prompt
	if p.Prompt == nil {
		if st.req != nil && st.req.ID == p.Closed {
			m.endPrompt()
			if text := promptClosedText(p.Reason); text != "" {
				m.ShowNotification(text, "info", m.Settings.NotificationDuration)
			}
		}
		return
	}
	if p.Prompt.ID == st.done || st.req != nil && st.req.ID == p.Prompt.ID {
		return
	}
	if m.ShowCommandPalette {
		m.CloseCommandPalette()
	}
	req := p.Prompt
	*st = promptState{req: req, openedAt: time.Now(), done: st.done}
	if req.Multiple {
		st.marked = map[string]bool{}
	}
	m.ShowCommandPalette = true
	m.paletteList.Reset()
	if req.Kind == session.PromptInput && req.Default != "" {
		m.paletteList.Query.Set(req.Default)
	}
	m.PaletteItems = promptPaletteItems(req)
	m.raiseOverlay("palette")
	m.MarkAllDirty()
}

// promptClosedText says how a prompt ended at another client or at the
// daemon.
func promptClosedText(reason string) string {
	switch reason {
	case session.PromptAnswered:
		return "The prompt was answered at another client"
	case session.PromptCancelled:
		return "The prompt was cancelled at another client"
	case session.PromptTimeout:
		return "The prompt timed out"
	}
	return "The prompt closed"
}

// endPrompt takes the prompt off the palette and closes it. It sends nothing.
func (m *OS) endPrompt() {
	st := &m.prompt
	if st.req == nil {
		return
	}
	done := st.req.ID
	*st = promptState{done: done}
	m.ShowCommandPalette = false
	m.paletteList.Reset()
	m.PaletteItems = nil
	m.MarkAllDirty()
}

// promptPaletteItems are a pick's rows as palette rows: the icon before the
// title, and the subtitle in the quiet slot on the right.
func promptPaletteItems(req *session.PromptRequest) []CommandPaletteItem {
	if req.Kind != session.PromptPick {
		return []CommandPaletteItem{}
	}
	items := make([]CommandPaletteItem, 0, len(req.Items))
	for _, it := range req.Items {
		name := it.Title
		if it.Icon != "" {
			name = it.Icon + " " + it.Title
		}
		items = append(items, CommandPaletteItem{Name: name, Shortcut: it.Subtitle, promptID: it.ID})
	}
	return items
}

// promptRows are the pick rows the query matches, with their marks, and the
// row that sends the typed text when the prompt takes other text.
func (m *OS) promptRows() []CommandPaletteItem {
	req := m.prompt.req
	if req.Kind != session.PromptPick {
		return nil
	}
	query := m.paletteList.Query.String()
	rows := matchPaletteItems(m.allPaletteItems(), query)
	if req.Multiple {
		rows = append([]CommandPaletteItem(nil), rows...)
		for i := range rows {
			rows[i].promptMark = promptMarkOff
			if m.prompt.marked[rows[i].promptID] {
				rows[i].promptMark = promptMarkOn
			}
		}
	}
	if !req.AllowOther || strings.TrimSpace(query) == "" {
		return rows
	}
	for _, it := range req.Items {
		if strings.EqualFold(it.Title, query) {
			return rows
		}
	}
	return append(rows, CommandPaletteItem{Name: "Use \"" + query + "\"", promptOther: true})
}

// promptKey reads the prompt's own keys: Tab marks a row of a pick that
// takes more than one. The rebind key does nothing here.
func (m *OS) promptKey(key string) (tea.Cmd, bool) {
	req := m.prompt.req
	switch key {
	case "tab":
		if !req.Multiple {
			return nil, true
		}
		rows := m.filteredPaletteItems()
		if c := m.paletteList.Cursor; c >= 0 && c < len(rows) && !rows[c].promptOther {
			id := rows[c].promptID
			m.prompt.marked[id] = !m.prompt.marked[id]
			if !m.prompt.marked[id] {
				delete(m.prompt.marked, id)
			}
		}
		return nil, true
	case PaletteRebindKey:
		return nil, true
	}
	return nil, false
}

// promptAccept sends the answer: the typed text, or the rows chosen.
func (m *OS) promptAccept() tea.Cmd {
	st := &m.prompt
	req := st.req
	if time.Since(st.openedAt) < promptSettle {
		m.ShowNotification("This prompt just appeared. Read it, then answer", "warning", m.Settings.NotificationDuration)
		return nil
	}
	query := m.paletteList.Query.String()
	params := map[string]any{"request_id": req.ID}
	if req.Kind == session.PromptInput {
		params["value"] = query
		return m.sendPromptAnswer(params)
	}
	rows := m.filteredPaletteItems()
	cur := m.paletteList.Cursor
	onRow := cur >= 0 && cur < len(rows)
	ids := []string{}
	other := ""
	if req.Multiple && len(st.marked) > 0 {
		for _, it := range req.Items {
			if st.marked[it.ID] {
				ids = append(ids, it.ID)
			}
		}
		if onRow && rows[cur].promptOther {
			other = query
		}
	} else {
		switch {
		case !onRow:
			m.ShowNotification("Nothing matches. Type less, or press Esc to cancel", "info", m.Settings.NotificationDuration)
			return nil
		case rows[cur].promptOther:
			other = query
		default:
			ids = append(ids, rows[cur].promptID)
		}
	}
	params["ids"] = ids
	if other != "" {
		params["other"] = other
	}
	return m.sendPromptAnswer(params)
}

// promptCancel sends the cancel.
func (m *OS) promptCancel() tea.Cmd {
	return m.sendPromptAnswer(map[string]any{"request_id": m.prompt.req.ID, "cancel": true})
}

// sendPromptAnswer sends params to answer-prompt with this client's attach
// nonce, and closes the prompt here. A key that was not the person's sends
// nothing, and the prompt stays.
func (m *OS) sendPromptAnswer(params map[string]any) tea.Cmd {
	if !m.inboxCanAct("Answering a prompt") {
		return nil
	}
	nonce := m.inboxNonce()
	if nonce == "" {
		m.ShowNotification(m.noNonceText(), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	params["human_nonce"] = nonce
	call := m.inboxActCaller()
	m.endPrompt()
	return func() tea.Msg {
		_, err := call("answer-prompt", params, 5*time.Second)
		return CommandRanMsg{Label: "answer-prompt", Err: err}
	}
}

// promptHead is the question, wrapped to width, at most promptHeadMax lines.
func (m *OS) promptHead(width int) []string {
	if m.prompt.req == nil {
		return nil
	}
	lines := wrapPlain(printableTitle(m.prompt.req.Title), max(width, 1))
	if len(lines) > promptHeadMax {
		lines = lines[:promptHeadMax]
		lines[promptHeadMax-1] = overlay.Truncate(lines[promptHeadMax-1]+" ...", width)
	}
	return lines
}

// promptFooter is the palette's footer while a prompt shows.
func (m *OS) promptFooter() []overlay.Hint {
	req := m.prompt.req
	if req.Kind == session.PromptInput {
		return []overlay.Hint{{Key: overlay.EnterGlyph, Label: "send"}, {Key: "esc", Label: "cancel"}}
	}
	hints := []overlay.Hint{{Key: hintListMove, Label: "move"}, {Key: overlay.EnterGlyph, Label: "choose"}}
	if req.Multiple {
		hints = append(hints, overlay.Hint{Key: "tab", Label: "mark"})
	}
	return append(hints, overlay.Hint{Key: "esc", Label: "cancel"})
}

// promptMarks are a pick row's marks, in ASCII when the chrome asks for it.
func promptMarks() (on, off string) {
	if overlay.UseASCII() {
		return "x", "-"
	}
	return "✓", "·"
}

// promptPanelTitle is the palette's title while a prompt shows: who asks.
func promptPanelTitle(req *session.PromptRequest) string {
	if req.From == "" {
		return "Prompt"
	}
	return "Prompt from " + printableTitle(req.From)
}
