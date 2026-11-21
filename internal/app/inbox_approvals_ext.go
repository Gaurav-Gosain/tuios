package app

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/theme"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const inboxRiskPressWindow = 3 * time.Second

const inboxApprovalFetchTimeout = 5 * time.Second

type inboxApprovalState struct {
	answer   inboxAnswer
	lastKey  string
	plan     inboxPlanView
	question inboxQuestionView
	refusal  inboxRefusal
}

type inboxRefusal struct {
	requestID string
	text      string
}

func (m *OS) refuseAnswer(it session.AttentionItem, text, level string) {
	m.Inbox.approvals.refusal = inboxRefusal{requestID: it.RequestID, text: text}
	m.cockpitTouch()
	m.ShowNotification(text, level, m.Settings.NotificationDuration)
}

func (m *OS) clearAnswerRefusal() {
	if m.Inbox.approvals.refusal != (inboxRefusal{}) {
		m.Inbox.approvals.refusal = inboxRefusal{}
		m.cockpitTouch()
	}
}

func (m *OS) inboxRefusalLines(it session.AttentionItem, width int) []string {
	r := m.Inbox.approvals.refusal
	if r.text == "" || it.RequestID == "" || r.requestID != it.RequestID {
		return nil
	}
	var lines []string
	for _, l := range wrapPlain(r.text, max(width, 8)) {
		lines = append(lines, "  "+l)
	}
	return lines
}

func (m *OS) answerPressName(decision string) string {
	if k := m.Inbox.approvals.lastKey; k != "" {
		return k
	}
	return inboxDecisionKey(decision)
}

type inboxPeekAsk struct {
	action, value string
}

// InboxPeekAsk asks before a key in the peek answers the prompt: the peek
// says what enter does, and any other key drops it. Typed text (t) is a
// whole answer typed on purpose, and is not asked about.
func (m *OS) InboxPeekAsk(action, value string) {
	p := m.Inbox.Peek
	if p == nil {
		return
	}
	m.answerPeekAsk(&inboxPeekAsk{action: action, value: value})
	what := "Answer the prompt"
	switch action {
	case harness.ActionApprove:
		what = "Allow it"
	case harness.ActionApproveAlways:
		what = "Always allow it"
	case harness.ActionDeny:
		what = "Reject it"
	case harness.ActionChoose:
		what = "Choose " + value
	}
	p.Note = what + "? Press enter to do it. Press esc to cancel."
}

// InboxPeekAskOpen reports whether an answer in the peek waits for enter.
func (m *OS) InboxPeekAskOpen() bool {
	return m.Inbox.Peek != nil && m.Inbox.approvals.answer.peekAsk != nil
}

// InboxPeekAskKey takes the key after an answer key in the peek: enter
// sends the answer, any other key drops it and does nothing else.
func (m *OS) InboxPeekAskKey(key string) tea.Cmd {
	a := m.Inbox.approvals.answer.peekAsk
	m.answerEnd(answerPeekAsk, "key after the peek ask")
	if p := m.Inbox.Peek; p != nil {
		p.Note = ""
	}
	if a == nil || key != "enter" {
		return nil
	}
	return m.inboxAnswer(a.action, a.value, true)
}

type inboxConfirm struct {
	decision string
	itemID   string
	shown    string
}

// InboxConfirmOpen reports whether an answer waits for enter. It goes when
// its item is no longer under the cursor.
func (m *OS) InboxConfirmOpen() bool {
	c := m.Inbox.approvals.answer.confirm
	if c == nil {
		return false
	}
	if it, ok := m.inboxSelected(); !ok || it.ID != c.itemID {
		m.answerEnd(answerConfirm, "its item left the cursor")
		return false
	}
	return true
}

