package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
)

// Approvals answered from the Inbox.
//
// A harness with a structured decision channel, Claude Code's PermissionRequest
// hook or opencode's permission reply, can be told the answer to its permission
// prompt by the hook instead of asking in its pane. With [agents.approvals]
// naming the harness, `tuios agent-hook` does that: after reporting the pane as
// needs_input it calls request-approval, and the call does not answer until the
// person answers the Inbox item with reply-approval, or the hold ends. The hook
// then prints the harness's own decision, or nothing, and a harness that gets
// nothing shows its own prompt as it always did. That fallback is the whole
// safety story, so every way a hold can end without an answer ends it with no
// decision:
//
//   - the hold times out ([agents.approvals] hold_seconds, 120 by default);
//   - the pane leaves needs_input, closes, or its block turns into a question;
//   - the person dismisses the item, presses enter on it, or focuses the pane;
//   - the hook goes away, which is how a harness that gave up on it shows;
//   - a second request for the same pane arrives;
//   - the daemon shuts down.
//
// Who may do what:
//
//   - Only reply-approval can put a decision in a hold, and it takes the same
//     proof dismiss-attention does: the nonce of a client attached right now,
//     from a process that is not inside a pane of this daemon (human_sender.go
//     and human_origin.go). An agent cannot answer its own prompt or another
//     agent's, and no mail, ask or send-keys reaches a hold.
//   - request-approval is refused over a link, and a caller inside a pane may
//     only hold the prompt of the pane it runs in. All it gets back is what the
//     person answered about the item it opened, which says nothing it did not
//     ask.
//   - A hold is only opened on a pane already on needs_input with kind
//     approval, which the hook's own report establishes, so a stray call
//     cannot put an approval in the Inbox for a pane that is not blocked.
//
// The holds live in the attention store under its lock, beside the items they
// belong to, so an item and its hold cannot disagree: closing the item ends the
// hold, and ending the hold clears the item's request.

// Approval decisions. They are wire values: the decision reply-approval takes
// and request-approval returns.
const (
	// ApprovalOnce allows this one call.
	ApprovalOnce = "once"
	// ApprovalAlways allows it and tells the harness to stop asking for calls
	// like it, in whatever way the harness does that.
	ApprovalAlways = "always"
	// ApprovalDeny refuses the call.
	ApprovalDeny = "deny"
	// ApprovalAsk is not a decision: it ends the hold with none, so the
	// harness asks in its pane. It is what going to the pane means.
	ApprovalAsk = "ask"
)

// approvalDecisions are the decisions a hold can end with.
var approvalDecisions = []string{ApprovalOnce, ApprovalAlways, ApprovalDeny}

// approvalReplies are what reply-approval takes.
var approvalReplies = []string{ApprovalOnce, ApprovalAlways, ApprovalDeny, ApprovalAsk}

// Reasons a hold ended, as request-approval reports them. The attention close
// reasons (resolved, dismissed, window_closed, session_closed, evicted) are
// reported as they are.
const (
	approvalEndAnswered   = AttentionClosedAnswered
	approvalEndTimeout    = "timeout"
	approvalEndDisabled   = "disabled"
	approvalEndNotBlocked = "not_blocked"
	approvalEndSuperseded = "superseded"
	approvalEndHandedBack = "handed_back"
	approvalEndViewed     = "viewed"
	approvalEndCallerGone = "caller_gone"
	approvalEndShutdown   = "shutdown"
	// approvalEndNotShown is a hold refused because its line would not show
	// the whole request: the daemon would have to cut, mask or rewrite it.
	approvalEndNotShown = "not_shown"
	// approvalEndChanged is not an end: it is reply-approval's reason for a
	// decision refused because the held line is not the one it was made from.
	// The hold keeps running.
	approvalEndChanged  = "changed"
	approvalEndResolved = AttentionClosedResolved
)

// DefaultApprovalHold is how long a hold lasts when the config names none.
const DefaultApprovalHold = 120 * time.Second

// minApprovalHold and maxApprovalHold bound hold_seconds. The top is below the
// 310 second timeout the Claude Code integration installs for its
// PermissionRequest hook, so the daemon always ends a hold before the harness
// kills the hook. They are variables so a test can hold for milliseconds.
var (
	minApprovalHold = 10 * time.Second
	maxApprovalHold = 300 * time.Second
)

