package agentproto

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// The pane program: one agent over its protocol, shown as a transcript with a
// prompt line under it.
//
// The pane is where the person talks to the agent, and it is also what tuios
// already knows how to drive: a prompt ask-agent or start-agent types arrives
// as a paste and a carriage return, the transcript is plain text capture-pane
// reads, and the agent's state reaches the rail and the Inbox as reports from
// this process for its own pane, the way a hook reports.
//
// A permission request is shown in the pane with a numbered key per answer,
// and reported as needs_input. When its line is the whole request (InboxLine),
// the Inbox is asked to hold it with request-approval as well, and whichever
// answers first, the pane or the Inbox, answers the agent. The other is then
// told: an answer in the pane ends the hold by closing its connection, and an
// answer from the Inbox is written into the pane. A hold that ends without a
// decision leaves the pane's question up, so the person can always answer
// there.

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

	r       *renderer
	buf     []rune
	shown   bool
	running bool
	ready   bool
	queued  string
	reply   strings.Builder
	perms   []*Permission
	shownAt time.Time
	hold    context.CancelFunc
	holds   chan holdResult
	// meta is the pane's agent metadata as the agent last stated it. feed
	// sends it, when the Reporter is a MetaReporter, and the activity, when
	// it is an ActivityReporter, off the pane's loop.
	meta map[string]string
	feed *feed
	// feedRetry is how long a failed metadata call waits to be tried again,
	// feedRetryAfter when zero. Tests shorten it.
	feedRetry time.Duration
	// toolPhase is how far each tool call of the turn has been reported as
	// activity: 1 started, 2 ended.
	toolPhase map[string]int
	// keysHandled, when set, is called after Run has handled the keys of one
	// read. Tests use it to know a key was handled, not only read.
	keysHandled func()
}

// defaultSettle is how long a permission is on screen before a key answers it,
// the Inbox's own wait (inboxAnswerSettle in internal/app) less a little, since
// the pane prompt is read where it is typed.
const defaultSettle = 500 * time.Millisecond

// reportTimeout bounds one state report, so a slow daemon slows the pane by at
// most this much.
const reportTimeout = 2 * time.Second

// Emit is the emit function to make the agent's client with.
func (s *Session) Emit(ev Event) { s.Events <- ev }

// NewEvents is an event channel deep enough that the client's read goroutine
// does not wait on the pane in a burst.
func NewEvents() chan Event { return make(chan Event, 4096) }

// holdResult is how an Inbox hold ended.
type holdResult struct {
	perm     *Permission
	decision string
	by       string
}

// turnResult is how a Prompt call returned.
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
			// The agent's updates for the turn were emitted before its
			// Prompt call returned, and select picks between ready channels
			// at random, so show what is already queued first: the reply's
			// last words belong to the turn that is ending.
			s.drain()
			s.endTurn(t)
		case h := <-s.holds:
			s.onHold(h)
		case ks, ok := <-keys:
			if !ok {
				// The pane's terminal is gone: nobody can answer here.
				s.cancelAll()
				return 0
			}
			for _, k := range ks {
				if quit := s.onKey(ctx, k, turns); quit {
					s.cancelAll()
					return 0
				}
			}
			// One redraw for what one read brought, not one per key.
			s.redrawInput()
			if s.keysHandled != nil {
				s.keysHandled()
			}
		case <-gone:
			gone = nil
			// Show whatever it said before it went.
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

// startedLine says what started.
func startedLine(info Info) string {
	parts := []string{"connected"}
	if info.Agent != "" {
		parts = append(parts, "to "+info.Agent)
	}
	if info.Model != "" {
		parts = append(parts, "("+info.Model+")")
	}
	return strings.Join(parts, " ")
}

// drain shows events already emitted, so the agent's last words come before
// the line saying it exited.
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
	default:
		s.write(s.r.event(ev))
	}
}

