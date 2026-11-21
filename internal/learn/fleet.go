package learn

import (
	"encoding/json"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
)

const learnSession = "local"

const learnNonce = "learn"

const fleetWait = 1500 * time.Millisecond

type fleet struct {
	send  func(tea.Msg)
	feed  chan tea.Msg
	poke  chan struct{}
	spawn func(msg spawnAgentsMsg)
	close func(windows []string)

	mu               sync.Mutex
	items, published map[string]session.AttentionItem
	unheld           map[string]bool
	dismissed        map[string]bool
	queued           map[string][]string
	typedAt          map[string]int
	seq              uint64
	since            map[string]int64
	fans             map[string]*learnFan
	attempts         map[string]fanAttempt
}

type learnFan struct {
	id, group, task, agent string
	sessions               []string
	verdict                map[string]any
}

type fanAttempt struct {
	fan    string
	window string
}

func newFleet(send func(tea.Msg)) *fleet {
	f := &fleet{
		send:      send,
		feed:      make(chan tea.Msg, 1),
		poke:      make(chan struct{}, 1),
		items:     map[string]session.AttentionItem{},
		published: map[string]session.AttentionItem{},
		unheld:    map[string]bool{},
		dismissed: map[string]bool{},
		queued:    map[string][]string{},
		typedAt:   map[string]int{},
		since:     map[string]int64{},
		fans:      map[string]*learnFan{},
		attempts:  map[string]fanAttempt{},
	}
	f.feed <- app.InboxSnapshotMsg{}
	go f.run()
	return f
}

func (f *fleet) run() {
	for range f.poke {
		f.mu.Lock()
		var events []app.InboxEvent
		for id, it := range f.items {
			old, ok := f.published[id]
			switch {
			case !ok:
				events = append(events, app.InboxEvent{Action: session.AttentionOpened, Item: &it})
			case !sameItem(old, it):
				events = append(events, app.InboxEvent{Action: session.AttentionUpdated, Item: &it})
			}
		}
		for id, old := range f.published {
			if _, ok := f.items[id]; !ok {
				old.Closed = session.AttentionClosedResolved
				events = append(events, app.InboxEvent{Action: session.AttentionClosed, Item: &old})
			}
		}
		f.published = maps.Clone(f.items)
		f.mu.Unlock()
		if len(events) > 0 {
			f.feed <- app.InboxEventsMsg{Events: events}
		}
	}
}

func sameItem(a, b session.AttentionItem) bool {
	return a.Kind == b.Kind && a.Summary == b.Summary && a.RequestID == b.RequestID && a.Name == b.Name
}

func (f *fleet) changed(window string) {
	f.reconcile()
	f.send(app.LearnTranscriptGrewMsg{Session: learnSession, Window: window})
}

func (f *fleet) reconcile() {
	f.mu.Lock()
	defer f.mu.Unlock()
	next := map[string]session.AttentionItem{}
	for _, a := range webshell.Agents() {
		v := a.View()
		if v.Resting && v.Rests != f.typedAt[a.Window] && len(f.queued[a.Window]) > 0 {
			text := f.queued[a.Window][0]
			f.queued[a.Window] = f.queued[a.Window][1:]
			f.typedAt[a.Window] = v.Rests
			a.Type(text + "\r")
		}
		it := session.AttentionItem{Session: learnSession, Window: a.Window, Harness: a.Harness, Name: a.Name}
		switch {
		case v.Prompt != nil:
			it.ID, it.Kind, it.Summary, it.Since = v.Prompt.ID, v.Prompt.Kind, v.Prompt.Summary, v.Prompt.Since
			if it.Kind == session.AttentionApproval && a.Harness == webshell.AgentHarness && !f.unheld[it.ID] {
				it.RequestID = it.ID
				it.Options = []string{session.ApprovalOnce, session.ApprovalAlways, session.ApprovalDeny}
				it.AlwaysScope = []string{"Edit(projects/website/**)"}
				it.DenyMessage = true
			}
		case v.State == "done":
			it.ID = a.Window + "-done-" + strconv.Itoa(len(v.Checkpoints)) + "-" + strconv.Itoa(v.Rests)
			if f.dismissed[it.ID] {
				continue
			}
			it.Kind, it.Summary, it.Count = session.AttentionFinished, v.Message, 1
			if f.since[it.ID] == 0 {
				f.since[it.ID] = time.Now().UnixNano()
			}
			it.Since = f.since[it.ID]
		default:
			continue
		}
		if old, ok := f.items[it.ID]; ok {
			it.Seq = old.Seq
			if !sameItem(old, it) {
				f.seq++
				it.Seq = f.seq
			}
		} else {
			f.seq++
			it.Seq = f.seq
		}
		next[it.ID] = it
	}
	f.items = next
	select {
	case f.poke <- struct{}{}:
	default:
	}
}

