package app

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/theme"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Safer approvals in the Inbox: a risky approval that needs a second press to
// allow, a deny that carries a reason, and a plan shown whole with its own
// group and keys. The daemon enforces the second press and the plan digest
// (risk_ack and plan_sha, see session/approval_risk.go); this is the side the
// person sees.
//
//   - An approval whose command matched a risk rule reads "risky:" on its row
//     and names each rule, and why, in the detail. 1 and 2 allow it only on a
//     second press of the same key within inboxRiskPressWindow; the first
//     press says so in the detail. Any other key resets it. 3 denies at once.
//     The peek holds its approving keys to the same rule.
//   - n on a held approval or plan whose harness takes a reason opens a
//     "Reason:" line; enter denies with what was typed.
//   - A plan is listed under Plans and shown whole in the detail, scrolled
//     with J and K. 1 and 2 approve it only once its last line has been on
//     screen; 3 and n keep it planning.

// inboxRiskPressWindow is how long the first press of an allow on a risky
// item waits for the second.
const inboxRiskPressWindow = 3 * time.Second

// inboxApprovalFetchTimeout bounds the get-approval call.
const inboxApprovalFetchTimeout = 5 * time.Second

// inboxApprovalState is the Inbox's state for the safer approvals.
type inboxApprovalState struct {
	// armed is the first press of an allow on a risky item, waiting for the
	// second.
	armed inboxArmed
	// lastKey is the key the Inbox or its peek received last, as the
	// keyboard spells it.
	lastKey string
	// reason is the open "Reason:" line, nil when none is open.
	reason *inboxReasonLine
	// plan is the plan shown in the detail.
	plan inboxPlanView
}

// inboxArmed is one press waiting for its second.
type inboxArmed struct {
	// key names the press: the decision for a list key, or the peek's action
	// and value.
	key string
	// item is the item as it was drawn (inboxShownKey): a second press on an
	// item that changed is a first press again.
	item string
	at   time.Time
	// press is the key that armed it. Any other key resets it.
	press string
}

// inboxReasonLine is the reason typed for a deny.
type inboxReasonLine struct {
	itemID    string
	requestID string
	// ed is the line, a one-line composer. It records a key that did not
	// come from the keyboard, and such a draft is never sent.
	ed *composer.Model
}

// newLineEditor is a one-line composer of at most maxBytes, for an answer
// typed into a prompt: a deny reason, a peek answer, a selector.
func newLineEditor(maxBytes int, placeholder string) *composer.Model {
	return composer.New(composer.Options{SingleLine: true, MaxRows: 3, MaxBytes: maxBytes, Placeholder: placeholder})
}

// inboxPlanView is the plan get-approval served for the selected plan item.
type inboxPlanView struct {
	// requestID and sha name the plan the view holds or is fetching.
	requestID string
	sha       string
	loading   bool
	err       string
	text      string
	// scroll is the first line shown, and window how many the card showed
	// last, 0 before it was drawn.
	scroll, window int
	// seenEnd holds the plans, by request id and digest, whose last line has
	// been on screen. 1 and 2 approve only such a plan.
	seenEnd map[string]bool
}

// InboxApprovalDetailMsg is a get-approval reply.
type InboxApprovalDetailMsg struct {
	RequestID string
	Detail    *session.ApprovalDetail
	Err       error
}

// inboxHeld reports whether an item is an approval or plan the Inbox holds.
func inboxHeld(it session.AttentionItem) bool {
	return (it.Kind == session.AttentionApproval || it.Kind == session.AttentionPlan) && it.RequestID != ""
}

// inboxRisky reports whether an item is an approval a risk rule matched.
func inboxRisky(it session.AttentionItem) bool {
	return len(it.Risk) > 0 && (it.Kind == session.AttentionApproval || it.Kind == session.AttentionPlan)
}

// InboxKeyPressed is told of every key the Inbox or its peek receives, before
// the key acts. A key other than the one that armed a press resets it.
func (m *OS) InboxKeyPressed(key string) {
	st := &m.Inbox.approvals
	st.lastKey = key
	if st.armed.key != "" && key != st.armed.press {
		st.armed = inboxArmed{}
	}
}