// InboxConfirmKey takes the key after an answer key: enter sends the
// answer, and any other key drops it and does nothing else.
func (m *OS) InboxConfirmKey(key string) tea.Cmd {
	c := m.Inbox.approvals.answer.confirm
	m.answerEnd(answerConfirm, "key after the ask")
	if c == nil || key != "enter" {
		return nil
	}
	if m.refuseRemoteAnswer("An answer") {
		return nil
	}
	it, ok := m.inboxSelected()
	if !ok || it.ID != c.itemID || inboxShownKey(it) != c.shown {
		if ok {
			m.refuseAnswer(it, "Not answered: this prompt changed after the key. Read it, then answer", "warning")
		} else {
			m.ShowNotification("This prompt just changed. Read it, then answer", "warning", m.Settings.NotificationDuration)
		}
		return nil
	}
	return m.inboxReplyCmd(it, c.decision)
}

func (m *OS) inboxConfirmLine(it session.AttentionItem) (string, bool) {
	c := m.Inbox.approvals.answer.confirm
	if c == nil || c.itemID != it.ID {
		return "", false
	}
	target := printableTitle(inboxRiskTarget(it))
	if r := []rune(target); len(r) > 60 {
		target = string(r[:59]) + "…"
	}
	switch c.decision {
	case session.ApprovalOnce:
		return "Allow " + target + "? Press enter to allow. Press esc to cancel.", true
	case session.ApprovalAlways:
		return "Always allow " + target + "? Press enter to allow it always. Press esc to cancel.", true
	case session.ApprovalAsk:
		return "Hand " + target + " back to the pane? Press enter to do it. Press esc to cancel.", true
	}
	return "Reject " + target + "? Press enter to reject. Press esc to cancel.", true
}

func inboxConfirmHints(decision string) []overlay.Hint {
	word := "reject"
	switch decision {
	case session.ApprovalOnce, session.ApprovalAlways:
		word = "allow"
	case session.ApprovalAsk:
		word = "hand back"
	}
	return []overlay.Hint{{Key: overlay.EnterKey(), Label: word}, {Key: "esc", Label: "cancel"}}
}

type inboxArmed struct {
	key   string
	item  string
	at    time.Time
	press string
}

type inboxReasonLine struct {
	itemID    string
	requestID string
	ed        *composer.Model
	wires     draftWires
}

func newLineEditor(maxBytes int, placeholder string) *composer.Model {
	return composer.New(composer.Options{SingleLine: true, MaxRows: 3, MaxBytes: maxBytes, Placeholder: placeholder})
}

type inboxPlanView struct {
	requestID             string
	sha                   string
	loading               bool
	err                   string
	text                  string
	scroll, window, total int
	body                  []string
	bodyKey               inboxPlanBodyKey
	seenEnd               map[string]bool
}

type inboxPlanBodyKey struct {
	width       int
	theme, look uint64
}

// InboxApprovalDetailMsg is a get-approval reply.
type InboxApprovalDetailMsg struct {
	RequestID string
	Detail    *session.ApprovalDetail
	Err       error
}

func inboxHeld(it session.AttentionItem) bool {
	return (it.Kind == session.AttentionApproval || it.Kind == session.AttentionPlan) && it.RequestID != ""
}

func inboxRisky(it session.AttentionItem) bool {
	return len(it.Risk) > 0 && (it.Kind == session.AttentionApproval || it.Kind == session.AttentionPlan)
}

// InboxKeyPressed is told of every key the Inbox or its peek receives, before
// the key acts. A key other than the one that armed a press resets it.
func (m *OS) InboxKeyPressed(key string) {
	st := &m.Inbox.approvals
	st.lastKey = key
	if a := st.answer.armed; st.answer.step == answerArmed && key != a.press {
		m.answerEnd(answerArmed, "another key")
	}
}

func (m *OS) inboxArm(key string, it session.AttentionItem, now time.Time) bool {
	st := &m.Inbox.approvals
	shown := inboxShownKey(it)
	if a := st.answer.armed; st.answer.step == answerArmed && a.key == key && a.item == shown && now.Sub(a.at) <= inboxRiskPressWindow {
		m.answerEnd(answerArmed, "second press")
		return true
	}
	m.answerArm(inboxArmed{key: key, item: shown, at: now, press: st.lastKey})
	return false
}

func (m *OS) inboxArmedUntil() string {
	return "by " + m.Inbox.approvals.answer.armed.at.Add(inboxRiskPressWindow).Format("15:04:05")
}

