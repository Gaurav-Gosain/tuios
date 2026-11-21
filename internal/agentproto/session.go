package agentproto

import (
	"context"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Reporter carries the session's state to the daemon. A nil Reporter reports
// nothing, which is how the program runs outside tuios.
type Reporter interface {
	// Report sets the pane's agent state. kind is approval for a
	// permission, and ifState, when set, is the states the pane must be in
	// for the report to apply.
	Report(ctx context.Context, state, kind, message, ifState string) error
	// Hold asks the Inbox to hold a permission until the person answers it,
	// and returns the decision, empty for none, with who answered. It ends
	// early, with no decision, when ctx is cancelled.
	Hold(ctx context.Context, line string, options []string) (decision, by string, err error)
}

// Session runs one agent in a pane.
type Session struct {
	Agent Agent
	// Events is where the agent's client emits; see Emit.
	Events chan Event
	In     io.Reader
	Out    io.Writer
	// Width is the pane's width in cells, 80 when nil or unknown.
	Width    func() int
	Reporter Reporter
	// Header is the first line of the transcript, naming what runs.
	Header string
	// Cwd is where the conversation opens.
	Cwd string
	// Stderr returns the last lines the agent wrote to stderr, shown when it
	// exits.
	Stderr func() string
	// Settle is how long a permission must be on screen before a key answers
	// it, so a key typed for the prompt line does not. Zero means
	// defaultSettle.
	Settle time.Duration
	// Now is the clock, time.Now when nil.
	Now func() time.Time

	r           *renderer
	buf         []rune
	shown       bool
	work        workPhase
	ready       bool
	queued      string
	reply       strings.Builder
	perms       []*Permission
	shownAt     time.Time
	hold        context.CancelFunc
	holds       chan holdResult
	meta        map[string]string
	feed        *feed
	feedRetry   time.Duration
	toolPhase   map[string]int
	keysHandled func()
	waiting     bool
}

type workPhase uint8

const (
	workNone workPhase = iota
	workTurn
	workOwn
)

type workEvent uint8

const (
	evPrompt workEvent = iota
	evTurnEnd
	evAgentRunning
	evAgentIdle
)

func (p workPhase) after(ev workEvent) (next workPhase, moved bool) {
	switch {
	case ev == evPrompt && p != workTurn:
		return workTurn, true
	case ev == evTurnEnd && p == workTurn:
		return workNone, true
	case ev == evAgentRunning && p == workNone:
		return workOwn, true
	case ev == evAgentIdle && p == workOwn:
		return workNone, true
	}
	return p, false
}

const defaultSettle = 500 * time.Millisecond

const reportTimeout = 2 * time.Second

// Emit is the emit function to make the agent's client with.
func (s *Session) Emit(ev Event) { s.Events <- ev }

// NewEvents is an event channel deep enough that the client's read goroutine
// does not wait on the pane in a burst.
func NewEvents() chan Event { return make(chan Event, 4096) }

type holdResult struct {
	perm     *Permission
	decision string
	by       string
}

type turnResult struct {
	res TurnResult
	err error
}

// Run runs the session until the person quits or the agent is gone. It
// returns the process exit code.
func (s *Session) Run(ctx context.Context) int {
	s.r = newRenderer()
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Settle <= 0 {
		s.Settle = defaultSettle
	}
	mr, _ := s.Reporter.(MetaReporter)
	ar, _ := s.Reporter.(ActivityReporter)
	if s.feed = newFeed(mr, ar, s.feedRetry); s.feed != nil {
		defer s.feed.stop()
	}
	keys := make(chan []key, 64)
	go func() {
		var p keyParser
		buf := make([]byte, 4096)
		for {
			n, err := s.In.Read(buf)
			if n > 0 {
				if ks := p.feed(buf[:n]); len(ks) > 0 {
					keys <- ks
				}
			}
			if err != nil {
				close(keys)
				return
			}
		}
	}()

	s.write(s.r.line(oneLine(s.Header)))
	type startResult struct {
		info Info
		err  error
	}
	started := make(chan startResult, 1)
	go func() {
		info, err := s.Agent.Start(ctx, s.Cwd)
		started <- startResult{info, err}
	}()
	turns := make(chan turnResult, 1)
	s.holds = make(chan holdResult, 4)
	gone := s.Agent.Done()
	for {
		select {
		case <-ctx.Done():
			s.endHold()
			return 1
		case st := <-started:
			started = nil
			if st.err != nil {
				s.fail("the agent did not start: " + st.err.Error())
				return s.waitToClose(keys)
			}
			s.ready = true
			s.write(s.r.line(oneLine(startedLine(st.info))))
			s.report("idle", "", "", "")
			if st.info.Model != "" {
				s.setMeta(map[string]string{MetaModel: oneLine(st.info.Model)})
			}
			s.write(s.r.line("Type a prompt and press Enter. Ctrl+C cancels a turn, Ctrl+D on an empty line quits."))
			if s.queued != "" {
				text := s.queued
				s.queued = ""
				s.startTurn(ctx, text, turns)
			}
		case ev := <-s.Events:
			s.onEvent(ev)
		case t := <-turns:
			s.drain()
			s.endTurn(t)
		case h := <-s.holds:
			s.onHold(h)
		case ks, ok := <-keys:
			if !ok {
				s.cancelAll()
				return 0
			}
			for _, k := range ks {
				if quit := s.onKey(ctx, k, turns); quit {
					s.cancelAll()
					return 0
				}
			}
			s.redrawInput()
			if s.keysHandled != nil {
				s.keysHandled()
			}
		case <-gone:
			s.drain()
			s.cancelAll()
			msg := "the agent exited"
			if s.Stderr != nil {
				if tail := strings.TrimSpace(s.Stderr()); tail != "" {
					s.write(s.r.line(indentLines(clean(tail))))
				}
			}
			s.fail(msg)
			return s.waitToClose(keys)
		}
	}
}

func startedLine(info Info) string {
	parts := []string{"connected"}
	if info.Agent != "" {
		parts = append(parts, "to "+info.Agent)
	}
	if info.Model != "" {
		parts = append(parts, "("+info.Model+")")
	}
	line := strings.Join(parts, " ")
	if info.Protocol > 0 {
		line += ", ACP version " + strconv.Itoa(info.Protocol)
	}
	return line
}

func (s *Session) drain() {
	for {
		select {
		case ev := <-s.Events:
			s.onEvent(ev)
		default:
			return
		}
	}
}

func (s *Session) onEvent(ev Event) {
	switch e := ev.(type) {
	case *Permission:
		s.perms = append(s.perms, e)
		if len(s.perms) == 1 {
			s.showPermission()
		}
	case Text:
		if !e.Thought && s.reply.Len() < 4096 {
			s.reply.WriteString(e.Text)
		}
		s.write(s.r.event(e))
	case Usage:
		s.setMeta(metaFromUsage(e))
	case Plan:
		if p := planProgress(e); p != "" {
			s.setMeta(map[string]string{MetaPlan: p})
		}
		s.write(s.r.event(e))
	case Tool:
		s.toolActivity(e)
		s.write(s.r.event(e))
	case AgentState:
		s.onAgentState(e.State)
	case Withdrawn:
		s.onWithdrawn(e.Permission)
	default:
		s.write(s.r.event(ev))
	}
}

func (s *Session) onAgentState(state string) {
	switch state {
	case "requires_action":
		if s.waiting {
			return
		}
		s.waiting = true
		if len(s.perms) == 0 {
			s.report("needs_input", "", "the agent is waiting for input", "")
		}
	case "running":
		if s.waiting {
			s.waiting = false
			if len(s.perms) == 0 {
				s.report("working", "", "", "needs_input")
			}
		}
		if next, moved := s.work.after(evAgentRunning); moved {
			s.work = next
			s.write(s.r.line("the agent is working on its own"))
			s.report("working", "", "", "")
		}
	case "idle":
		s.waiting = false
		if next, moved := s.work.after(evAgentIdle); moved {
			s.work = next
			s.write(s.r.line("the agent stopped working on its own"))
			s.report("done", "", "", "")
		}
	default:
		s.write(s.r.line("the agent reports the state " + oneLine(state)))
	}
}

func (s *Session) onWithdrawn(p *Permission) {
	i := slices.Index(s.perms, p)
	if i < 0 {
		return
	}
	s.write(s.r.answered(p, -1, "the agent withdrew the question"))
	if i > 0 {
		s.perms = slices.Delete(s.perms, i, i+1)
		return
	}
	s.endHold()
	s.perms = s.perms[1:]
	if len(s.perms) > 0 {
		s.showPermission()
		return
	}
	if s.waiting {
		return
	}
	s.report("working", "", "", "needs_input")
}

func (s *Session) showPermission() {
	p := s.perms[0]
	s.write(s.r.permission(p))
	s.shownAt = s.Now()
	line := InboxLine(p)
	s.report("needs_input", "approval", StateLine(p), "")
	if line == "" || s.Reporter == nil {
		return
	}
	var options []string
	for _, d := range []string{DecisionOnce, DecisionDeny} {
		if p.Pick(d) >= 0 {
			options = append(options, d)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.hold = cancel
	holds := s.holds
	go func() {
		decision, by, _ := s.Reporter.Hold(ctx, line, options)
		holds <- holdResult{perm: p, decision: decision, by: by}
	}()
}

func (s *Session) onHold(h holdResult) {
	if len(s.perms) == 0 || s.perms[0] != h.perm || h.decision == "" {
		return
	}
	i := h.perm.Pick(h.decision)
	if i < 0 {
		return
	}
	how := "from the Inbox"
	if h.by != "" {
		how += " by " + h.by
	}
	s.answer(i, how)
}

func (s *Session) answer(i int, how string) {
	p := s.perms[0]
	s.endHold()
	if i >= 0 {
		p.Choose(i)
	} else {
		p.Cancel()
	}
	s.write(s.r.answered(p, i, how))
	s.perms = s.perms[1:]
	s.report("working", "", "", "needs_input")
	if len(s.perms) > 0 {
		s.showPermission()
	}
}

func (s *Session) endHold() {
	if s.hold != nil {
		s.hold()
		s.hold = nil
	}
}

func (s *Session) onKey(ctx context.Context, k key, turns chan turnResult) bool {
	if len(s.perms) > 0 {
		return s.permissionKey(k)
	}
	switch k.kind {
	case keyRune:
		s.buf = append(s.buf, k.r)
	case keyPaste:
		s.buf = append(s.buf, []rune(k.text)...)
	case keyBackspace:
		if len(s.buf) > 0 {
			s.buf = s.buf[:len(s.buf)-1]
		}
	case keyCtrlU:
		s.buf = s.buf[:0]
	case keyCtrlC:
		if s.work == workTurn {
			s.Agent.Cancel()
			s.write(s.r.line("cancelling the turn"))
		} else {
			s.buf = s.buf[:0]
		}
	case keyCtrlD:
		if len(s.buf) == 0 && s.work != workTurn {
			return true
		}
	case keyEnter:
		text := strings.TrimSpace(string(s.buf))
		if text == "" {
			break
		}
		switch {
		case s.work == workTurn:
			s.write(s.r.line("a turn is running: wait for it to end, or press Ctrl+C to cancel it"))
			return false
		case !s.ready:
			s.queued = text
			s.buf = s.buf[:0]
			s.write(s.r.line("the prompt is sent once the agent is ready"))
			return false
		}
		s.buf = s.buf[:0]
		s.startTurn(ctx, text, turns)
	}
	return false
}

func (s *Session) permissionKey(k key) bool {
	switch k.kind {
	case keyCtrlC:
		queued := s.perms[1:]
		s.perms = s.perms[:1]
		s.answer(-1, "in the pane, which cancels the turn")
		for _, p := range queued {
			p.Cancel()
			s.write(s.r.answered(p, -1, "in the pane, which cancels the turn"))
		}
		s.Agent.Cancel()
		return false
	case keyRune:
		if k.r < '1' || k.r > '9' {
			return false
		}
		if s.Now().Sub(s.shownAt) < s.Settle {
			return false
		}
		i := int(k.r - '1')
		if i >= len(s.perms[0].Options) {
			return false
		}
		s.answer(i, "in the pane")
	}
	return false
}

func (s *Session) startTurn(ctx context.Context, text string, turns chan turnResult) {
	s.work, _ = s.work.after(evPrompt)
	s.reply.Reset()
	s.write(s.r.prompt(text))
	s.report("working", "", "", "")
	if s.feed != nil && len(s.meta) > 0 {
		s.feed.resync()
	}
	s.toolPhase = nil
	s.activity(Activity{Event: ActivityPrompt, Text: firstLine(text)})
	go func() {
		res, err := s.Agent.Prompt(ctx, text)
		turns <- turnResult{res: res, err: err}
	}()
}

func (s *Session) endTurn(t turnResult) {
	s.work, _ = s.work.after(evTurnEnd)
	if len(s.perms) > 0 {
		s.endHold()
		for _, p := range s.perms {
			p.Cancel()
			s.write(s.r.answered(p, -1, "the turn ended"))
		}
		s.perms = nil
	}
	res := t.res
	if t.err != nil && res.Stop == "" {
		res = TurnResult{Stop: StopFailed, Detail: t.err.Error()}
	}
	s.write(s.r.turnEnd(res))
	endState := "done"
	switch res.Stop {
	case StopFailed, StopRefused:
		endState = "errored"
		s.report(endState, "", oneLine(firstOf(res.Detail, "turn "+res.Stop)), "")
	case StopCancelled:
		endState = "idle"
		s.report(endState, "", "", "")
	default:
		s.report(endState, "", firstLine(s.reply.String()), "")
	}
	end := Activity{Event: ActivityTurnEnd, Text: firstLine(s.reply.String()), State: endState}
	if res.Stop != StopFinished {
		end.Text = oneLine(firstOf(res.Detail, "turn "+res.Stop))
	}
	s.activity(end)
}

func (s *Session) setMeta(tokens map[string]string) {
	if len(tokens) == 0 {
		return
	}
	if s.meta == nil {
		s.meta = map[string]string{}
	}
	changed := false
	for k, v := range tokens {
		if s.meta[k] != v {
			s.meta[k] = v
			changed = true
		}
	}
	if changed && s.feed != nil {
		s.feed.setMeta(s.meta)
	}
}

func (s *Session) toolActivity(t Tool) {
	if t.ID == "" {
		return
	}
	if s.toolPhase == nil {
		s.toolPhase = map[string]int{}
	}
	phase := s.toolPhase[t.ID]
	tool, target := activityTool(t)
	switch t.Status {
	case ToolDone, ToolFailed:
		if phase >= 2 {
			return
		}
		s.toolPhase[t.ID] = 2
		ok := t.Status == ToolDone
		ev := ActivityToolDone
		if !ok {
			ev = ActivityToolFailed
		}
		s.activity(Activity{Event: ev, Tool: tool, Target: target, OK: &ok})
	default:
		if phase >= 1 {
			return
		}
		s.toolPhase[t.ID] = 1
		s.activity(Activity{Event: ActivityTool, Tool: tool, Target: target})
	}
}

func (s *Session) activity(a Activity) {
	if s.feed == nil {
		return
	}
	if a.State == "" {
		a.State = "working"
	}
	s.feed.activity(a)
}

func firstLine(s string) string {
	for l := range strings.SplitSeq(clean(s), "\n") {
		if l = oneLine(l); l != "" {
			return ansi.Truncate(l, 120, "...")
		}
	}
	return ""
}

func (s *Session) cancelAll() {
	s.endHold()
	for _, p := range s.perms {
		p.Cancel()
	}
	s.perms = nil
	if s.work == workTurn {
		s.Agent.Cancel()
	}
}

func (s *Session) fail(msg string) {
	s.write(s.r.event(Notice{Text: msg, Error: true}))
	s.report("errored", "", oneLine(msg), "")
}

func (s *Session) waitToClose(keys chan []key) int {
	s.write(s.r.line("Press Enter to close this pane."))
	for ks := range keys {
		for _, k := range ks {
			switch k.kind {
			case keyEnter, keyCtrlC, keyCtrlD:
				return 1
			}
		}
	}
	return 1
}

func (s *Session) report(state, kind, message, ifState string) {
	if s.Reporter == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
	defer cancel()
	_ = s.Reporter.Report(ctx, state, kind, message, ifState)
}

func (s *Session) write(text string) {
	if text == "" {
		return
	}
	s.hideInput()
	_, _ = io.WriteString(s.Out, strings.ReplaceAll(text, "\n", "\r\n"))
	s.showInput()
}

func (s *Session) hideInput() {
	if s.shown {
		_, _ = io.WriteString(s.Out, "\r\x1b[K")
		s.shown = false
	}
}

func (s *Session) showInput() {
	if s.r.midLine {
		return
	}
	_, _ = io.WriteString(s.Out, s.inputLine())
	s.shown = true
}

func (s *Session) redrawInput() {
	s.hideInput()
	s.showInput()
}

func (s *Session) inputLine() string {
	width := 80
	if s.Width != nil {
		if w := s.Width(); w > 0 {
			width = w
		}
	}
	if len(s.perms) > 0 {
		return ansi.Truncate(sgrBold+"answer> "+sgrReset+sgrDim+"press a number"+sgrReset, width-1, "")
	}
	if len(s.buf) == 0 {
		hint := "type a prompt"
		switch {
		case !s.ready:
			hint = "starting"
		case s.work == workTurn:
			hint = "working, Ctrl+C cancels"
		}
		return ansi.Truncate(sgrBold+"> "+sgrReset+sgrDim+hint+sgrReset, width-1, "")
	}
	text := strings.ReplaceAll(clean(string(s.buf)), "\n", "↵")
	avail := width - 3
	if w := ansi.StringWidth(text); avail > 0 && w > avail {
		text = ansi.TruncateLeft(text, w-avail, "")
	}
	return sgrBold + "> " + sgrReset + text
}