func (f *fleet) call(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
	str := func(k string) string {
		s, _ := params[k].(string)
		return s
	}
	agent := func() (*webshell.Agent, error) {
		if a := webshell.FindAgent(str("window")); a != nil {
			return a, nil
		}
		return nil, &session.VerbCallError{Code: session.ErrVerbNoTranscript, Message: "no agent runs in this pane"}
	}
	switch verb {
	case "agent-transcript":
		a, err := agent()
		if err != nil {
			return nil, err
		}
		v := a.View()
		return reply(map[string]any{"entries": v.Entries, "cursor": strconv.FormatUint(v.Rev, 10), "reset": true, "more": false})
	case "peek-prompt":
		return f.peek(str("window"))
	case "respond":
		return f.respond(str("window"), str("action"), str("value"), str("prompt_id"))
	case "reply-approval":
		return f.replyApproval(str("request_id"), str("decision"), str("message"))
	case "queue-prompt":
		return f.queue(str("window"), str("text"))
	case "dismiss-attention":
		return f.dismiss(str("id"))
	case "list-checkpoints":
		a, err := agent()
		if err != nil {
			return reply(map[string]any{"enabled": true, "checkpoints": []any{}})
		}
		return reply(map[string]any{"enabled": true, "checkpoints": a.View().Checkpoints})
	case "agent-activity":
		a, err := agent()
		if err != nil {
			return nil, err
		}
		return reply(map[string]any{"recap": recap(a.View())})
	case "artifact-list":
		return reply(map[string]any{"artifacts": []any{}})
	case "start-agent":
		return f.start([]string{str("agent")}, str("prompt"), 1, "")
	case "fan":
		n, _ := params["count"].(int)
		specs, _ := params["agents"].([]string)
		if len(specs) == 0 {
			specs = []string{str("agent")}
		}
		return f.start(specs, str("prompt"), max(n, 1), str("name"))
	case "compare-fan":
		return f.compare(str("session"))
	case "judge-fan":
		return f.judge(str("session"))
	case "keep-fan":
		return f.keep(str("session"))
	}
	return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: "the tour has no " + verb}
}

func reply(v any) (json.RawMessage, error) {
	b, err := jsonx.Marshal(v)
	return b, err
}

func recap(v webshell.AgentView) session.AgentActivityRecap {
	rc := session.AgentActivityRecap{Files: []string{}, State: v.State, LastSaid: v.Message}
	start := 0
	for i, e := range v.Entries {
		if e.Kind == "text" && e.Role == "user" {
			start, rc.Turns = i, 1
		}
	}
	for _, e := range v.Entries[start:] {
		switch {
		case e.Kind == "tool_call" && e.Diff != nil && e.Status == "ok":
			rc.Files = append(rc.Files, e.Target)
			rc.Since = e.At * int64(time.Millisecond)
		case e.Kind == "tool_call" && e.Tool == "Bash":
			rc.Commands++
		}
	}
	rc.FilesTotal = len(rc.Files)
	if len(v.Entries) > start {
		rc.Since = v.Entries[start].At * int64(time.Millisecond)
	}
	return rc
}