func (m *OS) inboxArmedFor(it session.AttentionItem, now time.Time) (string, bool) {
	a := m.Inbox.approvals.answer.armed
	if m.Inbox.approvals.answer.step != answerArmed || a.item != inboxShownKey(it) || now.Sub(a.at) > inboxRiskPressWindow {
		return "", false
	}
	return a.key, true
}

func (m *OS) inboxApprovalGate(it session.AttentionItem, decision string) (stop bool) {
	if m.refuseRemoteAnswer("An answer") {
		return true
	}
	if decision == session.ApprovalDeny || decision == session.ApprovalAsk {
		return false
	}
	if it.Kind == session.AttentionPlan && !m.inboxPlanReadToEnd(it) {
		more := m.inboxPlanMoreBelow(it)
		msg := "Not approved. Read the plan to its end first: " + m.inboxKeyOr(config.ActionInboxLast, "G") + " goes to the end"
		if more > 0 {
			msg += ", " + strconv.Itoa(more) + " lines are left to read"
		}
		m.refuseAnswer(it, msg, "info")
		return true
	}
	return false
}

func inboxReplyExtras(it session.AttentionItem, decision string) map[string]any {
	extra := map[string]any{}
	if decision == session.ApprovalOnce || decision == session.ApprovalAlways {
		if len(it.Risk) > 0 {
			extra["risk_ack"] = slices.Clone(it.Risk)
		}
		if it.Kind == session.AttentionPlan && it.PlanSHA != "" {
			extra["plan_sha"] = it.PlanSHA
		}
	}
	return extra
}

// InboxDenyReason opens the reason line for a deny of the selected approval
// or plan (n).
func (m *OS) InboxDenyReason() (tea.Cmd, bool) {
	it, ok := m.inboxSelected()
	if !ok || !inboxHeld(it) || !slices.Contains(it.Options, session.ApprovalDeny) {
		return nil, false
	}
	if !it.DenyMessage {
		m.ShowNotification("This agent takes no reason with a reject. 3 rejects it", "warning", m.Settings.NotificationDuration)
		return nil, true
	}
	ed := newLineEditor(inboxReasonMax, "Type the reason, or press enter to reject without one.")
	if m.ProcessingRemoteKeys {
		ed.MarkAutomated()
	}
	m.answerReason(&inboxReasonLine{itemID: it.ID, requestID: it.RequestID, ed: ed})
	return nil, true
}

// InboxReasonOpen reports whether the reason line is open, where every
// printable key is text.
func (m *OS) InboxReasonOpen() bool {
	r := m.Inbox.approvals.answer.reason
	if r == nil {
		return false
	}
	it, ok := m.inboxSelected()
	if !ok || it.ID != r.itemID || it.RequestID != r.requestID {
		m.answerEnd(answerReason, "its item left the cursor")
		return false
	}
	return true
}

// InboxReasonPaste puts a paste into the reason, on one line.
func (m *OS) InboxReasonPaste(text string) {
	if r := m.Inbox.approvals.answer.reason; r != nil && text != "" {
		r.ed.Paste(text, m.ProcessingRemoteKeys)
	}
}

// InboxReasonKey gives a key to the reason line: enter denies with it, esc
// closes it, and every other key edits it.
func (m *OS) InboxReasonKey(msg tea.KeyPressMsg) tea.Cmd {
	r := m.Inbox.approvals.answer.reason
	if r == nil {
		return nil
	}
	switch r.ed.Update(msg, m.ProcessingRemoteKeys) {
	case composer.EventSend:
		return m.InboxReasonSend()
	case composer.EventClose:
		m.InboxReasonCancel()
	case composer.EventCopy, composer.EventCut:
		return m.CopyFromEditor(r.ed.Copied())
	case composer.EventPaste:
		return m.RequestHostPaste()
	}
	return nil
}

const inboxReasonMax = 500

// InboxReasonCancel closes the reason line and drops what was typed.
func (m *OS) InboxReasonCancel() {
	m.answerEnd(answerReason, "esc")
}