// showPermission puts the first queued permission on screen and reports it.
func (s *Session) showPermission() {
	p := s.perms[0]
	s.write(s.r.permission(p))
	s.shownAt = s.Now()
	line := InboxLine(p)
	// The report comes first and is waited for: a hold is only opened on a
	// pane already on needs_input, and a later report must not land before
	// this one.
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

// onHold takes an Inbox hold's result: a decision answers the permission it
// was for, if that is still the one on screen.
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

// answer answers the permission on screen with option i, or cancels it when i
// is negative, and moves to the next.
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

// endHold ends a running Inbox hold, which the daemon sees as its caller
// gone and clears from the Inbox item.
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
		if s.running {
			s.Agent.Cancel()
			s.write(s.r.line("cancelling the turn"))
		} else {
			s.buf = s.buf[:0]
		}
	case keyCtrlD:
		if len(s.buf) == 0 && !s.running {
			return true
		}
	case keyEnter:
		text := strings.TrimSpace(string(s.buf))
		if text == "" {
			break
		}
		switch {
		case s.running:
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

// permissionKey handles a key while a permission is on screen. Only a digit
// answers, and only once the question has been on screen for Settle: a paste,
// an Enter or a key typed before it appeared answers nothing.
func (s *Session) permissionKey(k key) bool {
	switch k.kind {
	case keyCtrlC:
		s.answer(-1, "in the pane, which cancels the turn")
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
	s.running = true
	s.reply.Reset()
	s.write(s.r.prompt(text))
	s.report("working", "", "", "")
	// Metadata the daemon lost since the last turn (a restart, or a clear
	// to none) comes back with the turn, not only when a value changes.
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
	s.running = false
	// A permission still up belongs to a turn that is over.
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

// setMeta takes metadata the agent stated and sends what changed.
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

// toolActivity reports a tool call as activity once when it starts and once
// when it ends, however many updates the protocol sends about it.
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

// activity hands one activity to the feed, when the Reporter takes activity.
// It is sent off the pane's loop, so a slow daemon does not hold the pane at
// every tool call.
func (s *Session) activity(a Activity) {
	if s.feed == nil {
		return
	}
	if a.State == "" {
		a.State = "working"
	}
	s.feed.activity(a)
}

// firstLine is the first non-empty line of s, cleaned and clipped for a state
// message.
func firstLine(s string) string {
	for l := range strings.SplitSeq(clean(s), "\n") {
		if l = oneLine(l); l != "" {
			return ansi.Truncate(l, 120, "...")
		}
	}
	return ""
}

// cancelAll answers every permission with cancelled and ends the hold.
func (s *Session) cancelAll() {
	s.endHold()
	for _, p := range s.perms {
		p.Cancel()
	}
	s.perms = nil
	if s.running {
		s.Agent.Cancel()
	}
}

// fail shows why the session cannot go on and reports it.
func (s *Session) fail(msg string) {
	s.write(s.r.event(Notice{Text: msg, Error: true}))
	s.report("errored", "", oneLine(msg), "")
}

// waitToClose keeps the pane open, so what went wrong can be read, until the
// person presses Enter, Ctrl+C or Ctrl+D.
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

// write puts transcript text on screen above the prompt line.
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

// showInput draws the prompt line, unless the transcript has a line open: the
// reply is streaming, and the prompt line comes back when the line ends.
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

// inputLine is the prompt line as drawn: the end of what is typed when it is
// wider than the pane, and a hint when nothing is.
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
		case s.running:
			hint = "working, Ctrl+C cancels"
		}
		return ansi.Truncate(sgrBold+"> "+sgrReset+sgrDim+hint+sgrReset, width-1, "")
	}
	// What is typed is shown cleaned too: a paste into the pane must not
	// become output the pane's terminal acts on.
	text := strings.ReplaceAll(clean(string(s.buf)), "\n", "↵")
	avail := width - 3
	if w := ansi.StringWidth(text); avail > 0 && w > avail {
		text = ansi.TruncateLeft(text, w-avail, "")
	}
	return sgrBold + "> " + sgrReset + text
}