func (f *fleet) peek(window string) (json.RawMessage, error) {
	a := webshell.FindAgent(window)
	res := session.PromptPeek{Session: learnSession, Window: window, Lines: []string{}, Options: []harness.Option{}, Actions: []string{}}
	if a == nil {
		res.Reason = "no agent runs in this pane"
		return reply(res)
	}
	v := a.View()
	res.Name, res.Harness, res.State = a.Name, a.Harness, v.State
	p := v.Prompt
	if p == nil {
		res.Reason = "no prompt on the screen"
		return reply(res)
	}
	res.Found, res.Blocked, res.Answerable = true, true, true
	res.Kind, res.PromptID, res.Source = p.Kind, p.ID, "screen"
	res.StateAt = p.Since
	res.WaitingMS = time.Since(time.Unix(0, p.Since)).Milliseconds()
	res.Lines = p.Lines
	for i, o := range p.Options {
		res.Options = append(res.Options, harness.Option{N: i + 1, Label: o})
	}
	res.Actions = []string{harness.ActionChoose}
	if p.Kind == session.AttentionApproval {
		res.Actions = []string{harness.ActionApprove, harness.ActionApproveAlways, harness.ActionDeny, harness.ActionChoose}
	}
	return reply(res)
}

func (f *fleet) respond(window, action, value, promptID string) (json.RawMessage, error) {
	a := webshell.FindAgent(window)
	var p *webshell.AgentPrompt
	if a != nil {
		p = a.View().Prompt
	}
	if p == nil || p.ID != promptID {
		return nil, &session.VerbCallError{Code: session.ErrVerbPromptChanged, Message: "the prompt changed"}
	}
	keys := value
	switch action {
	case harness.ActionApprove:
		keys = "1"
	case harness.ActionApproveAlways:
		keys = "2"
	case harness.ActionDeny:
		keys = strconv.Itoa(len(p.Options))
	case harness.ActionChoose:
		if n, err := strconv.Atoi(value); err != nil || n < 1 || n > len(p.Options) {
			return nil, &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: "no option " + value}
		}
	default:
		return nil, &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: "this prompt does not take " + action}
	}
	a.Type(keys)
	state := waitMoved(a, promptID)
	return reply(session.PromptResponse{Window: window, Action: action, Sent: keys, PromptID: promptID, SettledBy: "state", State: state})
}

func waitMoved(a *webshell.Agent, promptID string) string {
	until := time.Now().Add(fleetWait)
	for {
		v := a.View()
		if v.Prompt == nil || v.Prompt.ID != promptID || time.Now().After(until) {
			return v.State
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (f *fleet) replyApproval(requestID, decision, message string) (json.RawMessage, error) {
	f.mu.Lock()
	it, ok := f.items[requestID]
	f.mu.Unlock()
	a := webshell.FindAgent(it.Window)
	if !ok || it.RequestID == "" || a == nil {
		return reply(map[string]any{"applied": false, "reason": "gone"})
	}
	if p := a.View().Prompt; p == nil || p.ID != requestID {
		return reply(map[string]any{"applied": false, "reason": "gone"})
	}
	switch decision {
	case session.ApprovalAsk:
		f.mu.Lock()
		f.unheld[requestID] = true
		f.mu.Unlock()
		f.reconcile()
		return reply(map[string]any{"decision": decision, "applied": true})
	case session.ApprovalOnce:
		a.Type("1")
	case session.ApprovalAlways:
		a.Type("2")
	case session.ApprovalDeny:
		text := "3"
		if m := strings.TrimSpace(message); m != "" {
			text += m + "\r"
		}
		a.Type(text)
	default:
		return nil, &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: "unknown decision " + decision}
	}
	return reply(map[string]any{"decision": decision, "applied": true})
}

func (f *fleet) queue(window, text string) (json.RawMessage, error) {
	if webshell.FindAgent(window) == nil {
		return nil, &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: "no agent runs in this pane"}
	}
	text = strings.Join(strings.Fields(text), " ")
	f.mu.Lock()
	f.seq++
	id := "q" + strconv.FormatUint(f.seq, 10)
	f.queued[window] = append(f.queued[window], text)
	n := len(f.queued[window])
	f.mu.Unlock()
	f.reconcile()
	f.mu.Lock()
	waiting := len(f.queued[window])
	f.mu.Unlock()
	return reply(map[string]any{"id": id, "position": n, "queued": waiting, "delivering": waiting < n})
}

func (f *fleet) dismiss(id string) (json.RawMessage, error) {
	f.mu.Lock()
	_, ok := f.items[id]
	if ok {
		f.dismissed[id] = true
	}
	f.mu.Unlock()
	if !ok {
		return nil, &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: "no open item " + id, Hint: &session.VerbHint{Param: "id"}}
	}
	f.reconcile()
	return reply(map[string]any{})
}