// InboxReasonSend denies the item with the reason. An empty reason sends the
// harness's default.
func (m *OS) InboxReasonSend() tea.Cmd {
	r := m.Inbox.approvals.answer.reason
	if r == nil || !m.InboxReasonOpen() {
		return nil
	}
	if r.ed.Automated() || m.ProcessingRemoteKeys {
		m.answerEnd(answerReason, "typed by send-keys")
		m.ShowNotification("A reason that send-keys typed is not sent: only keys from your keyboard answer an approval", "error", m.Settings.NotificationDuration)
		return nil
	}
	it, _ := m.inboxSelected()
	if !inboxShowsWhole(it) || !m.inboxAnswerSettled(it, time.Now()) {
		m.ShowNotification("This prompt just changed. Read it, then answer", "warning", m.Settings.NotificationDuration)
		return nil
	}
	text, ok := r.ed.Submit()
	if !ok {
		return nil
	}
	m.answerEnd(answerReason, "sent")
	return m.inboxReplyCmdWith(it, session.ApprovalDeny, strings.TrimSpace(text))
}

// InboxDetailScroll scrolls the detail under the list, such as a long plan,
// by delta pages: a page is the card's window less one line, so the last
// line of one page is the first of the next.
func (m *OS) InboxDetailScroll(delta int) (tea.Cmd, bool) {
	m.cockpitTouch()
	it, ok := m.inboxSelected()
	if !ok || it.Kind != session.AttentionPlan || it.RequestID == "" {
		return nil, false
	}
	pv := &m.Inbox.approvals.plan
	if pv.requestID != it.RequestID || pv.text == "" {
		return nil, true
	}
	m.inboxPlanScrollTo(pv.scroll + delta*m.inboxPlanPage())
	return nil, true
}

func (m *OS) inboxPlanPage() int {
	return max(m.inboxPlanWindow()-1, 1)
}

func (m *OS) inboxPlanScrollTo(line int) bool {
	pv := &m.Inbox.approvals.plan
	limit := line
	if pv.total > 0 {
		limit = max(pv.total-m.inboxPlanWindow(), 0)
	}
	to := clampInt(line, 0, limit)
	if to == pv.scroll {
		return false
	}
	pv.scroll = to
	m.cockpitTouch()
	return true
}

func (m *OS) inboxPlanMove(action string, lines bool) bool {
	it, ok := m.inboxSelected()
	if !ok || it.Kind != session.AttentionPlan || it.RequestID == "" {
		return false
	}
	pv := &m.Inbox.approvals.plan
	if pv.requestID != it.RequestID || pv.text == "" || pv.total == 0 {
		return false
	}
	to := pv.scroll
	switch action {
	case config.ActionInboxPageDown:
		to += m.inboxPlanPage()
	case config.ActionInboxPageUp:
		to -= m.inboxPlanPage()
	case config.ActionInboxFirst:
		to = 0
	case config.ActionInboxLast:
		to = pv.total
	case config.ActionInboxDown:
		if !lines {
			return false
		}
		to++
	case config.ActionInboxUp:
		if !lines {
			return false
		}
		to--
	default:
		return false
	}
	return m.inboxPlanScrollTo(to)
}

func (m *OS) inboxPlanSelected() bool {
	it, ok := m.inboxSelected()
	return ok && it.Kind == session.AttentionPlan && inboxHeld(it)
}

// InboxApprovalFetch reads the selected plan's text when the view does not
// hold it yet. It is called after every input and every Inbox delivery while
// the Inbox is up, and costs a comparison when there is nothing to read.
func (m *OS) InboxApprovalFetch() tea.Cmd {
	if !m.ShowInbox || m.Inbox.Peek != nil {
		return nil
	}
	it, ok := m.inboxSelected()
	if !ok || it.Kind != session.AttentionPlan || it.RequestID == "" || it.Host != "" {
		return nil
	}
	pv := &m.Inbox.approvals.plan
	if pv.requestID == it.RequestID && pv.sha == it.PlanSHA && (pv.loading || pv.text != "" || pv.err != "") {
		return nil
	}
	if m.DaemonClient == nil && m.Inbox.call == nil {
		return nil
	}
	seen := pv.seenEnd
	*pv = inboxPlanView{requestID: it.RequestID, sha: it.PlanSHA, loading: true, seenEnd: seen}
	call, id, sessName := m.inboxActCaller(), it.RequestID, it.Session
	return func() tea.Msg {
		raw, err := call("get-approval", map[string]any{"request_id": id, "session": sessName}, inboxApprovalFetchTimeout)
		if err != nil {
			return InboxApprovalDetailMsg{RequestID: id, Err: err}
		}
		var d session.ApprovalDetail
		if err := jsonx.Unmarshal(raw, &d); err != nil {
			return InboxApprovalDetailMsg{RequestID: id, Err: err}
		}
		return InboxApprovalDetailMsg{RequestID: id, Detail: &d}
	}
}