// approvalSettledMax bounds how many ended holds are remembered for a late or
// repeated reply.
const approvalSettledMax = 256

// approvalMaxMessage bounds a deny message, which goes to the model.
const approvalMaxMessage = 500

// ApprovalPolicy is the [agents.approvals] table as the daemon uses it.
type ApprovalPolicy struct {
	// Enabled holds the harness ids whose approvals may be held, canonical.
	Enabled map[string]bool
	// Hold is how long a hold lasts, already bounded.
	Hold time.Duration
	// Plans says a plan may be held too (hold_plans), for the same harnesses.
	Plans bool
	// Risk are the rules that mark an approval risky: the shipped ones and
	// the person's, from [agents.approvals.risk]. See internal/risk.
	Risk []risk.Rule
	// PanesMayAllow lets a pane with the respond grant allow a risky prompt.
	PanesMayAllow bool
}

// ApprovalPolicyFromConfig reads the table. Harness names are resolved to the
// ids hooks report under, so claude and claude-code are the same harness.
func ApprovalPolicyFromConfig(c config.ApprovalsConfig) ApprovalPolicy {
	p := ApprovalPolicy{
		Hold:          time.Duration(c.HoldSeconds) * time.Second,
		Plans:         c.PlansHeld(),
		Risk:          risk.FromConfig(c.Risk),
		PanesMayAllow: c.Risk.PanesMayAllow,
	}
	for _, name := range c.Enabled {
		id := canonicalHarness(name)
		if id == "" {
			continue
		}
		if p.Enabled == nil {
			p.Enabled = make(map[string]bool)
		}
		p.Enabled[id] = true
	}
	return p
}

// canonicalHarness resolves a harness name to the id hooks report under, or
// lower-cases a name nothing knows.
func canonicalHarness(name string) string {
	if id, ok := integration.Canonical(name); ok {
		return id
	}
	return strings.ToLower(strings.TrimSpace(name))
}

// holdFor is the bounded hold length.
func (p ApprovalPolicy) holdFor() time.Duration {
	switch {
	case p.Hold <= 0:
		return DefaultApprovalHold
	case p.Hold < minApprovalHold:
		return minApprovalHold
	case p.Hold > maxApprovalHold:
		return maxApprovalHold
	}
	return p.Hold
}

// SetApprovalPolicy replaces the approval policy. Holds already running keep
// the length they started with.
func (d *Daemon) SetApprovalPolicy(p ApprovalPolicy) {
	d.approvals.Store(&p)
	if d.attention != nil {
		d.attention.setRisk(p.Risk)
	}
}

// approvalPolicy is the current policy, never nil.
func (d *Daemon) approvalPolicy() ApprovalPolicy {
	if p := d.approvals.Load(); p != nil {
		return *p
	}
	return ApprovalPolicy{}
}

// approvalOutcome is how a hold ended: a decision, or none with the reason.
type approvalOutcome struct {
	Decision string
	Message  string
	Reason   string
	By       string
}

// approvalHold is one hook waiting for the person.
type approvalHold struct {
	id      string
	itemID  string
	session string
	window  string
	options []string
	// summary is the line the person answers from, which the item shows for
	// as long as the hold runs. A reply that names another line is refused.
	summary string
	// latest is the newest message reported for the pane while the hold
	// ran, shown on the item again once the hold ends. Empty when none came.
	latest string
	// kind is AttentionApproval or AttentionPlan.
	kind string
	// tool and target are the call the risk rules read, as the hook named
	// it. Empty when the hook did not.
	tool, target string
	// root is the pane's worktree root, else its working directory, when
	// the hold began: where the outside-the-worktree rule measures from.
	root string
	// risk are the rules the call matched. An allow needs risk_ack naming
	// exactly these.
	risk []risk.Hit
	// plan and planSHA are a plan's text and its digest. An allow of a plan
	// must name the digest of the plan it was made from.
	plan, planSHA string
	// denyMessage says the harness takes a reason with a deny.
	denyMessage bool
	// done receives the outcome exactly once. It is buffered, so whatever
	// ends the hold never waits on the hook.
	done chan approvalOutcome
}