// inboxArm records the first press of key on it, or reports the second. It
// returns true when this press is the second one within the window, on the
// same item as it was drawn.
func (m *OS) inboxArm(key string, it session.AttentionItem, now time.Time) bool {
	st := &m.Inbox.approvals
	shown := inboxShownKey(it)
	if st.armed.key == key && st.armed.item == shown && now.Sub(st.armed.at) <= inboxRiskPressWindow {
		st.armed = inboxArmed{}
		return true
	}
	st.armed = inboxArmed{key: key, item: shown, at: now, press: st.lastKey}
	return false
}

// inboxArmedUntil says when the armed press lapses, as a clock time: nothing
// redraws the line when it does, so it names the time rather than implying
// the press is still waiting.
func (m *OS) inboxArmedUntil() string {
	return "by " + m.Inbox.approvals.armed.at.Add(inboxRiskPressWindow).Format("15:04:05")
}

// inboxArmedFor reports whether a press on it is waiting for its second, and
// which key it was.
func (m *OS) inboxArmedFor(it session.AttentionItem, now time.Time) (string, bool) {
	a := m.Inbox.approvals.armed
	if a.key == "" || a.item != inboxShownKey(it) || now.Sub(a.at) > inboxRiskPressWindow {
		return "", false
	}
	return a.key, true
}

// inboxApprovalGate runs the safer-approval checks on an answer from the list
// keys. It returns stop true when the answer must not be sent now: a first
// press of an allow on a risky item, or an allow of a plan whose end has not
// been on screen.
func (m *OS) inboxApprovalGate(it session.AttentionItem, decision string) (stop bool) {
	// A key send-keys typed neither answers nor arms a second press.
	if m.refuseRemoteAnswer("An answer") {
		return true
	}
	if decision == session.ApprovalDeny || decision == session.ApprovalAsk {
		return false
	}
	if it.Kind == session.AttentionPlan && !m.inboxPlanReadToEnd(it) {
		more := m.inboxPlanMoreBelow(it)
		msg := "Read the plan to its end before approving it: " + m.inboxKeyOr(config.ActionInboxDetailDown, "J") + " scrolls"
		if more > 0 {
			msg += ", " + strconv.Itoa(more) + " more below"
		}
		m.ShowNotification(msg, "info", m.Settings.NotificationDuration)
		return true
	}
	if inboxRisky(it) && !m.inboxArm(decision, it, time.Now()) {
		// The detail says what the second press does. Nothing is sent.
		return true
	}
	return false
}

// inboxReplyExtras are what an answer to it carries beyond the decision: the
// rules the person saw for an allow of a risky call, and the digest of the plan
// they read for an allow of a plan.
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
		m.ShowNotification("This agent takes no reason with a reject. 3 rejects it", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	m.Inbox.approvals.armed = inboxArmed{}
	ed := newLineEditor(inboxReasonMax, "Type the reason, or press enter to reject without one.")
	if m.ProcessingRemoteKeys {
		ed.MarkAutomated()
	}
	m.Inbox.approvals.reason = &inboxReasonLine{itemID: it.ID, requestID: it.RequestID, ed: ed}
	return nil, true
}

// InboxReasonOpen reports whether the reason line is open, where every
// printable key is text.
func (m *OS) InboxReasonOpen() bool {
	r := m.Inbox.approvals.reason
	if r == nil {
		return false
	}
	// The line belongs to the item it was opened on. If that item is gone
	// or no longer under the cursor, the line is gone with it.
	it, ok := m.inboxSelected()
	if !ok || it.ID != r.itemID || it.RequestID != r.requestID {
		m.Inbox.approvals.reason = nil
		return false
	}
	return true
}

// InboxReasonType types text into the reason at the cursor.
func (m *OS) InboxReasonType(text string) {
	if r := m.Inbox.approvals.reason; r != nil && text != "" {
		r.ed.Insert(text, m.ProcessingRemoteKeys)
	}
}