func (m *OS) applyInboxApprovalDetail(msg InboxApprovalDetailMsg) {
	m.cockpitTouch()
	pv := &m.Inbox.approvals.plan
	if pv.requestID != msg.RequestID {
		return
	}
	pv.loading = false
	switch {
	case msg.Err != nil:
		pv.err = "Could not read the plan: " + msg.Err.Error()
	case msg.Detail.PlanSHA != pv.sha:
		pv.err = "The plan changed while it was read. Move off it and back to read it again"
	default:
		pv.text = msg.Detail.Plan
	}
}

func inboxPlanKey(it session.AttentionItem) string {
	return it.RequestID + "\x00" + it.PlanSHA
}

func (m *OS) inboxPlanReadToEnd(it session.AttentionItem) bool {
	return m.Inbox.approvals.plan.seenEnd[inboxPlanKey(it)]
}

func (m *OS) inboxPlanWindow() int {
	rh := m.GetRenderHeight()
	if rh <= 0 {
		return 8
	}
	n := min(max(rh/3, 3), 16)
	if w := m.Inbox.approvals.plan.window; w > 0 {
		n = w
	}
	return n
}

func (m *OS) inboxPlanBody(width int) []string {
	pv := &m.Inbox.approvals.plan
	l := m.cockpitLook()
	key := inboxPlanBodyKey{width: width, theme: theme.Generation(), look: m.Inbox.ck.lookGen}
	if pv.body != nil && pv.bodyKey == key {
		return pv.body
	}
	bar := "│ "
	if overlay.UseASCII() {
		bar = "| "
	}
	inner := max(width-4, 8)
	src := strings.Split(strings.ReplaceAll(pv.text, "\t", "    "), "\n")
	for i, s := range src {
		src[i] = printableRunes(s)
	}
	md := m.ckMarkdown(l, strings.Join(src, "\n"), inner)
	var c, row *ckCanvas
	out := make([]string, len(md))
	for i, ln := range md {
		c = c.reset(inner, 1, l.panel)
		row = row.reset(inner+4, 1, l.panel)
		row.text(2, 0, 0, ckSeg{t: bar, fg: l.pal.FgMute})
		row.draw(4, 0, inner, ckRender(c, inner, ln))
		out[i] = row.render()
	}
	pv.body, pv.bodyKey = out, key
	return out
}

func (m *OS) inboxPlanMoreBelow(it session.AttentionItem) int {
	pv := &m.Inbox.approvals.plan
	if pv.requestID != it.RequestID || pv.text == "" {
		return 0
	}
	return max(pv.total-(pv.scroll+m.inboxPlanWindow()), 0)
}

