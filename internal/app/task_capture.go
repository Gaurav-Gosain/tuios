package app

import (
	"cmp"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// The task capture sheet: a task card written from anywhere, in one key
// (ctrl+b + by default, or "New task" in the palette). It is the New agent
// sheet's editor and pills, made small: the task, then the session the card
// goes to, its stage, and the agent ctrl+enter starts on it.
//
// enter files the card and closes; ctrl+enter files it and starts the agent
// on it now, which holds the card from the start (start-agent with task);
// esc closes and keeps the draft for the next open. The first line is the
// title, and the rest is the prompt. An image is attached to the card
// (attach-task), which keeps it as long as the card, also after a restart,
// and the prompt names it by its attachment's name.
//
// A file is two calls. While add-task is out the draft waits in tcFiling and
// the sheet does not open, so a refusal can give the draft back and the
// draft cannot file twice. Once the card is added the draft is done: the
// next open is a new sheet, and the start-agent answer, which can take
// minutes, is a toast that touches no sheet.
//
// The card goes to the current session. The first pill is its project: the
// repository of the focused pane's folder, by its origin URL, so the card
// shows on that repository's board in every session and on linked machines;
// or none, for a card of the session only. When a machine is linked, the
// last pill is where the card's agent starts: Any machine (the daemon picks,
// place-agent), this machine (the default), or one linked machine. ctrl+enter places the
// agent there; on another machine its pane claims the card here over that
// machine's link back.

// overlayKindTaskCapture is the sheet's overlay kind.
const overlayKindTaskCapture = "taskcapture"

// tcField is one stop of the sheet's tab order.
type tcField uint8

const (
	tcTask tcField = iota
	tcProject
	tcStage
	tcAgent
	tcMachine
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
	// project is the origin URL of the focused pane's repository, empty
	// when it has none or it is not read yet, and noProject the person's
	// pick of a card of the session only.
	project   string
	noProject bool
	stage     int
	// agents are the harnesses installed on this machine, and agent the
	// one ctrl+enter starts.
	agents []spawnAgent
	agent  int
	// machines are where the card's agent may start: "" for this machine,
	// "any" for the machine the daemon picks, and each linked machine.
	// machine is the pick. The pill shows only when a machine is linked.
	machines []string
	machine  int
	notice   string
	// gen is the opening the sheet is in, from OS.tcOpenGen.
	gen uint64
}

// tcMachineValues are the Machine pill's values: this machine first, the
// default, then "any" and the linked machines this client knows. Nil when
// none is linked. A card's agent moves only when the person picks it.
func (m *OS) tcMachineValues() []string {
	var hosts []string
	for _, h := range m.FederationHosts {
		if h.Name != federation.LocalHostName && h.Name != "" {
			hosts = append(hosts, h.Name)
		}
	}
	if len(hosts) == 0 {
		return nil
	}
	return append([]string{"", session.TaskMachineAny}, hosts...)
}

// machineLabel is a Machine pill value as a person reads it.
func tcMachineLabel(v string) string {
	switch v {
	case session.TaskMachineAny:
		return "Any machine"
	case "":
		return "This machine"
	}
	return v
}

// lastField is the last stop of the tab order: the Machine pill shows only
// when a machine is linked.
func (s *taskCapture) lastField() tcField {
	if len(s.machines) == 0 {
		return tcAgent
	}
	return tcMachine
}

// TaskCaptureOpen reports whether the sheet is up and owns the keyboard.
func (m *OS) TaskCaptureOpen() bool { return m.taskCapture != nil && !m.taskCaptureClosed }

// OpenTaskCapture opens the sheet, with the draft of the last one when it
// was closed with esc.
func (m *OS) OpenTaskCapture() tea.Cmd {
	if m.TaskCaptureOpen() {
		return nil
	}
	if m.tcFiling != nil {
		m.ShowNotification("The last card is still being added. Open the sheet again in a moment.", "info", m.Settings.NotificationDuration)
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
	m.tcOpenGen++
	s.field, s.notice, s.gen = tcTask, "", m.tcOpenGen
	s.ed.SetPreviewer(m.replyPreviewer())
	c := m.newReplyMentions("", "")
	if !m.BrowserClient && (!m.RemoteClient || m.SSHIsLoopback) {
		c.dir = m.spawnFolder()
	}
	s.ed.SetCompleter(c)
	s.wires = draftWires{mentions: c}
	s.project = ""
	// The pick stays across openings while the machine is still linked.
	picked := ""
	if s.machine < len(s.machines) {
		picked = s.machines[s.machine]
	}
	s.machines, s.machine = m.tcMachineValues(), 0
	if i := slices.Index(s.machines, picked); i >= 0 && s.machines != nil && picked != "" {
		s.machine = i
	}
	return tea.Batch(tcAgentsCmd(s.gen), tcProjectCmd(s.gen, c.dir))
}

// tcOpening is the sheet of opening gen, or nil when the sheet was opened
// again since, or filed.
func (m *OS) tcOpening(gen uint64) *taskCapture {
	if s := m.taskCapture; s != nil && s.gen == gen {
		return s
	}
	return nil
}

// tcProjectMsg carries the origin URL of the folder the sheet opened in,
// read off the update loop.
type tcProjectMsg struct {
	gen uint64
	url string
}

// tcProjectCmd reads dir's origin URL. A folder on no machine this client
// can read (a remote client) has none.
func tcProjectCmd(gen uint64, dir string) tea.Cmd {
	if dir == "" {
		return nil
	}
	return func() tea.Msg {
		url, err := worktree.OriginURL(dir)
		if err != nil || worktree.NormalizeRemote(url) == "" || strings.HasPrefix(worktree.NormalizeRemote(url), "file:") {
			return tcProjectMsg{gen: gen}
		}
		return tcProjectMsg{gen: gen, url: url}
	}
}

// applyTCProject takes the folder's project.
func (m *OS) applyTCProject(msg tcProjectMsg) {
	if s := m.tcOpening(msg.gen); s != nil {
		s.project = msg.url
	}
}

// projectLabel is the project pill's value: the repository as host and
// path without the host, or none.
func (s *taskCapture) projectLabel() string {
	if s.project == "" || s.noProject {
		return "None, this session"
	}
	p := worktree.NormalizeRemote(s.project)
	if _, path, ok := strings.Cut(p, "/"); ok {
		return path
	}
	return p
}

// tcAgentsMsg carries the installed harnesses, read off the update loop.
type tcAgentsMsg struct {
	gen    uint64
	agents []spawnAgent
}

func tcAgentsCmd(gen uint64) tea.Cmd {
	return func() tea.Msg {
		info := spawnInfoCmd(0, "")().(spawnInfoMsg)
		var out []spawnAgent
		for _, a := range info.agents {
			if a.installed {
				out = append(out, a)
			}
		}
		return tcAgentsMsg{gen: gen, agents: out}
	}
}

// applyTCAgents takes the installed harnesses, keeping the picked one.
func (m *OS) applyTCAgents(msg tcAgentsMsg) {
	s := m.tcOpening(msg.gen)
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
	s.notice = ""
	return m.composerPasteCmd(s.ed, "", text)
}

// TaskCaptureKey handles a key while the sheet is up.
func (m *OS) TaskCaptureKey(msg tea.KeyPressMsg) tea.Cmd {
	s := m.taskCapture
	if !m.TaskCaptureOpen() {
		return nil
	}
	// A notice answers the last key: the next one clears it, and a file
	// that is refused again says so again.
	s.notice = ""
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
		case composer.EventCopy, composer.EventCut:
			return m.CopyFromEditor(s.ed.Copied())
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
		s.field = min(s.field+1, s.lastField())
		return nil
	}
	step := func(i, n int) int { return (i + d + n) % max(n, 1) }
	switch s.field {
	case tcProject:
		if s.project != "" && d != 0 {
			s.noProject = !s.noProject
		}
	case tcStage:
		s.stage = step(s.stage, len(tcStages))
	case tcAgent:
		s.agent = step(s.agent, len(s.agents))
	case tcMachine:
		s.machine = step(s.machine, len(s.machines))
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

// tcFiledMsg is the daemon's answer to add-task for the draft s. next,
// when set, starts the agent on the card.
type tcFiledMsg struct {
	s    *taskCapture
	text string
	err  error
	next tea.Cmd
}

// tcStartedMsg is the answer to start-agent on a filed card.
type tcStartedMsg struct {
	text string
	warn bool
}

// taskCaptureFile files the card, and with start starts the picked agent on
// it. The sheet closes at once; a refused file opens it again with the draft.
func (m *OS) taskCaptureFile(start bool) tea.Cmd {
	s := m.taskCapture
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
	sess := m.SessionName
	project := ""
	if !s.noProject {
		project = s.project
	}
	where := sess
	if project != "" {
		where = s.projectLabel()
	}
	stage := tcStages[s.stage]
	machine := ""
	if s.machine < len(s.machines) {
		machine = s.machines[s.machine]
	}
	if machine != "" && project == "" {
		s.notice, s.field = "A card of the session only starts on this machine. Pick a project, or This machine.", tcMachine
		return nil
	}
	var agent spawnAgent
	if start {
		agent = s.agents[s.agent]
		m.Inbox.ck.spawnLast = agent.id
	}
	call, nonce := m.inboxCaller(), m.inboxNonce()
	local := m.AttachedHost == ""
	build := ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	others := slices.DeleteFunc(slices.Clone(s.machines), func(v string) bool { return v == "" || v == session.TaskMachineAny || v == machine })
	m.closeTaskCapture(false)
	m.tcFiling = s
	return func() tea.Msg {
		// The images are attached once the card is added, and the prompt
		// names them.
		prompt, images := tcAttachmentParts(promptParts)
		params := map[string]any{"session": sess, "title": title, "stage": stage, "human_nonce": nonce}
		if prompt != "" {
			params["prompt"] = prompt
		}
		if project != "" {
			params["project"] = project
		}
		if machine != "" {
			params["machine"] = machine
		}
		raw, err := call("add-task", params, 10*time.Second)
		if err != nil {
			return tcFiledMsg{s: s, err: err}
		}
		var row session.TaskRow
		if err := jsonx.Unmarshal(raw, &row); err != nil || row.ID == "" {
			return tcFiledMsg{s: s, err: errfmt.Errorf("read the add-task answer: no card id")}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Added %s to %s.", row.ID, where)
		for _, im := range images {
			ap := map[string]any{"session": sess, "id": row.ID, "name": im.name, "human_nonce": nonce,
				"content": base64.StdEncoding.EncodeToString(im.data)}
			if _, err := call("attach-task", ap, 30*time.Second); err != nil {
				fmt.Fprintf(&b, " The image %s could not be attached: %s", im.name, printableTitle(err.Error()))
			}
		}
		filed := b.String()
		if !start {
			return tcFiledMsg{s: s, text: filed}
		}
		// The draft is done once the card is added. The agent's start,
		// which can take minutes, is a call of its own whose answer is a
		// toast only.
		return tcFiledMsg{s: s, text: filed, next: func() tea.Msg {
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
			start := call
			where := ""
			if machine != "" {
				// The daemon picks the machine, or checks the one named: the
				// agent there needs the agent, the repository and a link back.
				pp := map[string]any{"agent": agent.cmd, "task": row.ID, "task_session": sess, "local_origin": project}
				if machine != session.TaskMachineAny {
					pp["exclude"] = append(others, federation.LocalHostName)
				}
				if local {
					pp["env"] = map[string]string{"PATH": os.Getenv("PATH")}
				}
				ans, err := placeCall(call, pp)
				if err != nil {
					return tcStartedMsg{warn: true, text: filed + " " + agent.cmd + " did not start: " + tcVerbMessage(err)}
				}
				if host := ans.Host; host != "" {
					where = " on " + host
					sp["task"], sp["task_host"], sp["task_session"] = row.ID, ans.TaskHost, sess
					sp["repo_url"] = project
					if root := config.HostReposRoot(host); root != "" {
						sp["repos_root"] = root
					}
					if ans.Clone {
						sp["clone"] = true
					}
					start = hostCaller(host, build)
				}
			}
			if local && where == "" {
				sp["env"] = map[string]string{"PATH": os.Getenv("PATH")}
			}
			raw, err := start("start-agent", sp, spawnStartTimeout)
			if err != nil {
				return tcStartedMsg{warn: true, text: filed + " " + agent.cmd + " did not start: " + err.Error()}
			}
			var res struct {
				Ready  bool
				Reason string
			}
			_ = jsonx.Unmarshal(raw, &res)
			if !res.Ready && res.Reason != "" {
				return tcStartedMsg{warn: true, text: filed + " " + agent.cmd + " started on it and is not ready: " + printableTitle(res.Reason)}
			}
			return tcStartedMsg{text: filed + " " + agent.cmd + " works on it" + where + "."}
		}}
	}
}

// applyTCFiled says what add-task did. A refused card opens the sheet again
// with its draft; an added one ends the draft, and starts the agent when
// ctrl+enter asked for one.
func (m *OS) applyTCFiled(msg tcFiledMsg) tea.Cmd {
	if m.tcFiling != msg.s {
		return nil
	}
	m.tcFiling = nil
	if msg.err != nil {
		// The sheet stays shut while add-task is out, so no other draft
		// is open to replace.
		m.tcOpenGen++
		m.taskCapture, m.taskCaptureClosed = msg.s, false
		msg.s.gen, msg.s.notice = m.tcOpenGen, "The card was not added: "+msg.err.Error()
		return nil
	}
	if msg.next != nil {
		return msg.next
	}
	m.ShowNotification(msg.text, "success", m.Settings.NotificationDuration)
	return nil
}

// applyTCStarted says what start-agent did on a filed card.
func (m *OS) applyTCStarted(msg tcStartedMsg) {
	kind := "success"
	if msg.warn {
		kind = "warning"
	}
	m.ShowNotification(msg.text, kind, m.Settings.NotificationDuration)
}

// tcVerbMessage is a verb error as the toast says it: the daemon's own words
// without the code.
func tcVerbMessage(err error) string {
	if callErr, ok := errors.AsType[*session.VerbCallError](err); ok {
		return printableTitle(callErr.Message)
	}
	return printableTitle(err.Error())
}

// tcImage is an image of a task to attach to its card.
type tcImage struct {
	name string
	data []byte
}

// tcAttachmentParts is a task's prompt with each image named by the name it
// is attached by, and the images. Two images never share a name.
func tcAttachmentParts(parts []composer.Part) (string, []tcImage) {
	var b strings.Builder
	var images []tcImage
	used := map[string]bool{}
	for i, p := range parts {
		if p.Kind != composer.PartImage || p.Image == nil {
			b.WriteString(p.Text)
			continue
		}
		name := p.Image.Name
		if name == "" {
			name = "image-" + strconv.Itoa(i+1) + "." + cmp.Or(p.Image.Format, "png")
		}
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		for n := 2; used[name]; n++ {
			name = base + "-" + strconv.Itoa(n) + ext
		}
		used[name] = true
		images = append(images, tcImage{name: name, data: p.Image.Data})
		if t := b.String(); t != "" && !strings.HasSuffix(t, " ") && !strings.HasSuffix(t, "\n") {
			b.WriteByte(' ')
		}
		b.WriteString(name)
		b.WriteString(" (attached) ")
	}
	return strings.TrimSpace(b.String()), images
}
