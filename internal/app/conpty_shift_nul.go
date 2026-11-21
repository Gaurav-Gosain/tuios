package app

import (
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
)

const conptyNULHold = 25 * time.Millisecond

var conptyInput = runtime.GOOS == "windows"

type heldNUL struct {
	keys []tea.KeyPressMsg
	gen  uint64
}

type heldNULExpired struct{ gen uint64 }

type releasedNUL struct{ key tea.KeyPressMsg }

func isNULKey(k tea.KeyPressMsg) bool {
	return k.Code == tea.KeySpace && k.Mod == tea.ModCtrl && k.Text == ""
}

func (m *OS) dropConPTYShiftNUL(msg tea.Msg) tea.Msg {
	if !conptyInput || m.RemoteClient {
		return msg
	}
	h := &m.conptyNUL
	switch e := msg.(type) {
	case releasedNUL:
		return e.key
	case heldNULExpired:
		if e.gen != h.gen || len(h.keys) == 0 {
			return nil
		}
		return h.giveBack(nil)
	case tea.KeyPressMsg:
		if isNULKey(e) {
			h.keys = append(h.keys, e)
			if len(h.keys) > 1 {
				return nil
			}
			h.gen++
			gen := h.gen
			return tea.BatchMsg{tea.Tick(conptyNULHold, func(time.Time) tea.Msg {
				return heldNULExpired{gen: gen}
			})}
		}
		if len(h.keys) == 0 {
			return msg
		}
		if e.Text != "" {
			h.keys = h.keys[:0]
			return msg
		}
		return h.giveBack(msg)
	}
	return msg
}

func (h *heldNUL) giveBack(msg tea.Msg) tea.Msg {
	defer func() { h.keys = h.keys[:0] }()
	if msg == nil && len(h.keys) == 1 {
		return h.keys[0]
	}
	cmds := make([]tea.Cmd, 0, len(h.keys)+1)
	for _, k := range h.keys {
		cmds = append(cmds, func() tea.Msg { return releasedNUL{key: k} })
	}
	if msg != nil {
		cmds = append(cmds, func() tea.Msg { return msg })
	}
	return tea.Sequence(cmds...)()
}