func (f *fleet) start(specs []string, task string, n int, group string) (json.RawMessage, error) {
	if task == "" {
		return nil, &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: "a task is required"}
	}
	n = min(max(n, 1), 8)
	msg := spawnAgentsMsg{task: task}
	for i := range n {
		spec := specs[min(i, len(specs)-1)]
		name, _, _ := strings.Cut(strings.TrimSpace(spec), " ")
		if name == "" {
			name = "claude"
		}
		msg.agents = append(msg.agents, name)
	}
	res := map[string]any{"session": learnSession, "total": n, "ready": true, "prompt_status": session.PromptSent}
	if group != "" {
		f.mu.Lock()
		f.seq++
		fan := &learnFan{id: "fan" + strconv.FormatUint(f.seq, 10), group: group, task: task, agent: msg.agents[0]}
		stem := group[strings.LastIndexByte(group, '/')+1:]
		for i := range n {
			name := stem
			if i > 0 {
				name += "-" + strconv.Itoa(i+1)
			}
			for f.attempts[name].fan != "" || f.fans[name] != nil {
				name += "x"
			}
			fan.sessions = append(fan.sessions, name)
			f.attempts[name] = fanAttempt{fan: fan.id}
		}
		f.fans[fan.id] = fan
		f.mu.Unlock()
		msg.fan = fan
		res["group"] = group
		res["branch"] = group
		res["session"] = fan.sessions[0]
	}
	f.spawn(msg)
	return reply(res)
}

func (f *fleet) placed(session, window string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.attempts[session]; ok {
		a.window = window
		f.attempts[session] = a
	}
}

func (f *fleet) fanOf(name string) (*learnFan, error) {
	if a, ok := f.attempts[name]; ok {
		if fan := f.fans[a.fan]; fan != nil {
			return fan, nil
		}
	}
	return nil, &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: name + " is not an attempt of a fan"}
}

func (f *fleet) compare(name string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fan, err := f.fanOf(name)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	for _, s := range fan.sessions {
		row := map[string]any{"session": s, "branch": fan.group, "agent": fan.agent}
		a := webshell.FindAgent(f.attempts[s].window)
		if a == nil {
			row["gone"], row["state"] = true, "none"
			rows = append(rows, row)
			continue
		}
		v := a.View()
		row["agent"], row["harness"], row["state"] = a.Name, a.Harness, v.State
		files, added, removed := 0, 0, 0
		var stats, lines []map[string]any
		for _, e := range v.Entries {
			if e.Kind != "tool_call" || e.Diff == nil || e.Status != "ok" {
				continue
			}
			files++
			added += e.Diff.Added
			removed += e.Diff.Removed
			stats = append(stats, map[string]any{"path": e.Diff.File, "added": e.Diff.Added, "removed": e.Diff.Removed})
			for _, h := range e.Diff.Hunks {
				n := h.NewStart
				for _, l := range h.Lines {
					if l.Op != " " {
						lines = append(lines, map[string]any{"file": e.Diff.File, "op": l.Op, "line": n, "text": l.Text})
					}
					if l.Op != "-" {
						n++
					}
				}
			}
		}
		row["files"], row["added"], row["removed"] = files, added, removed
		row["file_list"], row["lines"] = stats, lines
		if len(v.Entries) > 0 {
			row["started_at"] = v.Entries[0].At * int64(time.Millisecond)
			if v.State == "done" {
				row["done_at"] = v.Entries[len(v.Entries)-1].At * int64(time.Millisecond)
			}
		}
		rows = append(rows, row)
	}
	repo := webshell.Home + "/projects/website"
	res := map[string]any{"group": fan.group, "fan_id": fan.id, "repo": "website", "repo_root": repo, "base": "main",
		"prompt": fan.task, "rows": rows}
	if fan.verdict != nil {
		res["verdict"] = fan.verdict
	}
	return reply(res)
}

