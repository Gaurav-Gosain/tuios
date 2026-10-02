//go:build !slim

package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

func (d *Daemon) verbSetAgentState(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		State   string `json:"state"`
		Message string `json:"message"`
		Source  string `json:"source"`
		Harness string `json:"harness"`
		// The fields below are what a hook reporter adds. Every one is
		// optional, and a caller that sends none of them is handled exactly as
		// before they existed.
		Kind           string `json:"kind"`
		AgentSessionID string `json:"agent_session_id"`
		TranscriptPath string `json:"transcript_path"`
		IfState        string `json:"if_state"`
		HarnessPID     int    `json:"harness_pid"`
		// Activity is one hook event for the pane's activity ring. It is
		// recorded when the report passes the identity guard, whether or not
		// the state part applies. See agent_activity.go.
		Activity *AgentActivityReport `json:"activity"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := checkActivityReport(p.Activity, activityEvents); verr != nil {
		return nil, verr
	}
	if p.State == "" {
		return nil, invalidParam("state", "state is required, one of: "+strings.Join(AgentStateNames, ", "))
	}
	state, ok := ParseAgentState(p.State)
	if !ok {
		return nil, hintedVerbError(ErrVerbInvalidParams, "unknown agent state "+echoName(p.State), &VerbHint{
			Param:      "state",
			DidYouMean: closestMatch(p.State, AgentStateNames),
			Available:  AgentStateNames,
			Detail:     "state names the pane's agent state. Use none to clear it.",
		})
	}
	// An omitted source is a report, so a caller written before sources existed
	// keeps the authority it had.
	source, ok := ParseAgentSource(p.Source)
	if !ok {
		return nil, hintedVerbError(ErrVerbInvalidParams, "unknown agent state source "+echoName(p.Source), &VerbHint{
			Param:      "source",
			DidYouMean: closestMatch(p.Source, AgentSourceNames),
			Available:  AgentSourceNames,
			Detail:     "source says where the state came from and decides which of two competing reports wins. Omit it to report for yourself.",
		})
	}
	if p.Kind != "" {
		if p.Kind != harness.PromptKindApproval && p.Kind != harness.PromptKindQuestion {
			return nil, hintedVerbError(ErrVerbInvalidParams, "unknown kind "+echoName(p.Kind), &VerbHint{
				Param:     "kind",
				Available: agentKindNames,
				Detail:    "kind says what a needs_input state waits for.",
			})
		}
		if state != AgentStateNeedsInput {
			return nil, invalidParam("kind", "kind describes a needs_input state, and this report is "+state.Name())
		}
	}
	var ifState []AgentState
	for name := range strings.SplitSeq(p.IfState, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		st, ok := ParseAgentState(name)
		if !ok {
			return nil, hintedVerbError(ErrVerbInvalidParams, "unknown agent state "+echoName(name)+" in if_state", &VerbHint{
				Param:      "if_state",
				DidYouMean: closestMatch(name, AgentStateNames),
				Available:  AgentStateNames,
				Detail:     "if_state lists the states the window must be in for the report to apply.",
			})
		}
		ifState = append(ifState, st)
	}
	sess, target, verr := d.reportTarget(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}

	report := AgentReport{
		State:      state,
		Message:    p.Message,
		Source:     source,
		Harness:    p.Harness,
		Kind:       p.Kind,
		SessionID:  p.AgentSessionID,
		HarnessPID: p.HarnessPID,
		IfState:    ifState,
	}
	// The identity guard is read before the report applies, because it is
	// about the pane as the report found it, and applyAgentReport checks
	// if_state first and so may never reach it.
	var windowID, guard string
	if p.Activity != nil {
		windowID, guard = sess.agentReportGuard(target, report)
	}
	// A pane's first activity gets its ring before the report applies, so
	// the state change the report makes reaches the ring through the session
	// event sink, completion_seq step included, the same as for a pane that
	// already had one. Without this a first report that finished a turn, a
	// Stop after the hooks were installed mid-session or after a daemon
	// restart, kept its turn_end entry but did not count the turn.
	created := false
	if p.Activity != nil && windowID != "" && guard == "" {
		created = d.activity.ensure(sess.ID, windowID)
	}
	effective, applied, reason, err := sess.applyAgentReport(target, report)
	if err != nil {
		if created {
			d.activity.forgetIfEmpty(sess.ID, windowID)
		}
		return nil, mapResolveErr(err, sess)
	}
	// Activity is recorded for a report from the pane's own agent, applied or
	// not: a PostToolUse refused by if_state still finished a tool call. A
	// report the identity guard refuses is a nested run's, and its activity
	// is not the pane's.
	recorded := false
	if p.Activity != nil && windowID != "" && guard == "" &&
		reason != agentRefusedForeignSession && reason != agentRefusedForeignHarness {
		d.recordAgentActivity(sess, windowID, p.Activity, effective)
		recorded = true
	} else if created {
		// The guard read before the report let it through and the report
		// then found a nested run: the ring made for it holds nothing of the
		// pane's agent.
		d.activity.forgetIfEmpty(sess.ID, windowID)
	}
	if applied && p.TranscriptPath != "" {
		d.joinReportedTranscript(sess, target, p.Harness, p.TranscriptPath)
	}
	// state is the effective state, so a report a higher-ranked source outranked
	// reports what the pane actually shows rather than what was asked for.
	// applied says which of the two happened, and reason says why not.
	out := map[string]any{
		"type":    "agent_state_set",
		"state":   effective.Name(),
		"message": p.Message,
		"source":  source.Name(),
		"applied": applied,
	}
	if reason != "" {
		out["reason"] = reason
	}
	if p.Activity != nil {
		out["activity_recorded"] = recorded
	}
	return out, nil
}

// reportTarget resolves the pane a report is about. A report from inside a
// pane that names no window is about that pane, not about the focused one.
// The pane is the one the daemon placed the caller in, so a hook or a person
// in the pane marks the pane they are in. A caller outside every pane reports
// about the focused window.
func (d *Daemon) reportTarget(cs *connState, sessionName, window string) (*Session, string, *verbError) {
	var own *paneAuth
	if window == "" {
		if pa := d.paneAuthority(cs); pa != nil && !pa.hosted && pa.window != "" && pa.window != unplacedWindow && pa.session != "" {
			own = pa
			if sessionName == "" {
				sessionName = pa.session
			}
		}
	}
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, "", verr
	}
	target := window
	if target == "" && own != nil && sess.Name() == own.session {
		target = own.window
	}
	if target == "" {
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, "", mapResolveErr(err, sess)
		}
		target = id
	}
	return sess, target, nil
}

// Rates of report-agent-activity. Each event it records may change what the
// rail draws, and so push state to every attached client, and the subagent
// cap bounds memory, not pushes. So a pane may report a burst of
// agentActivityBurst events, then agentActivityRate a second; a call past
// that is refused with rate_limited and records nothing. The burst is a full
// set of subagents starting at once.
const (
	agentActivityRate  = 10.0
	agentActivityBurst = float64(subagentsMax)
)

// verbReportAgentActivity records one hook event of a pane's own agent with
// no state report: a subagent starting or stopping, a conversation starting,
// or any other activity event. The event goes into the pane's ring and moves
// its reserved metadata keys, the subagents key above all, once it passes
// activityReportGuard. Nothing else moves: not the state, its source or its
// stamp, not the message, and not the harness the pane is attributed to.
func (d *Daemon) verbReportAgentActivity(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session        string               `json:"session"`
		Window         string               `json:"window"`
		Harness        string               `json:"harness"`
		AgentSessionID string               `json:"agent_session_id"`
		HarnessPID     int                  `json:"harness_pid"`
		Activity       *AgentActivityReport `json:"activity"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Activity == nil {
		return nil, invalidParam("activity", "activity is required: the hook event to record, with its event")
	}
	if verr := checkActivityReport(p.Activity, reportActivityEvents); verr != nil {
		return nil, verr
	}
	sess, target, verr := d.reportTarget(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	windowID, state, reason, err := sess.activityReportGuard(target, AgentReport{
		Harness:    p.Harness,
		SessionID:  p.AgentSessionID,
		HarnessPID: p.HarnessPID,
	})
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	if !d.activityReports.take(windowID, time.Now(), agentActivityRate, agentActivityBurst) {
		return nil, hintedVerbError(ErrVerbRateLimited, "this pane reports activity too fast", &VerbHint{
			Param:  "activity",
			Detail: fmt.Sprintf("a pane may report a burst of %d events, then %g a second. Nothing was recorded; report again later.", int(agentActivityBurst), agentActivityRate),
		})
	}
	recorded := false
	if reason == "" {
		recorded = d.recordAgentActivity(sess, windowID, p.Activity, state)
	}
	out := map[string]any{
		"type":      "agent_activity_reported",
		"window_id": windowID,
		"state":     state.Name(),
		"recorded":  recorded,
		"subagents": sess.subagentCount(windowID),
	}
	if reason != "" {
		out["reason"] = reason
	}
	return out, nil
}

// verbSetAgentSession stores the conversation id a harness reports for a pane
// without touching the pane's state. See applyAgentSession.
func (d *Daemon) verbSetAgentSession(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session        string `json:"session"`
		Window         string `json:"window"`
		Harness        string `json:"harness"`
		AgentSessionID string `json:"agent_session_id"`
		HarnessPID     int    `json:"harness_pid"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	harnessID := strings.TrimSpace(p.Harness)
	if harnessID == "" {
		return nil, invalidParam("harness", "harness is required: the id of the harness the conversation belongs to, e.g. qwen")
	}
	sid := strings.TrimSpace(p.AgentSessionID)
	if sid == "" {
		return nil, invalidParam("agent_session_id", "agent_session_id is required: the harness's own id for the conversation")
	}
	if len(sid) > maxAgentSessionIDLen {
		return nil, invalidParam("agent_session_id", fmt.Sprintf("agent_session_id is %d bytes; the limit is %d", len(sid), maxAgentSessionIDLen))
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(sess.GetState())
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	stored, applied, reason, err := sess.applyAgentSession(target, AgentSessionReport{
		Harness:    harnessID,
		SessionID:  sid,
		HarnessPID: p.HarnessPID,
	})
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	out := map[string]any{
		"type":             "agent_session_set",
		"agent_session_id": stored,
		"applied":          applied,
	}
	if reason != "" {
		out["reason"] = reason
	}
	return out, nil
}

// maxAgentSessionIDLen bounds a reported conversation id. Every harness uses a
// uuid or a short token; the cap only stops a caller parking a blob on the
// window, which is synced to every client and persisted.
const maxAgentSessionIDLen = 256

// agentKindNames are the values set-agent-state accepts for kind. They are
// the manifest rule kinds, so a hook and a screen rule describe a block in the
// same words.
var agentKindNames = []string{harness.PromptKindApproval, harness.PromptKindQuestion}

// joinReportedTranscript binds a window to the transcript file its harness
// named in a hook. This is the exact join the transcript source was built for:
// the searched join refuses whenever two files could be the pane's, and the
// harness naming its own file settles that. Only a harness whose manifest has
// a transcript reader is joined, since nothing else could read the file, and
// a failure leaves the pane on whatever join it had.
func (d *Daemon) joinReportedTranscript(sess *Session, target, harnessID, path string) {
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return
	}
	w := state.Windows[idx]
	if harnessID == "" {
		harnessID = w.AgentHarness
	}
	reg := d.agentMatcher.registry
	if harnessID == "" || reg == nil || reg.TranscriptFor(harnessID) == nil {
		return
	}
	_ = sess.JoinAgentTranscript(w.ID, harnessID, path, true)
}

func (d *Daemon) verbGetAgentState(_ *connState, params json.RawMessage) (any, *verbError) {
	var p commonParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	state := sess.GetState()
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := state.Windows[idx]
	claim := sess.agentClaimFor(w.ID)
	return map[string]any{
		"type":           "agent_state",
		"window_id":      w.ID,
		"state":          w.AgentState.Name(),
		"message":        w.AgentMessage,
		"agent_state_at": w.AgentStateAt,
		// source and harness_id are what make a shown state explainable: which
		// tier put it there and, once the harness registry lands, which harness.
		// harness_id is empty until something names one.
		"source":     claim.source.Name(),
		"harness_id": claim.harness,
		// identity and confidence say what kind of evidence named the harness:
		// a report from the harness itself is certain, a process name is strong,
		// and a pane nothing has named has none.
		"identity":   string(claim.identity),
		"confidence": claim.identity.confidence(),
		// evidence_age_ms is how old the newest evidence behind state is, so
		// a consumer can tell a fresh answer from a stale one.
		"evidence_age_ms": evidenceAgeMS(sess.evidenceStamp(w, claim), d.evidenceNow()),
		// needs_you is the one question a person asks of a pane, answered as a
		// bool so a consumer does not have to know which states mean it.
		"needs_you": w.AgentState.NeedsYou(),
		"activity":  w.AgentState.Activity(),
		// ready and blocked_by are the same answers list-agents gives: whether
		// ask-agent would type at the pane now, and, for a pane on needs_input,
		// whether it waits on an approval or a question. blocked_by is also
		// where the kind a hook reported with set-agent-state reads back.
		"ready":      d.agentReady(w, agentRestStates),
		"blocked_by": agentBlockedBy(w),
		// The harness's own conversation id, empty until a hook reports one.
		"agent_session_id": w.AgentSessionID,
		// meta is what set-agent-meta recorded, key to value.
		"meta": agentMetaMap(w.AgentMeta, time.Now().UnixNano()),
		// queued is how many messages wait in the pane's delivery queue.
		"queued": w.AgentQueued,
		// subagents is how many subagents the pane's agent is running, as
		// its hooks reported them; meta's subagents key says the same in
		// words.
		"subagents": w.AgentSubagents,
	}, nil
}

// verbExplainAgentDetect says what the foreground-process detector sees in a
// pane and what every manifest makes of it.
//
// Detection was unfalsifiable from outside. A pane was an agent or it was not,
// with no way to ask which of comm, argv0, argv_path or exe_glob decided it, or
// even what the daemon had read. That is why a registry in which every exe_glob
// matched nothing shipped, and why a rule that matched any process with an
// agent's name anywhere in its arguments went unnoticed until users found
// unrelated panes turning into agents. This is the counterpart to
// explain-agent-screen: that one explains a state, this one explains the name.
//
// The answer leads with a verdict in plain words and the evidence it rests on,
// then lists every word on the command line that looks like an agent's name and
// was not counted. A person who reports a false positive should be able to read
// the answer once and see either the rule that fired or the word they took for
// one.
func (d *Daemon) verbExplainAgentDetect(_ *connState, params json.RawMessage) (any, *verbError) {
	var p commonParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	state := sess.GetState()
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := state.Windows[idx]
	claim := sess.agentClaimFor(w.ID)
	// A pane nothing has claimed has no source. The zero claim names itself
	// report, and printing that would say a shell prompt reported for itself.
	source := ""
	if isAgentWindow(w) {
		source = claim.source.Name()
	}

	out := map[string]any{
		"type":            "agent_detect",
		"window_id":       w.ID,
		"state":           w.AgentState.Name(),
		"source":          source,
		"harness_id":      w.AgentHarness,
		"auto_detected":   claim.auto,
		"identity":        string(claim.identity),
		"confidence":      claim.identity.confidence(),
		"evidence_age_ms": evidenceAgeMS(sess.evidenceStamp(w, claim), d.evidenceNow()),
		"needs_you":       w.AgentState.NeedsYou(),
		"activity":        w.AgentState.Activity(),
		"running":         false,
		"matched":         false,
	}
	var evidence []string
	if claim.identity == identityReport && claim.harness != "" {
		evidence = append(evidence, "The harness "+claim.harness+" named itself in a report. That is certain.")
	}

	// Read the process now rather than reporting what the last poll happened to
	// see. A diagnostic that shows a cached answer cannot be used to check a rule
	// against a pane the user is looking at.
	info, running := d.foregroundResolver(sess)(w.PTYID)
	out["running"] = running
	if !running {
		// Not an error: a pane with no live process is the ordinary case, and
		// saying so is the answer.
		out["reason"] = "No foreground process can be read for this pane."
		out["verdict"] = "This pane runs no process that tuios can read."
		out["evidence"] = evidence
		return out, nil
	}

	proc := info.proc()
	out["process"] = harness.Describe(proc)
	if reg := d.agentMatcher.registry; reg != nil {
		out["manifests"] = reg.ExplainDetect(proc)
	}
	if rule, ok := d.agentMatcher.nameRule(proc); ok {
		out["name_list"] = rule
	}
	det, ok := d.agentMatcher.identifyDetail(info)
	if len(det.visited) > 0 {
		group := make([]map[string]any, 0, len(det.visited))
		for _, member := range det.visited {
			_, matched := d.agentMatcher.matchProc(member)
			group = append(group, map[string]any{
				"pid":     member.pid,
				"depth":   member.depth,
				"comm":    member.comm,
				"argv":    member.argv,
				"exe":     member.exe,
				"matched": matched,
			})
		}
		out["group"] = group
	}
	label := processLabel(info)
	switch {
	case ok:
		out["matched"] = true
		out["matched_rule"] = det.rule
		out["matched_harness"] = det.harness
		out["matched_via"] = det.via
		if claim.identity != identityReport {
			out["confidence"] = det.tier.confidence()
			out["identity"] = string(det.tier)
		}
		name := det.harness
		if name == "" {
			name = "an agent named " + processLabel(det.proc)
			out["note"] = "The name list matched, not a manifest. No harness is named, so no screen rules run."
		}
		if det.tier == identityHint {
			// The process itself was not recognised: its environment named
			// the harness. Said as that, so nobody reads it as a name match.
			out["verdict"] = "This pane runs " + name + ", as named by " + AgentHintEnv + "."
			evidence = append(evidence, fmt.Sprintf("Named by %s on pid %d (%s). The process itself is not a known agent.",
				det.rule, det.proc.pid, processLabel(det.proc)))
			break
		}
		what := "The process " + processLabel(det.proc) + " matched " + describeRule(det)
		if len(det.via) > 0 {
			out["verdict"] = "This pane runs " + name + " behind " + strings.Join(det.via, ", ") + "."
			evidence = append(evidence, "The foreground process "+label+" is a wrapper, so tuios read the processes behind it.")
		} else {
			out["verdict"] = "This pane runs " + name + "."
		}
		evidence = append(evidence, what)
		if det.harness == "" {
			evidence = append(evidence, "A process name is strong evidence. It names no harness.")
		} else {
			evidence = append(evidence, "A process name is strong evidence.")
		}
	case info.atShell():
		out["verdict"] = "This pane is at its shell prompt. It runs no agent."
	default:
		out["verdict"] = "This pane does not run an agent. The foreground process is " + label + "."
		if proc.Wraps() {
			if len(det.visited) == 0 {
				evidence = append(evidence, "The process "+label+" is a wrapper. Nothing runs behind it.")
			} else {
				evidence = append(evidence, fmt.Sprintf("The process %s is a wrapper. None of the %d processes behind it is an agent.",
					label, len(det.visited)))
			}
		}
		if info.pid > 0 && info.group == nil && proc.Wraps() {
			evidence = append(evidence, "This platform cannot list the processes behind a wrapper.")
		}
		out["ignored"] = d.agentMatcher.mentions(info)
	}
	out["evidence"] = evidence
	return out, nil
}

// describeRule spells a detection's rule as a sentence fragment: which manifest
// or list it came from, and the predicate.
func describeRule(det detection) string {
	if det.harness != "" {
		return "the manifest " + det.harness + " on " + det.rule + "."
	}
	return "the name list on " + strings.TrimPrefix(det.rule, "name ") + "."
}

// verbExplainAgentScreen dumps a pane's tail exactly as the screen tier reads
// it, with what every rule of its harness made of it.
//
// Writing a screen rule was otherwise guesswork in both directions: the text is
// matched inside the daemon against a pane that has moved on by the time anyone
// looks, and a rule that fails says nothing about which of its strings was the
// reason. This answers both, so adding a harness is an edit and a re-run rather
// than an experiment.
func (d *Daemon) verbExplainAgentScreen(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		commonParams
		Harness string `json:"harness"`
		Lines   int    `json:"lines"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	state := sess.GetState()
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := state.Windows[idx]
	claim := sess.agentClaimFor(w.ID)

	// A harness named on the call rather than on the pane is how a rule is tried
	// against a pane the detector has not attributed yet, which is every pane
	// while the rule that would attribute it is still being written.
	hid := strings.TrimSpace(p.Harness)
	if hid == "" {
		hid = w.AgentHarness
	}

	reg := d.agentMatcher.registry
	var m *harness.Manifest
	if reg != nil && hid != "" {
		if m = reg.Lookup(hid); m == nil {
			return nil, hintedVerbError(ErrVerbInvalidParams, "unknown harness "+echoName(hid), &VerbHint{
				Param:      "harness",
				DidYouMean: closestMatch(hid, reg.IDs()),
				Available:  reg.IDs(),
				Detail:     "harness names a manifest in the registry. Drop a file in the user manifest directory to add one.",
			})
		}
	}

	// How far up the rules would read, so what is dumped is what would be
	// matched. An explicit lines wins, for checking whether a rule needs to see
	// further up than its manifest lets it.
	lines := p.Lines
	if lines <= 0 && m != nil {
		lines = m.Screen.Lines
	}
	if lines <= 0 {
		lines = harness.DefaultScreenLines
	}

	// The tail is read whether or not a harness was resolved. Writing the first
	// rule for a harness tuios does not know yet means looking at a pane nothing
	// has claimed, so refusing to dump it there would withhold the diagnostic
	// from the case it is most needed in.
	// The pane title is read here, in the same look as the tail, so the
	// explanation and the classification below run against one reading rather
	// than two of a value that moves.
	var tail []string
	var paneTitle, paneProgress string
	if w.PTYID != "" {
		if pty := sess.GetPTY(w.PTYID); pty != nil {
			tail = pty.tailText(lines)
			paneTitle = pty.Title()
			paneProgress = pty.ProgressText()
		}
	}

	out := map[string]any{
		"type":       "agent_screen",
		"window_id":  w.ID,
		"harness_id": hid,
		"state":      w.AgentState.Name(),
		"source":     claim.source.Name(),
		"lines":      lines,
		"tail":       tail,
		"rules":      []harness.RuleReport{},
		"matched":    false,
		"rule":       -1,
		"title":      paneTitle,
	}
	if m == nil {
		// No harness means no rules to run, which is a fact worth returning
		// rather than an error: it is the answer for most panes.
		return out, nil
	}
	out["enabled"] = m.Screen.Enabled
	// Which file is in force, so a user override that shadows a bundled
	// manifest is visible where its rules are being read.
	source, replaced := m.Source()
	out["manifest_source"] = source
	out["replaces_bundled"] = replaced

	matchedState, rule, reports := reg.Explain(hid, tail)
	out["rules"] = reports
	out["rule"] = rule
	out["matched"] = rule >= 0
	out["rule_state"] = matchedState

	// The title tier, reported beside the screen tier rather than in a verb of
	// its own. Someone asking why a pane reads the way it does is asking about
	// the pane, not about one channel, and a title rule that fired is exactly
	// the thing they would otherwise have no way to see: the string it matched
	// is gone from the screen by the time anyone looks.
	out["title"] = paneTitle
	out["title_enabled"] = m.Title.Enabled
	out["progress"] = paneProgress
	titleState, titleRule, titleReports := reg.ExplainOSC(hid, paneTitle, paneProgress)
	out["title_rules"] = titleReports
	out["title_rule"] = titleRule
	out["title_matched"] = titleRule >= 0
	out["title_rule_state"] = titleState
	return out, nil
}