// InboxReasonPaste puts a paste into the reason, on one line.
func (m *OS) InboxReasonPaste(text string) {
	if r := m.Inbox.approvals.reason; r != nil && text != "" {
		r.ed.Paste(text, m.ProcessingRemoteKeys)
	}
}

// InboxReasonKey gives a key to the reason line: enter denies with it, esc
// closes it, and every other key edits it.
func (m *OS) InboxReasonKey(msg tea.KeyPressMsg) tea.Cmd {
	r := m.Inbox.approvals.reason
	if r == nil {
		return nil
	}
	switch r.ed.Update(msg, m.ProcessingRemoteKeys) {
	case composer.EventSend:
		return m.InboxReasonSend()
	case composer.EventClose:
		m.InboxReasonCancel()
	case composer.EventPaste:
		return m.RequestHostPaste()
	}
	return nil
}

// inboxReasonMax bounds the reason, the bound the daemon applies too.
const inboxReasonMax = 500

// InboxReasonCancel closes the reason line and drops what was typed.
func (m *OS) InboxReasonCancel() {
	m.Inbox.approvals.reason = nil
}

// InboxReasonSend denies the item with the reason. An empty reason sends the
// harness's default.
func (m *OS) InboxReasonSend() tea.Cmd {
	r := m.Inbox.approvals.reason
	if r == nil || !m.InboxReasonOpen() {
		return nil
	}
	if r.ed.Automated() || m.ProcessingRemoteKeys {
		m.Inbox.approvals.reason = nil
		m.ShowNotification("A reason that send-keys typed is not sent: only keys from your keyboard answer an approval", "error", m.Settings.NotificationDuration)
		return nil
	}
	it, _ := m.inboxSelected()
	if !inboxShowsWhole(it) || !m.inboxAnswerSettled(it, time.Now()) {
		m.ShowNotification("This prompt just changed. Read it, then answer", "info", m.Settings.NotificationDuration)
		return nil
	}
	text, ok := r.ed.Submit()
	if !ok {
		// Invisible characters were removed; the line says so.
		return nil
	}
	m.Inbox.approvals.reason = nil
	return m.inboxReplyCmdWith(it, session.ApprovalDeny, strings.TrimSpace(text))
}

// InboxDetailScroll scrolls the detail under the list, such as a long plan,
// by delta lines.
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
	pv.scroll = max(pv.scroll+delta, 0)
	return nil, true
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
	call, id, sessName := m.inboxCaller(), it.RequestID, it.Session
	return func() tea.Msg {
		raw, err := call("get-approval", map[string]any{"request_id": id, "session": sessName}, inboxApprovalFetchTimeout)
		if err != nil {
			return InboxApprovalDetailMsg{RequestID: id, Err: err}
		}
		var d session.ApprovalDetail
		if err := json.Unmarshal(raw, &d); err != nil {
			return InboxApprovalDetailMsg{RequestID: id, Err: err}
		}
		return InboxApprovalDetailMsg{RequestID: id, Detail: &d}
	}
}

// applyInboxApprovalDetail takes a get-approval reply for the plan view. A
// plan whose digest is not the one on the item is not shown: the item will be
// updated, and read again then.
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

// inboxPlanKey names one plan for seenEnd.
func inboxPlanKey(it session.AttentionItem) string {
	return it.RequestID + "\x00" + it.PlanSHA
}

// inboxPlanReadToEnd reports whether the plan's last line has been on screen.
func (m *OS) inboxPlanReadToEnd(it session.AttentionItem) bool {
	return m.Inbox.approvals.plan.seenEnd[inboxPlanKey(it)]
}

// inboxPlanWindow is how many plan lines the detail shows at once: a third of
// the screen, 8 on a 24 row terminal.
func (m *OS) inboxPlanWindow() int {
	rh := m.GetRenderHeight()
	if rh <= 0 {
		return 8
	}
	n := min(max(rh/3, 3), 16)
	if w := m.Inbox.approvals.plan.window; w > 0 {
		// What the card showed: the scroll keys move by its window.
		n = w
	}
	return n
}