func (f *fleet) judge(name string) (json.RawMessage, error) {
	raw, err := f.compare(name)
	if err != nil {
		return nil, err
	}
	var res struct {
		Rows []struct {
			Session string `json:"session"`
			Added   int    `json:"added"`
			State   string `json:"state"`
		} `json:"rows"`
	}
	_ = jsonx.Unmarshal(raw, &res)
	pick, most := "", -1
	for _, r := range res.Rows {
		if r.State == "done" && r.Added > most {
			pick, most = r.Session, r.Added
		}
	}
	if pick == "" {
		return nil, &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: "no attempt has finished yet"}
	}
	f.mu.Lock()
	if fan, err := f.fanOf(name); err == nil {
		fan.verdict = map[string]any{
			"pick": pick, "summary": "It has the most complete plan.",
			"reasons": []string{"It lists the most steps.", "Each step is small."},
			"by":      "claude", "judge": "claude", "at": time.Now().UnixNano(),
		}
	}
	f.mu.Unlock()
	return reply(map[string]any{"judge_session": "judge", "ready": true})
}

func (f *fleet) keep(name string) (json.RawMessage, error) {
	f.mu.Lock()
	fan, err := f.fanOf(name)
	if err != nil {
		f.mu.Unlock()
		return nil, err
	}
	var gone []string
	var removed []map[string]any
	for _, s := range fan.sessions {
		if s == name {
			continue
		}
		if w := f.attempts[s].window; w != "" {
			gone = append(gone, w)
		}
		removed = append(removed, map[string]any{"session": s, "removed": true})
		delete(f.attempts, s)
	}
	fan.sessions = []string{name}
	f.mu.Unlock()
	f.close(gone)
	return reply(map[string]any{"removed": removed, "left": 0})
}

type spawnAgentsMsg struct {
	agents []string
	task   string
	fan    *learnFan
}

func (m *Model) spawnAgents(msg spawnAgentsMsg) {
	o := m.OS
	for i, agent := range msg.agents {
		o.AddWindow("")
		id := o.GetFocusedWindowID()
		if id == "" {
			continue
		}
		_ = o.SendToWindow(id, []byte(agent+" "+strconv.Quote(msg.task)+"\r"))
		if fan := msg.fan; fan != nil && i < len(fan.sessions) {
			m.fleet.placed(fan.sessions[i], id)
			branch := fan.group
			if i > 0 {
				branch += "-" + strconv.Itoa(i+1)
			}
			o.LearnSetFanAttempt(id, app.LearnFanAttempt{Group: fan.group, FanID: fan.id, Branch: branch,
				Prompt: msg.task, Repo: webshell.Home + "/projects/website", Session: fan.sessions[i]})
		}
	}
	o.MarkAllDirty()
}

type closeWindowsMsg struct{ windows []string }

func spawnInfo() app.LearnSpawnInfo {
	return app.LearnSpawnInfo{
		Agents: []app.LearnSpawnAgent{
			{ID: webshell.AgentHarness, Cmd: "claude"},
			{ID: "codex", Cmd: "codex"},
			{ID: "gemini-cli", Cmd: "gemini"},
		},
		Folder:   webshell.Home + "/projects/website",
		Repo:     webshell.Home + "/projects/website",
		Head:     "main",
		Branches: []string{"main"},
		Machine:  "this tab",
	}
}