// newApprovalID returns a fresh request id.
func newApprovalID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	}
	return hex.EncodeToString(b[:])
}

// holdSpec is what a hold is opened with: the line and answers the item
// shows, and what the risk rules, a plan and a deny reason need.
type holdSpec struct {
	session, window string
	// kind is AttentionApproval or AttentionPlan.
	kind    string
	summary string
	options []string
	scope   []string
	expires time.Time
	// tool, target and root are the call as the risk rules read it.
	tool, target, root string
	// risk are the rules the call matched, computed before the hold opens.
	risk []risk.Hit
	// plan is a plan's text.
	plan        string
	denyMessage bool
}

// startHold opens a hold on a pane's approval item. It returns the reason
// when there is nothing to hold: the pane has no open approval. A hold already
// on the item ends as superseded, since one pane shows one prompt at a time.
// The item shows summary, the held call's own line, and scope beside always,
// until the hold ends. A plan turns the item into a plan item for as long as
// the hold runs.
func (a *attentionStore) startHold(spec holdSpec) (*approvalHold, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// A hold on a snoozed approval is news the person can act on from the
	// Inbox now, so the approval wakes.
	if sid, ok := a.snoozedKey[attentionKey(AttentionApproval, spec.session, spec.window, 0)]; ok && a.snoozed[sid].Kind == AttentionApproval {
		a.wakeLocked(sid)
	}
	id, ok := a.byKey[attentionKey(AttentionApproval, spec.session, spec.window, 0)]
	if !ok || (a.items[id].Kind != AttentionApproval && a.items[id].Kind != AttentionPlan) {
		return nil, approvalEndNotBlocked
	}
	it := a.items[id]
	if it.RequestID != "" {
		a.endHoldLocked(it.RequestID, approvalOutcome{Reason: approvalEndSuperseded}, false)
	}
	if a.holds == nil {
		a.holds = make(map[string]*approvalHold)
	}
	kind := spec.kind
	if kind != AttentionPlan {
		kind = AttentionApproval
	}
	h := &approvalHold{
		id:          newApprovalID(),
		itemID:      id,
		session:     spec.session,
		window:      spec.window,
		options:     slices.Clone(spec.options),
		summary:     spec.summary,
		kind:        kind,
		tool:        spec.tool,
		target:      spec.target,
		root:        spec.root,
		risk:        slices.Clone(spec.risk),
		denyMessage: spec.denyMessage,
		done:        make(chan approvalOutcome, 1),
	}
	if kind == AttentionPlan {
		h.plan, h.planSHA = spec.plan, planDigest(spec.plan)
	}
	scope := spec.scope
	if !slices.Contains(spec.options, ApprovalAlways) {
		scope = nil
	}
	a.holds[h.id] = h
	a.rev++
	it.RequestID, it.Options, it.Expires = h.id, h.options, spec.expires.UnixNano()
	it.Summary, it.AlwaysScope = spec.summary, slices.Clone(scope)
	it.Kind, it.Risk, it.DenyMessage = kind, risk.Names(h.risk), h.denyMessage
	it.PlanLines, it.PlanSHA = 0, h.planSHA
	if kind == AttentionPlan {
		it.PlanLines = planLineCount(h.plan)
	}
	it.Seq = a.rev
	a.publish(attentionEvent(AttentionUpdated, *it))
	a.changedLocked()
	return h, ""
}

// endHold ends a hold with out, if it is still running, and reports whether
// it was. The item stays open with its request cleared.
func (a *attentionStore) endHold(requestID string, out approvalOutcome) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.endHoldLocked(requestID, out, true)
}

// endHoldLocked ends a hold. clearItem publishes the item without its request;
// a caller about to close or rewrite the item passes false. The caller holds mu.
func (a *attentionStore) endHoldLocked(requestID string, out approvalOutcome, clearItem bool) bool {
	h, ok := a.holds[requestID]
	if !ok {
		return false
	}
	delete(a.holds, requestID)
	a.rememberLocked(requestID, out)
	h.done <- out
	if !clearItem {
		return true
	}
	if it, ok := a.items[h.itemID]; ok && it.RequestID == requestID {
		a.rev++
		it.RequestID, it.Options, it.Expires, it.AlwaysScope = "", nil, 0, nil
		if h.latest != "" {
			it.Summary = h.latest
		}
		// With no hold there is no plan to serve and no deny to type a
		// reason for, so the item is the pane's approval again, marked by
		// what its line says now.
		it.Kind, it.DenyMessage, it.PlanLines, it.PlanSHA = AttentionApproval, false, 0, ""
		it.Risk = risk.Names(a.riskOfLine(it.Summary, h.root))
		it.Seq = a.rev
		a.publish(attentionEvent(AttentionUpdated, *it))
		a.changedLocked()
	}
	return true
}