// inboxPlanLines is the plan's text wrapped to width, each line as drawn.
func inboxPlanLines(text string, width int) []string {
	var out []string
	for l := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		l = strings.ReplaceAll(l, "\t", "    ")
		out = append(out, wrapPlain(printableRunes(l), width)...)
	}
	return out
}

// inboxPlanMoreBelow is how many plan lines are below the window.
func (m *OS) inboxPlanMoreBelow(it session.AttentionItem) int {
	pv := &m.Inbox.approvals.plan
	if pv.requestID != it.RequestID || pv.text == "" {
		return 0
	}
	lines := inboxPlanLines(pv.text, max(m.panelWidth(inboxWidth)-6, 8))
	return max(len(lines)-(pv.scroll+m.inboxPlanWindow()), 0)
}

// inboxPlanDetail is the plan under the cursor: who asks, the plan's lines
// behind a bar that marks them as the agent's text, what 2 also sets, and how
// much is left to read. Drawing the window that holds the last line records
// the plan as read to its end, when the panel fits the screen whole.
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
	// The plan's title and length first: the card's title only asks.
	add("  ", inboxPlanRowText(it))
	switch {
	case pv.requestID != it.RequestID || pv.loading:
		add("  ", head+", "+strconv.Itoa(it.PlanLines)+" lines. Reading the plan...")
		return lines
	case pv.err != "":
		add("  ", head+". "+pv.err+". Enter answers it in the pane.")
		return lines
	}
	bar := "│ "
	if overlay.UseASCII() {
		bar = "| "
	}
	body := inboxPlanLines(pv.text, max(width-4, 8))
	window := min(max(m.GetRenderHeight()/3, 3), 16)
	if room := m.Inbox.ck.cardRoom; room > 0 {
		// The card holds the title, the head, the window, what always
		// sets and the line under it, which can wrap once.
		fixed := len(lines) + 1 + 2
		if slices.Contains(it.Options, session.ApprovalAlways) {
			fixed += len(it.AlwaysScope)
		}
		window = max(min(window, room-fixed), 1)
	}
	pv.window = window
	pv.scroll = clampInt(pv.scroll, 0, max(len(body)-window, 0))
	end := min(pv.scroll+window, len(body))
	if len(body) <= window {
		head += ", " + strconv.Itoa(len(body)) + " lines, all shown"
	} else {
		head += ", lines " + strconv.Itoa(pv.scroll+1) + "-" + strconv.Itoa(end) + " of " + strconv.Itoa(len(body))
	}
	add("  ", head)
	for _, l := range body[pv.scroll:end] {
		lines = append(lines, "  "+bar+l)
	}
	if slices.Contains(it.Options, session.ApprovalAlways) {
		for _, rule := range it.AlwaysScope {
			add("  ", "2 also sets: "+printableTitle(rule))
		}
	}
	if more := len(body) - end; more > 0 {
		add("  ", m.inboxKeyOr(config.ActionInboxDetailDown, "J")+"/"+m.inboxKeyOr(config.ActionInboxDetailUp, "K")+" scroll, "+strconv.Itoa(more)+" more below. 1 approves once the end has been shown.")
		return lines
	}
	// The window holds the last line. It has been read only if the panel
	// fits the screen: on one too short, the panel is squeezed past its
	// minimum and the bottom of the detail is cut off.
	if !m.inboxDetailFits(len(lines)) {
		add("  ", "The screen is too short to show the whole plan. Enter answers it in the pane.")
		return lines
	}
	if pv.seenEnd == nil {
		pv.seenEnd = make(map[string]bool)
	}
	pv.seenEnd[inboxPlanKey(it)] = true
	return lines
}

// inboxDetailFits reports whether the Inbox panel, with n detail lines under
// the list, fits the screen whole: the panel's chrome, the scroll line, the
// rule and lines of the detail, and the fewest rows the list is squeezed to.
// The key hints are not counted, since the panel drops them before anything
// else. An unknown screen size is read as not fitting.
func (m *OS) inboxDetailFits(n int) bool {
	if m.ShowInbox {
		// The cockpit's card says how many detail lines it shows whole.
		return n <= m.Inbox.ck.cardRoom
	}
	rh := m.GetRenderHeight()
	return rh > 0 && panelChromeRows+1+n+1+minPanelRows <= rh
}