func (m *OS) inboxPlanDetail(it session.AttentionItem, width int) []string {
	width = max(width-2, 8)
	pv := &m.Inbox.approvals.plan
	head := "Plan from " + inboxWho(it)
	if it.Harness != "" {
		head += " (" + it.Harness + ")"
	}
	var lines []string
	add := func(indent, s string) {
		for _, l := range wrapPlain(s, width-len(indent)) {
			lines = append(lines, indent+l)
		}
	}
	switch {
	case pv.requestID != it.RequestID || pv.loading:
		add("  ", inboxPlanTitle(it))
		add("  ", head+", "+strconv.Itoa(it.PlanLines)+" lines. Reading the plan...")
		return lines
	case pv.err != "":
		add("  ", inboxPlanTitle(it))
		add("  ", head+". "+pv.err+". Enter answers it in the pane.")
		return lines
	}
	body := m.inboxPlanBody(width)
	window := min(max(m.GetRenderHeight()/3, 3), 16)
	if m.Inbox.ck.zoom {
		window = len(body)
	}
	if room := m.Inbox.ck.cardRoom; room > 0 {
		fixed := len(lines) + 1 + 2
		if slices.Contains(it.Options, session.ApprovalAlways) {
			fixed += len(it.AlwaysScope)
		}
		window = max(min(window, room-fixed), 1)
	}
	pv.window, pv.total = window, len(body)
	pv.scroll = clampInt(pv.scroll, 0, max(len(body)-window, 0))
	end := min(pv.scroll+window, len(body))
	if len(body) <= window {
		head = plural.Count(len(body), "line") + ", all shown"
	} else {
		head = "Lines " + strconv.Itoa(pv.scroll+1) + "-" + strconv.Itoa(end) + " of " + strconv.Itoa(len(body))
	}
	add("  ", head)
	lines = append(lines, body[pv.scroll:end]...)
	if slices.Contains(it.Options, session.ApprovalAlways) {
		for _, rule := range it.AlwaysScope {
			add("  ", m.inboxKeyOr(config.ActionInboxAllowAlways, "A")+" also sets: "+printableTitle(rule))
		}
	}
	if more := len(body) - end; more > 0 {
		add("  ", plural.Count(more, "more line")+". Read to the end to approve: "+m.inboxKeyOr(config.ActionInboxDetailDown, "J")+" "+m.inboxKeyOr(config.ActionInboxDetailUp, "K")+" page, "+m.inboxKeyOr(config.ActionInboxLast, "G")+" end, "+m.inboxKeyOr(config.ActionInboxZoom, "Z")+" zoom.")
		return append(lines, m.inboxRefusalLines(it, width-2)...)
	}
	if !m.inboxDetailFits(len(lines)) {
		add("  ", "The screen is too short to show the whole plan. Enter answers it in the pane.")
		return lines
	}
	if pv.seenEnd == nil {
		pv.seenEnd = make(map[string]bool)
	}
	pv.seenEnd[inboxPlanKey(it)] = true
	return append(lines, m.inboxRefusalLines(it, width-2)...)
}

func (m *OS) inboxDetailFits(n int) bool {
	if m.ShowInbox {
		return n <= m.Inbox.ck.cardRoom
	}
	rh := m.GetRenderHeight()
	return rh > 0 && panelChromeRows+1+n+1+minPanelRows <= rh
}

func inboxRiskLines(it session.AttentionItem, width int) []string {
	var lines []string
	for _, name := range it.Risk {
		for _, l := range wrapPlain("Risky: "+printableTitle(name)+", "+inboxRiskWhy(name), width) {
			lines = append(lines, "  "+l)
		}
	}
	return lines
}

func inboxRiskWhy(name string) string {
	if why, ok := risk.Why(name); ok {
		return why
	}
	return "a rule in your config"
}

func inboxDecisionKey(decision string) string {
	for _, a := range inboxAnswerOrder {
		if a.decision == decision {
			return a.key
		}
	}
	return decision
}

func inboxPlanTitle(it session.AttentionItem) string {
	title := strings.TrimPrefix(printableTitle(it.Summary), "plan: ")
	title = strings.TrimSpace(strings.TrimLeft(title, "#"))
	if title == "" {
		title = "a plan"
	}
	return title
}

func inboxPlanRowText(it session.AttentionItem) string {
	title := inboxPlanTitle(it)
	if it.PlanLines > 0 {
		title += " (" + strconv.Itoa(it.PlanLines) + " lines)"
	}
	return title
}