// rememberLocked keeps how a hold ended, for a reply that comes after it.
func (a *attentionStore) rememberLocked(requestID string, out approvalOutcome) {
	if a.settled == nil {
		a.settled = make(map[string]approvalOutcome)
	}
	if _, ok := a.settled[requestID]; !ok {
		a.settledOrder = append(a.settledOrder, requestID)
	}
	a.settled[requestID] = out
	for len(a.settledOrder) > approvalSettledMax {
		delete(a.settled, a.settledOrder[0])
		a.settledOrder = a.settledOrder[1:]
	}
}

// holdFor finds the running hold on a pane, if there is one.
func (a *attentionStore) holdOn(session, window string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, h := range a.holds {
		if h.session == session && h.window == window {
			return id, true
		}
	}
	return "", false
}

// errNoHold is a reply to a request no hold is waiting on.
var errNoHold = errors.New("no hold")

// answer ends a hold with the person's reply. A decision closes the item as
// answered; ask hands the prompt back to the pane and leaves the item open.
// A reply to a hold that already ended returns how it ended with applied
// false, so the first reply wins and a repeat is harmless. A decision that
// names the line it was made from (shown) is refused with applied false and
// reason changed when the hold is on another line, so an answer can only
// approve what the person read.
//
// An allow (once or always) must also say what the person saw: a plan's
// digest (planSHA), without which or for another plan nothing is answered and
// the reason is changed; and for a call the risk rules matched, riskAck naming
// exactly those rules, without which errRiskUnacknowledged is returned and
// nothing is answered. A deny needs neither.
func (a *attentionStore) answer(requestID, decision, message, by, shown string, riskAck []string, planSHA string) (out approvalOutcome, h approvalHold, applied bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	hold, ok := a.holds[requestID]
	if !ok {
		if prev, ok := a.settled[requestID]; ok {
			return prev, approvalHold{id: requestID}, false, nil
		}
		return approvalOutcome{}, approvalHold{}, false, errNoHold
	}
	if decision == ApprovalAsk {
		out = approvalOutcome{Reason: approvalEndHandedBack, By: by}
		a.endHoldLocked(requestID, out, true)
		return out, *hold, true, nil
	}
	if !slices.Contains(hold.options, decision) {
		return approvalOutcome{}, *hold, false, errBadDecision
	}
	if shown != "" && shown != hold.summary {
		return approvalOutcome{Reason: approvalEndChanged}, *hold, false, nil
	}
	if decision != ApprovalDeny {
		if hold.kind == AttentionPlan && planSHA != hold.planSHA {
			return approvalOutcome{Reason: approvalEndChanged}, *hold, false, nil
		}
		if len(hold.risk) > 0 && !sameRuleSet(riskAck, risk.Names(hold.risk)) {
			return approvalOutcome{}, *hold, false, errRiskUnacknowledged
		}
	}
	out = approvalOutcome{Decision: decision, Message: message, Reason: approvalEndAnswered, By: by}
	a.endHoldLocked(requestID, out, false)
	a.closeWithLocked(hold.itemID, AttentionClosedAnswered, func(it *AttentionItem) {
		it.Answer, it.AnsweredBy = decision, by
	})
	return out, *hold, true, nil
}

// errBadDecision is a decision the hold's harness does not offer.
var errBadDecision = errors.New("decision not offered")

// noteFocused ends the hold on a pane the person just brought in front of
// them: the harness's own prompt is the quickest thing to answer there, and a
// held prompt shows nothing in the pane at all.
func (a *attentionStore) noteFocused(session, window string) {
	if a == nil || window == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := a.byKey[attentionKey(AttentionApproval, session, window, 0)]
	if !ok || a.items[id].RequestID == "" {
		return
	}
	a.endHoldLocked(a.items[id].RequestID, approvalOutcome{Reason: approvalEndViewed}, true)
}

