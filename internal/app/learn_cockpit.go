package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// LearnInboxFeed makes ch the Inbox's feed in Learn mode, in place of the
// watcher that reads the daemon's attention queue. ch carries the watcher's
// messages, InboxSnapshotMsg and InboxEventsMsg. It returns the command that
// waits for the first one, and nil outside Learn mode.
func (m *OS) LearnInboxFeed(ch chan tea.Msg) tea.Cmd {
	if !m.LearnMode || ch == nil {
		return nil
	}
	m.inboxEvents = ch
	return listenForInbox(ch)
}

// LearnTranscriptGrewMsg says a pretend agent's conversation grew, in Learn
// mode: the fleet sends it in place of the daemon's transcript event. The
// thread reads the agent again when it is on screen.
type LearnTranscriptGrewMsg struct {
	Session, Window string
}

func (m *OS) applyLearnTranscriptGrew(msg LearnTranscriptGrewMsg) tea.Cmd {
	if !m.LearnMode {
		return nil
	}
	t := m.Inbox.threads.byKey["\x00"+msg.Session+"\x00"+msg.Window]
	if t == nil || !t.read {
		return nil
	}
	if t.loading {
		t.again = true
		return nil
	}
	t.loading = true
	return m.threadPageCmd(t, t.cursor)
}

// LearnSpawnAgent is one agent the New agent sheet offers in Learn mode.
type LearnSpawnAgent struct {
	// ID is the harness id, such as "claude-code", and Cmd the command
	// that starts it, such as "claude".
	ID, Cmd string
}

// LearnSpawnInfo is what the New agent sheet reads in Learn mode, in place
// of the host's programs and git: the agents it can start, the folder, and
// the folder's repository and branch. An empty Repo means the folder is not
// in one.
type LearnSpawnInfo struct {
	Agents             []LearnSpawnAgent
	Folder, Repo, Head string
	Branches           []string
	// Machine names this machine on the sheet.
	Machine string
}

// SetLearnSpawnInfo installs what the New agent sheet reads in Learn mode.
func (m *OS) SetLearnSpawnInfo(fn func() LearnSpawnInfo) {
	m.learnSpawn = fn
}

func (m *OS) learnSpawnInfoCmd(gen uint64, info LearnSpawnInfo) tea.Cmd {
	return func() tea.Msg {
		msg := spawnInfoMsg{gen: gen, repo: info.Repo, head: info.Head, branches: info.Branches}
		for _, a := range info.Agents {
			msg.agents = append(msg.agents, spawnAgent{id: a.ID, cmd: a.Cmd, installed: true})
		}
		return msg
	}
}

// CockpitSelection is the agents view's cursor while it is open: the pane
// of the agent under it, "" for none, and the focus tab, in lower case.
func (m *OS) CockpitSelection() (window, tab string) {
	if !m.ShowInbox {
		return "", ""
	}
	if e, ok := m.cockpitAgent(); ok {
		window = e.WindowID
	} else if r, ok := m.cockpitSelected(); ok && r.hasItem {
		window = r.item.Window
	}
	return window, strings.ToLower(m.cockpitTab())
}

// CockpitReplyOpen reports whether the reply editor is open in the agents
// view.
func (m *OS) CockpitReplyOpen() bool { return m.ShowInbox && m.InboxReplyOpen() }

// LearnFanAttempt is what makes a pane an attempt of a fan in Learn mode,
// where the attempts are panes of the tour's one session and not sessions
// in worktrees of their own. Session is the name the attempt has in place
// of its session, which compare-fan and keep-fan take.
type LearnFanAttempt struct {
	Group, FanID, Branch, Prompt, Repo, Session string
}

// LearnSetFanAttempt makes a pane an attempt of a fan, or no longer one when
// a.Group is empty, in Learn mode. The agents view gathers the attempts of a
// fan under one row, and c compares them.
func (m *OS) LearnSetFanAttempt(window string, a LearnFanAttempt) {
	if !m.LearnMode {
		return
	}
	if a.Group == "" {
		delete(m.learnFans, window)
	} else {
		if m.learnFans == nil {
			m.learnFans = map[string]ckWorktree{}
		}
		m.learnFans[window] = ckWorktree{branch: a.Branch, group: a.Group, fanID: a.FanID, prompt: printableTitle(a.Prompt),
			repo: a.Repo, path: a.Repo, name: a.Session}
	}
	m.Inbox.ck.branches = nil
	m.cockpitTouchRows()
}
