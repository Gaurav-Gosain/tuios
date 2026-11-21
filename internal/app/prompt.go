package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const promptSettle = 400 * time.Millisecond

const promptHeadMax = 3

type promptState struct {
	req      *session.PromptRequest
	openedAt time.Time
	marked   map[string]bool
	done     string
}

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

func (m *OS) promptCancel() tea.Cmd {
	return m.sendPromptAnswer(map[string]any{"request_id": m.prompt.req.ID, "cancel": true})
}

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

func promptMarks() (on, off string) {
	if overlay.UseASCII() {
		return "x", "-"
	}
	return "✓", "·"
}

func promptPanelTitle(req *session.PromptRequest) string {
	if req.From == "" {
		return "Prompt"
	}
	return "Prompt from " + printableTitle(req.From)
}