func (m *OS) inboxDetailExtra(it session.AttentionItem) (detail func(width int) []string, ok bool) {
	if m.InboxReasonOpen() {
		r := m.Inbox.approvals.answer.reason
		return func(width int) []string {
			pal := m.cockpitPalette()
			bg := pal.Surface
			st := overlay.Style(bg)
			var lines []string
			for _, l := range wrapPlain(printableTitle(it.Summary), max(width-4, 8)) {
				lines = append(lines, overlay.Fill(st.Foreground(pal.Fg).Render("  "+l), width, bg))
			}
			lines = append(lines, overlay.Fill(st.Foreground(overlay.Readable(pal.FgDim, bg)).Render("  Reason:"), width, bg))
			for _, l := range r.ed.Render(max(width-2, 8), composerStyles(pal), bg) {
				lines = append(lines, st.Render("  ")+l)
			}
			return lines
		}, true
	}
	held := inboxHeld(it)
	switch {
	case it.Kind == session.AttentionPlan && held:
		return func(width int) []string { return m.inboxPlanDetail(it, width) }, true
	case it.Kind == session.AttentionApproval && held && (inboxRisky(it) || it.DenyMessage):
		now := time.Now()
		return func(width int) []string {
			lines := inboxApprovalDetail(it, width)
			if !inboxShowsWhole(it) {
				return lines
			}
			lines = append(lines, inboxRiskLines(it, max(width-2, 8))...)
			lines = append(lines, m.inboxRefusalLines(it, max(width-2, 8))...)
			if line, ok := m.inboxConfirmLine(it); ok {
				for _, l := range wrapPlain(line, max(width-2, 8)) {
					lines = append(lines, "  "+l)
				}
			}
			if key, armed := m.inboxArmedFor(it, now); armed {
				press := m.Inbox.approvals.answer.armed.press
				if press == "" {
					press = inboxDecisionKey(key)
				}
				for _, l := range wrapPlain("Press "+press+" again "+m.inboxArmedUntil()+" to allow "+printableTitle(inboxRiskTarget(it)), max(width-2, 8)) {
					lines = append(lines, "  "+l)
				}
			}
			return lines
		}, true
	case it.Kind == session.AttentionApproval && inboxRisky(it):
		return func(width int) []string {
			lines := wrapPlain("  "+printableTitle(it.Summary), max(width-2, 8))
			return append(lines, inboxRiskLines(it, max(width-2, 8))...)
		}, true
	}
	return nil, false
}

func inboxRiskTarget(it session.AttentionItem) string {
	if _, text := risk.ParseSummary(it.Summary); text != "" {
		return text
	}
	return it.Summary
}

func inboxPeekApproves(action string) bool {
	switch action {
	case harness.ActionApprove, harness.ActionApproveAlways, harness.ActionChoose:
		return true
	}
	return false
}

func (m *OS) inboxPeekRiskGate(p *inboxPeek, action, value string, confirmed bool) ([]string, bool) {
	cur, ok := m.inboxItem(p.Item.ID)
	if !ok {
		cur = p.Item
	}
	if !inboxRisky(cur) || action == harness.ActionDeny {
		return nil, true
	}
	if action == harness.ActionText {
		return slices.Clone(cur.Risk), true
	}
	if !inboxPeekApproves(action) {
		return nil, true
	}
	if confirmed {
		return slices.Clone(cur.Risk), true
	}
	key := "peek:" + action
	if action == harness.ActionChoose {
		key += ":" + value
	}
	if !m.inboxArm(key, cur, time.Now()) {
		press := m.Inbox.approvals.answer.armed.press
		if press == "" {
			press = "it"
		}
		p.Note = "Risky: " + strings.Join(cur.Risk, ", ") + ". Press " + press + " again " + m.inboxArmedUntil() + " to allow " + printableTitle(inboxRiskTarget(cur)) + "."
		return nil, false
	}
	return slices.Clone(cur.Risk), true
}

func (m *OS) paneApprovalWord(windowID, kind string) string {
	if len(m.Inbox.Items) == 0 || windowID == "" {
		return kind
	}
	for _, it := range m.Inbox.Items {
		if it.Window != windowID || it.Host != "" {
			continue
		}
		switch {
		case it.Kind == session.AttentionPlan:
			return inboxWordPlan
		case it.Kind == session.AttentionApproval && len(it.Risk) > 0:
			return inboxWordRisky
		}
	}
	return kind
}

const (
	inboxWordPlan  = "plan"
	inboxWordRisky = "risky"
)