// inboxRiskLines names each rule a risky item matched and why.
func inboxRiskLines(it session.AttentionItem, width int) []string {
	var lines []string
	for _, name := range it.Risk {
		for _, l := range wrapPlain("Risky: "+printableTitle(name)+", "+inboxRiskWhy(name), width) {
			lines = append(lines, "  "+l)
		}
	}
	return lines
}

// inboxRiskWhy is what a rule guards against, for a shipped rule, and where
// it came from for one of the person's.
func inboxRiskWhy(name string) string {
	if why, ok := risk.Why(name); ok {
		return why
	}
	return "a rule in your config"
}

// inboxDecisionKey is the list key that sends decision.
func inboxDecisionKey(decision string) string {
	for _, a := range inboxAnswerOrder {
		if a.decision == decision {
			return a.key
		}
	}
	return decision
}

// inboxPlanRowText is a plan's row: its title and how long it is.
func inboxPlanRowText(it session.AttentionItem) string {
	title := strings.TrimPrefix(printableTitle(it.Summary), "plan: ")
	if title == "" {
		title = "a plan"
	}
	if it.PlanLines > 0 {
		title += " (" + strconv.Itoa(it.PlanLines) + " lines)"
	}
	return title
}

// inboxDetailExtra is the card's detail for an item the safer approvals
// draw: a plan, a risky approval, an approval that takes a reason, and the
// reason line itself. ok is false for every other item.
func (m *OS) inboxDetailExtra(it session.AttentionItem) (detail func(width int) []string, ok bool) {
	if m.InboxReasonOpen() {
		r := m.Inbox.approvals.reason
		return func(width int) []string {
			pal := theme.UI()
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
			if key, armed := m.inboxArmedFor(it, now); armed {
				press := m.Inbox.approvals.armed.press
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
		// An approval nobody holds: the rules say what the peek's second
		// press is about.
		return func(width int) []string {
			lines := wrapPlain("  "+printableTitle(it.Summary), max(width-2, 8))
			return append(lines, inboxRiskLines(it, max(width-2, 8))...)
		}, true
	}
	return nil, false
}

// inboxRiskTarget is what an allow of a risky item allows, as its line says
// it: the command after "approve Tool: ".
func inboxRiskTarget(it session.AttentionItem) string {
	if _, text := risk.ParseSummary(it.Summary); text != "" {
		return text
	}
	return it.Summary
}

// inboxPeekApproves reports whether a peek answer allows the call: approve,
// always, and a digit, which the peek cannot tell from an allow. d denies,
// and typed text is a whole answer typed on purpose.
func inboxPeekApproves(action string) bool {
	switch action {
	case harness.ActionApprove, harness.ActionApproveAlways, harness.ActionChoose:
		return true
	}
	return false
}

// inboxPeekRiskGate is the peek's second press on a risky prompt. It returns
// the rules to acknowledge and whether the answer may be sent now.
func (m *OS) inboxPeekRiskGate(p *inboxPeek, action, value string) ([]string, bool) {
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
	key := "peek:" + action
	if action == harness.ActionChoose {
		key += ":" + value
	}
	if !m.inboxArm(key, cur, time.Now()) {
		press := m.Inbox.approvals.armed.press
		if press == "" {
			press = "it"
		}
		p.Note = "Risky: " + strings.Join(cur.Risk, ", ") + ". Press " + press + " again " + m.inboxArmedUntil() + " to allow " + printableTitle(inboxRiskTarget(cur)) + "."
		return nil, false
	}
	return slices.Clone(cur.Risk), true
}

// paneApprovalWord is the need word for a pane blocked on an approval the
// Inbox marked: plan for a plan, risky for a call a risk rule matched, and
// kind otherwise.
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

// The need words the safer approvals add to approval and question.
const (
	inboxWordPlan  = "plan"
	inboxWordRisky = "risky"
)
