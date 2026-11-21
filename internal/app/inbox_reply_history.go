package app

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const replyHistoryTimeout = 3 * time.Second

type replyHistoryMsg struct {
	comp    *composer.Model
	entries []composer.HistoryEntry
}

type replyEditorDoneMsg struct {
	comp *composer.Model
	dir  string
	path string
	err  error
}

func (m *OS) replyHistoryTarget(sessionID, windowID string) string {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		for _, w := range m.Windows {
			if w == nil || w.ID != windowID || w.AgentHarness == "" || !m.paneIsLocal(w) {
				continue
			}
			if dir := m.paneDir(w); dir != "" {
				return "harness:" + w.AgentHarness + ":" + dir
			}
		}
	}
	return "pane:" + sessionID + "/" + windowID
}

func (m *OS) replyHistoryFetch() tea.Cmd {
	d, ok := m.openDraft()
	if !ok || d.wires.historyAsked || d.wires.histWindow == "" || (m.DaemonClient == nil && m.Inbox.call == nil) {
		return nil
	}
	d.wires.historyAsked = true
	nonce := m.inboxNonce()
	if nonce == "" {
		return nil
	}
	call, comp := m.inboxCaller(), d.comp
	target := m.replyHistoryTarget(d.wires.histSession, d.wires.histWindow)
	return func() tea.Msg {
		raw, err := call("reply-history", map[string]any{"target": target, "human_nonce": nonce}, replyHistoryTimeout)
		if err != nil {
			return nil
		}
		var res struct {
			Entries []struct {
				Parts   []replyPart `json:"parts"`
				Dropped []string    `json:"dropped"`
			} `json:"entries"`
		}
		if jsonx.Unmarshal(raw, &res) != nil {
			return nil
		}
		msg := replyHistoryMsg{comp: comp}
		for _, e := range res.Entries {
			msg.entries = append(msg.entries, composer.HistoryEntry{Parts: partsFromWire(e.Parts), Dropped: e.Dropped})
		}
		return msg
	}
}

func partsFromWire(in []replyPart) []composer.Part {
	out := make([]composer.Part, 0, len(in))
	for _, p := range in {
		switch p.Kind {
		case "paste":
			out = append(out, composer.Part{Kind: composer.PartPaste, Text: p.Text, N: p.N})
		case "image":
			img, err := composer.NewImage(p.Data)
			if err != nil {
				continue
			}
			out = append(out, composer.Part{Kind: composer.PartImage, N: p.N, Image: img})
		default:
			out = append(out, composer.Part{Kind: composer.PartText, Text: p.Text})
		}
	}
	return out
}

func (m *OS) applyReplyHistory(msg replyHistoryMsg) {
	if m.composerLive(msg.comp) {
		msg.comp.SetHistory(msg.entries)
	}
}

func (m *OS) replyHistoryAdd(sessionID, windowID string, parts []composer.Part) tea.Cmd {
	if len(parts) == 0 || (m.DaemonClient == nil && m.Inbox.call == nil) {
		return nil
	}
	nonce := m.inboxNonce()
	if nonce == "" {
		return nil
	}
	call := m.inboxCaller()
	params := map[string]any{"target": m.replyHistoryTarget(sessionID, windowID), "parts": wireReplyParts(parts), "human_nonce": nonce}
	return func() tea.Msg {
		_, _ = call("add-reply-history", params, replyHistoryTimeout)
		return nil
	}
}

func (m *OS) replyEditorCmd(comp *composer.Model) tea.Cmd {
	if m.imagePasteReason() != "" {
		comp.SetNotice("The editor runs only on the machine tuios runs on, and you are elsewhere.")
		return nil
	}
	dir, err := os.MkdirTemp("", "tuios-reply-")
	if err != nil {
		comp.SetNotice("tuios cannot make a file for the editor: " + err.Error())
		return nil
	}
	path := filepath.Join(dir, "reply.md")
	if err := os.WriteFile(path, []byte(comp.Text()), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		comp.SetNotice("tuios cannot make a file for the editor: " + err.Error())
		return nil
	}
	m.replyEditorDir = dir
	argv := strings.Fields(os.Getenv("VISUAL"))
	if len(argv) == 0 {
		argv = strings.Fields(os.Getenv("EDITOR"))
	}
	if len(argv) == 0 {
		argv = []string{"vi"}
	}
	c := m.editorCommand(exec.Command(argv[0], append(argv[1:], path)...)) //nolint:gosec // the person's own editor
	return tea.Sequence(m.releaseHostPixelMouse(), m.execProcess(c, func(err error) tea.Msg {
		return replyEditorDoneMsg{comp: comp, dir: dir, path: path, err: err}
	}))
}

func (m *OS) applyReplyEditorDone(msg replyEditorDoneMsg) tea.Cmd {
	data, readErr := readBounded(msg.path, composer.DefaultMaxBytes+1)
	_ = os.RemoveAll(msg.dir)
	if m.replyEditorDir == msg.dir {
		m.replyEditorDir = ""
	}
	back := func() tea.Msg { return hostTerminalBackMsg{} }
	if !m.composerLive(msg.comp) {
		return back
	}
	switch {
	case msg.err != nil:
		msg.comp.SetNotice("The editor did not finish: " + msg.err.Error() + ". The draft is as it was.")
	case readErr != nil:
		msg.comp.SetNotice("tuios cannot read what the editor wrote: " + readErr.Error())
	default:
		msg.comp.ReplaceText(strings.TrimRight(string(data), "\n"))
	}
	return back
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, limit))
}

func (m *OS) removeReplyEditorFile() {
	if m.replyEditorDir != "" {
		_ = os.RemoveAll(m.replyEditorDir)
		m.replyEditorDir = ""
	}
}

// ReplyHistoryFetch is replyHistoryFetch, for the input path.
func (m *OS) ReplyHistoryFetch() tea.Cmd { return m.replyHistoryFetch() }

func (m *OS) replyHistoryOnSent(msg InboxRepliedMsg) tea.Cmd {
	if msg.Err != nil || msg.Requeue {
		return nil
	}
	parts := msg.Parts
	if parts == nil && msg.Text != "" {
		parts = []composer.Part{{Kind: composer.PartText, Text: msg.Text}}
	}
	return m.replyHistoryAdd(msg.Session, msg.Window, parts)
}
