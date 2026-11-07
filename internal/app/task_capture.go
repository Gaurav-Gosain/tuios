package app

import (
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The task capture sheet: a task card written from anywhere, in one key
// (ctrl+b + by default, or "New task" in the palette). It is the New agent
// sheet's editor and pills, made small: the task, then the session the card
// goes to, its stage, and the agent ctrl+enter starts on it.
//
// enter files the card and closes; ctrl+enter files it and starts the agent
// on it now, which holds the card from the start (start-agent with task);
// esc closes and keeps the draft for the next open. The first line is the
// title, and the rest, with the images as their stored paths, is the prompt.
// An image goes to the card's session's file store, which lives as long as
// the session does.
//
// Cards belong to a session today, not to a repository, so the first pill is
// the session, the focused pane's by default. The design's project scope and
// its "Any machine" pill come with the board that is shared across sessions.

// overlayKindTaskCapture is the sheet's overlay kind.
const overlayKindTaskCapture = "taskcapture"

// tcField is one stop of the sheet's tab order.
type tcField uint8

const (
	tcTask tcField = iota
	tcSession
	tcStage
	tcAgent
	tcFields
)

// tcTaskRows is how many rows the editor shows before it scrolls.
const tcTaskRows = 6

// tcTitleMax is add-task's limit on a title, in bytes.
const tcTitleMax = 200

// tcStages are the stages a captured card can start in: todo, or plan,
// where the agent that takes it writes a plan first and waits.
var tcStages = []string{session.TaskStageTodo, session.TaskStagePlan}

// taskCapture is the open sheet, or a closed one kept as the draft.
type taskCapture struct {
	ed    *composer.Model
	wires draftWires
	field tcField
	// sessions are the ones a card can go to, the current one first, and
	// sess the picked one.
	sessions []string
	sess     int
	stage    int
	// agents are the harnesses installed on this machine, and agent the
	// one ctrl+enter starts.
	agents []spawnAgent
	agent  int
	notice string
	busy   bool
}

// TaskCaptureOpen reports whether the sheet is up and owns the keyboard.
func (m *OS) TaskCaptureOpen() bool { return m.taskCapture != nil && !m.taskCaptureClosed }

// OpenTaskCapture opens the sheet, with the draft of the last one when it
// was closed with esc.
func (m *OS) OpenTaskCapture() tea.Cmd {
	if m.TaskCaptureOpen() {
		return nil
	}
	if m.SessionName == "" {
		m.ShowNotification("A task card needs a session. Start tuios with a daemon.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	s := m.taskCapture
	if s == nil {
		s = &taskCapture{ed: composer.New(composer.Options{MaxRows: tcTaskRows, Placeholder: "What is the task? The first line is its title.", Images: true})}
	}
	m.taskCapture, m.taskCaptureClosed = s, false
	s.field, s.notice, s.busy = tcTask, "", false
	cur := ""
	if s.sess < len(s.sessions) {
		cur = s.sessions[s.sess]
	}
	s.sessions = append([]string{m.SessionName}, m.otherSessionNames()...)
	s.sess = max(slices.Index(s.sessions, cur), 0)
	s.ed.SetPreviewer(m.replyPreviewer())
	c := m.newReplyMentions("", "")
	if !m.BrowserClient && (!m.RemoteClient || m.SSHIsLoopback) {
		c.dir = m.spawnFolder()
	}
	s.ed.SetCompleter(c)
	s.wires = draftWires{mentions: c}
	return tcAgentsCmd()
}

// tcAgentsMsg carries the installed harnesses, read off the update loop.
type tcAgentsMsg struct{ agents []spawnAgent }

func tcAgentsCmd() tea.Cmd {
	return func() tea.Msg {
		info := spawnInfoCmd(0, "")().(spawnInfoMsg)
		var out []spawnAgent
		for _, a := range info.agents {
			if a.installed {
				out = append(out, a)
			}
		}
		return tcAgentsMsg{agents: out}
	}
}

// applyTCAgents takes the installed harnesses, keeping the picked one.
func (m *OS) applyTCAgents(msg tcAgentsMsg) {
	s := m.taskCapture
	if s == nil {
		return
	}
	cur := ""
	if s.agent < len(s.agents) {
		cur = s.agents[s.agent].id
	} else if m.Inbox.ck.spawnLast != "" {
		cur = m.Inbox.ck.spawnLast
	}
	s.agents = msg.agents
	s.agent = max(slices.IndexFunc(s.agents, func(a spawnAgent) bool { return a.id == cur }), 0)
}

// closeTaskCapture closes the sheet. keep keeps the draft for the next open.
func (m *OS) closeTaskCapture(keep bool) {
	if keep {
		m.taskCaptureClosed = true
		return
	}
	m.taskCapture, m.taskCaptureClosed = nil, false
}

// TaskCapturePaste puts a paste into the task. An image path becomes the
// image, as in a reply.
func (m *OS) TaskCapturePaste(text string) tea.Cmd {
	s := m.taskCapture
	if !m.TaskCaptureOpen() || s.field != tcTask {
		return nil
	}
	return m.composerPasteCmd(s.ed, "", text)
}

// TaskCaptureKey handles a key while the sheet is up.
func (m *OS) TaskCaptureKey(msg tea.KeyPressMsg) tea.Cmd {
	s := m.taskCapture
	if !m.TaskCaptureOpen() {
		return nil
	}
	key := msg.String()
	switch key {
	case "tab":
		s.field = (s.field + 1) % tcFields
		return nil
	case "shift+tab":
		s.field = (s.field + tcFields - 1) % tcFields
		return nil
	case "ctrl+enter":
		return m.taskCaptureFile(true)
	case "esc":
		m.closeTaskCapture(true)
		return nil
	}
	if s.field == tcTask {
		switch s.ed.Update(msg, m.ProcessingRemoteKeys) {
		case composer.EventSend:
			return m.taskCaptureFile(false)
		case composer.EventClose:
			m.closeTaskCapture(true)
		case composer.EventPaste:
			return m.composerClipboardCmd(s.ed, "")
		}
		return nil
	}
	d := 0
	switch key {
	case "left", "h":
		d = -1
	case "right", "l", "space":
		d = 1
	case "enter":
		return m.taskCaptureFile(false)
	case "up", "k":
		s.field--
		return nil
	case "down", "j":
		s.field = min(s.field+1, tcFields-1)
		return nil
	}
	step := func(i, n int) int { return (i + d + n) % max(n, 1) }
	switch s.field {
	case tcSession:
		s.sess = step(s.sess, len(s.sessions))
	case tcStage:
		s.stage = step(s.stage, len(tcStages))
	case tcAgent:
		s.agent = step(s.agent, len(s.agents))
	}
	return nil
}

// tcSplit is a draft as a card: the first line is the title, and the rest is
// the prompt. An image on the first line goes to the prompt, which is what
// the agent reads.
func tcSplit(parts []composer.Part) (title string, prompt []composer.Part) {
	var t strings.Builder
	inTitle := true
	for _, p := range parts {
		if !inTitle || p.Kind == composer.PartImage {
			prompt = append(prompt, p)
			continue
		}
		head, rest, cut := strings.Cut(p.Text, "\n")
		t.WriteString(head)
		if cut {
			inTitle = false
			if strings.TrimSpace(rest) != "" {
				prompt = append(prompt, composer.Part{Kind: p.Kind, Text: rest, N: p.N})
			}
		}
	}
	return strings.TrimSpace(t.String()), prompt
}

// tcFiledMsg is the daemon's answer to a file.
type tcFiledMsg struct {
	text string
	err  error
	warn bool
}

// taskCaptureFile files the card, and with start starts the picked agent on
// it. The sheet closes at once; a refused file opens it again with the draft.
func (m *OS) taskCaptureFile(start bool) tea.Cmd {
	s := m.taskCapture
	if s.busy {
		return nil
	}
	title, promptParts := tcSplit(s.ed.Parts())
	switch {
	case title == "":
		s.notice, s.field = "Write the task first. The first line is its title.", tcTask
		return nil
	case len(title) > tcTitleMax:
		s.notice, s.field = "The first line is the title, and it is longer than 200 bytes. Put the rest on the next line.", tcTask
		return nil
	case start && len(s.agents) == 0:
		s.notice, s.field = "No agent is installed on this machine to start.", tcAgent
		return nil
	}
	sess := s.sessions[s.sess]
	stage := tcStages[s.stage]
	var agent spawnAgent
	if start {
		agent = s.agents[s.agent]
		m.Inbox.ck.spawnLast = agent.id
	}
	call, nonce := m.inboxCaller(), m.inboxNonce()
	local := m.AttachedHost == ""
	s.busy = true
	m.closeTaskCapture(true)
	return func() tea.Msg {
		// The images go to the session's store, and the prompt names them.
		prompt, err := spawnStashImages(call, sess, promptParts)
		if err != nil {
			return tcFiledMsg{err: err}
		}
		if prompt == "" {
			prompt = strings.TrimSpace(composer.JoinParts(promptParts))
		}
		params := map[string]any{"session": sess, "title": title, "stage": stage, "human_nonce": nonce}
		if prompt != "" {
			params["prompt"] = prompt
		}
		raw, err := call("add-task", params, 10*time.Second)
		if err != nil {
			return tcFiledMsg{err: err}
		}
		var row session.TaskRow
		if err := jsonx.Unmarshal(raw, &row); err != nil || row.ID == "" {
			return tcFiledMsg{err: errfmt.Errorf("read the add-task answer: no card id")}
		}
		filed := "Added " + row.ID + " to " + sess + "."
		if !start {
			return tcFiledMsg{text: filed}
		}
		first := "Take task " + row.ID + ": tuios task get " + row.ID + "."
		if stage == session.TaskStagePlan {
			first = "Take task " + row.ID + ": tuios task get " + row.ID + ". Write a plan first, and wait for the person before you change files."
		}
		sp := map[string]any{"agent": agent.cmd, "session": sess, "task": row.ID, "human_nonce": nonce, "prompt": first}
		// Plan first starts the agent in its own plan mode where it has
		// one. Its plan approval moves the card back to plan, and the
		// person's yes on to doing (task_auto.go).
		if stage == session.TaskStagePlan && slices.Contains(agent.launch.Permissions, "plan") {
			sp["permission_mode"] = "plan"
		}
		if local {
			sp["env"] = map[string]string{"PATH": os.Getenv("PATH")}
		}
		raw, err = call("start-agent", sp, spawnStartTimeout)
		if err != nil {
			return tcFiledMsg{warn: true, text: filed + " " + agent.cmd + " did not start: " + err.Error()}
		}
		var res struct {
			Ready  bool
			Reason string
		}
		_ = jsonx.Unmarshal(raw, &res)
		if !res.Ready && res.Reason != "" {
			return tcFiledMsg{warn: true, text: filed + " " + agent.cmd + " started on it and is not ready: " + printableTitle(res.Reason)}
		}
		return tcFiledMsg{text: filed + " " + agent.cmd + " works on it."}
	}
}

// applyTCFiled says what the daemon did. A filed card ends the draft; a
// refused one opens the sheet again with it.
func (m *OS) applyTCFiled(msg tcFiledMsg) {
	s := m.taskCapture
	if s != nil {
		s.busy = false
	}
	if msg.err != nil {
		if s != nil {
			m.taskCaptureClosed = false
			s.notice = "The card was not added: " + msg.err.Error()
		}
		return
	}
	m.closeTaskCapture(false)
	kind := "success"
	if msg.warn {
		kind = "warning"
	}
	m.ShowNotification(msg.text, kind, m.Settings.NotificationDuration)
}