// approvalMaxScope bounds the rules always may add, the same bound the hook
// applies (integration.MaxScopeLines).
const approvalMaxScope = integration.MaxScopeLines

// approvalLineShown reports whether an Inbox item can show line exactly as it
// is: the item's own cleaning (control characters, whitespace, masking, the
// length cut) leaves it unchanged, and every rune is a visible character or a
// space, so no format character such as a bidi override makes it read as
// something else. The hook checks the same before it asks; the daemon checks
// again because the person answers from the item, not from the hook.
func approvalLineShown(line string) bool {
	if line == "" || !utf8.ValidString(line) || attentionText(line, attentionMaxSummary) != line {
		return false
	}
	for _, r := range line {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// approvalScopeShown reports whether always may be offered with scope: one to
// approvalMaxScope rules, each shown as it is.
func approvalScopeShown(scope []string) bool {
	if len(scope) == 0 || len(scope) > approvalMaxScope {
		return false
	}
	for _, line := range scope {
		if !approvalLineShown(line) {
			return false
		}
	}
	return true
}

// approvalResult is request-approval's answer.
func approvalResult(requestID string, out approvalOutcome) map[string]any {
	res := map[string]any{
		"type":       "approval_result",
		"request_id": requestID,
		"decision":   out.Decision,
		"reason":     out.Reason,
	}
	if out.Message != "" {
		res["message"] = out.Message
	}
	if out.By != "" {
		res["answered_by"] = out.By
	}
	return res
}

// verbRequestApproval holds a pane's permission prompt for an answer from the
// Inbox. See the file comment for who may call it and every way it ends.
func (d *Daemon) verbRequestApproval(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string   `json:"session"`
		Window  string   `json:"window"`
		Harness string   `json:"harness"`
		Options []string `json:"options"`
		Summary string   `json:"summary"`
		Scope   []string `json:"always_scope"`
		// The fields a hook sends for risk rules, plans and deny reasons.
		Kind        string `json:"kind"`
		Plan        string `json:"plan"`
		Tool        string `json:"tool"`
		Target      string `json:"target"`
		DenyMessage bool   `json:"deny_message"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if cs != nil && cs.viaLink {
		return nil, hintedVerbError(ErrVerbForbidden, "request-approval is refused over a link: an approval is held by the hook of a pane on this machine", &VerbHint{
			Detail: "Nothing was held. Run the hook on the machine the pane is on.",
		})
	}
	if p.Kind != "" && !slices.Contains(approvalKinds, p.Kind) {
		return nil, invalidParam("kind", "kind is approval or plan", approvalKinds...)
	}
	isPlan := p.Kind == AttentionPlan
	switch {
	case isPlan && strings.TrimSpace(p.Plan) == "":
		return nil, invalidParam("plan", "plan is required with kind plan: the plan's text")
	case !isPlan && p.Plan != "":
		return nil, invalidParam("plan", "plan is only taken with kind plan")
	case len(p.Plan) > approvalMaxPlan || !utf8.ValidString(p.Plan):
		return nil, invalidParam("plan", "plan must be UTF-8 text of at most "+strconv.Itoa(approvalMaxPlan)+" bytes")
	case len(p.Tool) > approvalMaxTool:
		return nil, invalidParam("tool", "tool is longer than "+strconv.Itoa(approvalMaxTool)+" bytes")
	case len(p.Target) > approvalMaxTarget:
		return nil, invalidParam("target", "target is longer than "+strconv.Itoa(approvalMaxTarget)+" bytes")
	}
	harnessID := canonicalHarness(p.Harness)
	if harnessID == "" {
		return nil, invalidParam("harness", "harness is required: the id of the harness whose prompt this is, e.g. claude-code")
	}
	if p.Window == "" {
		return nil, invalidParam("window", "window is required: the pane whose prompt is held, normally $TUIOS_PANE_ID")
	}
	if p.Summary == "" {
		return nil, invalidParam("summary", "summary is required: the line the person answers from, the message the hook reported")
	}
	options := p.Options
	if len(options) == 0 {
		options = []string{ApprovalOnce, ApprovalDeny}
	}
	for _, o := range options {
		if !slices.Contains(approvalDecisions, o) {
			return nil, invalidParam("options", "options: "+echoName(o)+" is not a decision", approvalDecisions...)
		}
	}
	// Always is only offered with the rules it adds, each shown as it is.
	if slices.Contains(options, ApprovalAlways) && !approvalScopeShown(p.Scope) {
		options = slices.DeleteFunc(slices.Clone(options), func(o string) bool { return o == ApprovalAlways })
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	st := sess.GetState()
	idx, err := findWindowStateIndex(st.Windows, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := st.Windows[idx]

	policy := d.approvalPolicy()
	// A pane start-agent --protocol opened holds without the table: choosing
	// the protocol is the opt-in (agent_protocol.go). Every check below still
	// applies to it.
	if !policy.Enabled[harnessID] && d.paneProtocol(w.ID) == "" {
		return approvalResult("", approvalOutcome{Reason: approvalEndDisabled}), nil
	}
	if isPlan && !policy.Plans {
		return approvalResult("", approvalOutcome{Reason: approvalEndDisabled}), nil
	}
	if fromPane, own := d.peerPane(cs); fromPane && own != w.ID {
		return nil, hintedVerbError(ErrVerbForbidden, "request-approval from inside a pane may only hold that pane's own prompt", &VerbHint{
			Param:  "window",
			Detail: "Nothing was held. The hook finds its pane from TUIOS_PANE_ID; a process in one pane cannot open an approval for another.",
		})
	}
	if w.AgentState != AgentStateNeedsInput {
		return approvalResult("", approvalOutcome{Reason: approvalEndNotBlocked}), nil
	}
	if d.paneInFrontOfPerson(sess, w.ID) {
		return approvalResult("", approvalOutcome{Reason: approvalEndViewed}), nil
	}
	summary := p.Summary
	if isPlan {
		// A plan is answered from its whole text, which the Inbox shows and
		// the answer names by digest, so its summary is only the title the
		// row carries: cleaned the way every item line is, not refused.
		summary = attentionText(summary, attentionMaxSummary)
		if summary == "" {
			return approvalResult("", approvalOutcome{Reason: approvalEndNotShown}), nil
		}
	} else if !approvalLineShown(summary) {
		return approvalResult("", approvalOutcome{Reason: approvalEndNotShown}), nil
	}

	spec := holdSpec{
		session:     sess.Name,
		window:      w.ID,
		kind:        AttentionApproval,
		summary:     summary,
		options:     options,
		scope:       p.Scope,
		tool:        p.Tool,
		target:      p.Target,
		root:        paneRoot(st, w),
		denyMessage: p.DenyMessage,
	}
	if isPlan {
		spec.kind, spec.plan, spec.tool, spec.target = AttentionPlan, p.Plan, "", ""
	} else {
		spec.risk = d.attention.riskOfCall(spec.tool, spec.target, summary, spec.root)
	}
	holdFor := policy.holdFor()
	spec.expires = time.Now().Add(holdFor)
	hold, reason := d.attention.startHold(spec)
	if hold == nil {
		return approvalResult("", approvalOutcome{Reason: reason}), nil
	}
	LogBasic("Approval %s held for %s in %s (%s) for up to %s", hold.id, w.ID, sess.Name, harnessID, holdFor)

	var gone <-chan struct{}
	if cs != nil && cs.conn != nil {
		var stop func()
		gone, stop = watchPeerGone(cs.conn)
		defer stop()
	}
	timer := time.NewTimer(holdFor)
	defer timer.Stop()
	select {
	case out := <-hold.done:
		return approvalResult(hold.id, out), nil
	case <-timer.C:
		d.attention.endHold(hold.id, approvalOutcome{Reason: approvalEndTimeout})
	case <-gone:
		d.attention.endHold(hold.id, approvalOutcome{Reason: approvalEndCallerGone})
	case <-d.ctx.Done():
		d.attention.endHold(hold.id, approvalOutcome{Reason: approvalEndShutdown})
	}
	// Whatever ended it, the outcome is in done now: endHold put it there, or
	// an answer that won the race did.
	out := <-hold.done
	LogBasic("Approval %s ended: %s", hold.id, out.Reason)
	return approvalResult(hold.id, out), nil
}

// verbReplyApproval answers a held approval for the person.
func (d *Daemon) verbReplyApproval(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		RequestID  string `json:"request_id"`
		Session    string `json:"session"`
		Window     string `json:"window"`
		Decision   string `json:"decision"`
		Message    string `json:"message"`
		HumanNonce string `json:"human_nonce"`
		// Summary is the line the decision was made from. When it is set
		// and the hold is on another line, nothing is answered.
		Summary string `json:"summary"`
		// RiskAck names the risk rules the person saw, for an allow of a
		// risky call, and PlanSHA the plan they read, for an allow of a
		// plan. See answer.
		RiskAck []string `json:"risk_ack"`
		PlanSHA string   `json:"plan_sha"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !slices.Contains(approvalReplies, p.Decision) {
		msg := "decision is required"
		if p.Decision != "" {
			msg = "decision: " + echoName(p.Decision) + " is not a decision"
		}
		return nil, invalidParam("decision", msg, approvalReplies...)
	}
	by, ok := d.humanNonceClient(p.HumanNonce, cs)
	if !ok {
		return nil, hintedVerbError(ErrVerbNotHuman, "reply-approval is for the person at an attached client", &VerbHint{
			Param:  "human_nonce",
			Detail: "Nothing was answered. Only a client attached right now can answer an approval, by passing the nonce its attach reply carried. An agent never can.",
		})
	}
	requestID := p.RequestID
	if requestID == "" {
		if p.Window == "" {
			return nil, invalidParam("request_id", "request_id is required, or session and window to answer the pane's held prompt")
		}
		sess, verr := d.resolveVerbSession(p.Session)
		if verr != nil {
			return nil, verr
		}
		st := sess.GetState()
		idx, err := findWindowStateIndex(st.Windows, p.Window)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		id, ok := d.attention.holdOn(sess.Name, st.Windows[idx].ID)
		if !ok {
			return nil, noHoldError("no approval is held for window " + echoName(p.Window))
		}
		requestID = id
	}
	message := attentionText(p.Message, approvalMaxMessage)
	out, hold, applied, err := d.attention.answer(requestID, p.Decision, message, by, p.Summary, p.RiskAck, p.PlanSHA)
	switch {
	case errors.Is(err, errNoHold):
		return nil, noHoldError("no approval is held under request " + echoName(requestID))
	case errors.Is(err, errBadDecision):
		return nil, invalidParam("decision", "this prompt does not offer "+echoName(p.Decision), append(slices.Clone(hold.options), ApprovalAsk)...)
	case errors.Is(err, errRiskUnacknowledged):
		return nil, riskUnacknowledgedError(risk.Names(hold.risk), "Nothing was answered and the hold runs on. The Inbox allows a risky call on a second press of the same key; a client that predates that answers it in the pane.")
	}
	if !applied && out.Reason == approvalEndChanged {
		// The hold is on another call, or another plan, than the one the
		// person read. Nothing was answered and the hold runs on, so they can
		// read it and answer.
		res := map[string]any{
			"type":       "approval_replied",
			"request_id": requestID,
			"decision":   "",
			"applied":    false,
			"reason":     approvalEndChanged,
			"summary":    hold.summary,
			"session":    hold.session,
			"window":     hold.window,
		}
		if hold.planSHA != "" {
			res["plan_sha"] = hold.planSHA
		}
		return res, nil
	}
	if !applied && out.Decision == "" && p.Decision != ApprovalAsk {
		return nil, noHoldError("the hold on request " + echoName(requestID) + " ended (" + out.Reason + ") before this reply")
	}
	if applied && out.Decision != "" {
		LogBasic("Approval %s answered %s by %s", requestID, out.Decision, by)
		// The hook is on its way back to the harness with the answer, and
		// the next report it would send is working. Saying it now closes the
		// block for every client at once, and only if the pane is still on it.
		if sess := d.manager.GetSession(hold.session); sess != nil {
			_, _, _, _ = sess.applyAgentReport(hold.window, AgentReport{
				State:   AgentStateWorking,
				IfState: []AgentState{AgentStateNeedsInput},
			})
		}
	}
	decision := out.Decision
	if applied && p.Decision == ApprovalAsk {
		decision = ApprovalAsk
	}
	res := map[string]any{
		"type":       "approval_replied",
		"request_id": requestID,
		"decision":   decision,
		"applied":    applied,
		"reason":     out.Reason,
	}
	if out.By != "" {
		res["answered_by"] = out.By
	}
	if hold.session != "" {
		res["session"], res["window"] = hold.session, hold.window
	}
	return res, nil
}

// noHoldError is the refusal for a reply nothing is waiting on.
func noHoldError(msg string) *verbError {
	return hintedVerbError(ErrVerbInvalidParams, msg, &VerbHint{
		Param:   "request_id",
		Verb:    "list-attention",
		Command: "tuios list-attention",
		Detail:  "A hold ends when it times out, when the pane moves on, when someone answers it, or when the prompt is handed back to the pane. The harness then asks in its pane, so answer it there.",
	})
}

// humanNonceClient is verifyAnyHumanNonce that also names the client whose
// attach the nonce came from, for the record of who answered.
func (d *Daemon) humanNonceClient(nonce string, sender *connState) (string, bool) {
	id, ok := d.matchHumanNonceClient(nonce, "", sender)
	return id, ok
}

// paneInFrontOfPerson reports whether a client the person holds is attached
// to the session with the pane focused. Holding that pane's prompt would hide
// it from someone already looking at it.
func (d *Daemon) paneInFrontOfPerson(sess *Session, window string) bool {
	if sess.GetState().FocusedWindowID != window {
		return false
	}
	var attached []*connState
	d.clientsMu.RLock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		if cs.attached && cs.isTUIClient && cs.sessionID == sess.ID {
			attached = append(attached, cs)
		}
		cs.mu.Unlock()
	}
	d.clientsMu.RUnlock()
	return slices.ContainsFunc(attached, d.mayActAsHuman)
}

// peerPlacer places the process on a connection: outside every pane, or in
// one, named by its window when that can be told.
type peerPlacer func(cs *connState) (fromPane bool, window string)

// peerPane says whether the process on cs runs inside a pane of this daemon,
// and which pane when that can be told. See peerPaneWindow.
func (d *Daemon) peerPane(cs *connState) (bool, string) {
	if place := d.approvalPeer.Load(); place != nil {
		return (*place)(cs)
	}
	return d.peerPaneWindow(cs)
}

// peerPaneWindow places the process on cs: outside every pane, or in one pane,
// found the way resolve-pane finds one (an ancestor that is a pane's shell,
// then the controlling terminal) and last by the TUIOS_PANE_ID in its
// environment. A process inside a pane that none of these place gets an empty
// window, which matches no target, so request-approval fails closed on it.
func (d *Daemon) peerPaneWindow(cs *connState) (bool, string) {
	if !d.connFromPane(cs) {
		return false, ""
	}
	pid := cs.peerPID
	shells := d.localPaneShells()
	chain := []int{pid}
	for cur, depth := pid, 0; depth < paneOriginMaxDepth; depth++ {
		ppid, _, ok := readProcLineage(cur)
		if !ok || ppid <= 1 {
			break
		}
		chain = append(chain, ppid)
		cur = ppid
	}
	if m, ok := matchPaneByProcess(shells, 0, chain); ok {
		return true, m.pane.windowID
	}
	if _, tty, ok := readProcLineage(pid); ok && tty != 0 {
		for _, sh := range shells {
			if _, shTTY, ok := readProcLineage(sh.shellPID); ok && shTTY == tty {
				return true, sh.windowID
			}
		}
	}
	if id, ok := readProcEnvVar(pid, "TUIOS_PANE_ID"); ok && id != "" && d.holdsWindow(id) {
		return true, id
	}
	return true, ""
}

// watchPeerGone reports when the process on the other end of conn goes away
// while a verb blocks on it. The connection is the hook's own and it sends
// nothing more while it waits, so a read that returns means the peer closed
// it. A byte that does arrive is discarded, which the verb's documentation
// says. stop ends the watch and leaves the connection readable again.
func watchPeerGone(conn net.Conn) (<-chan struct{}, func()) {
	gone := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var b [1]byte
		_, err := conn.Read(b[:])
		if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
			return
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return
		}
		close(gone)
	}()
	return gone, func() {
		_ = conn.SetReadDeadline(time.Now())
		<-finished
		_ = conn.SetReadDeadline(time.Time{})
	}
}
