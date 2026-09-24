package app

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The away recap: what an agent did while the person was looking at
// something else, from the activity ring the daemon keeps per agent pane
// (agent-activity, see session/agent_activity.go).
//
// It is shown in two places. The Inbox's detail for a Finished item says it
// under the list whenever [agents.recap] mode is toast or inbox. The dock says
// it in one line when the person comes back to a pane that finished at least
// one turn while they were away for at least [agents.recap] away, when mode
// is toast, the default.
//
// "Away" is this client's: SidebarAgentSeenAt records when the person last
// had the pane in front of them (sidebar_unread.go), and the recap starts
// there. Nothing here runs on a timer. The Inbox reads a recap when a
// finished item comes under the cursor, and the toast reads one when focus
// lands on a pane, both from calls the input path already makes; a pane no
// agent has reported on never gets as far as either.

// inboxRecapTimeout bounds one agent-activity call.
const inboxRecapTimeout = 5 * time.Second

// inboxRecapView is the recap the Inbox shows for the selected finished item,
// and the return toast waiting to be read.
type inboxRecapView struct {
	// key names the item and the time the recap starts at, so a recap is
	// read once per item and change.
	key     string
	loading bool
	err     error
	recap   *session.AgentActivityRecap
	// since is where the shown recap starts, 0 for the whole ring.
	since int64
	// pending is a return the dock should say, waiting for AgentRecapFetch.
	pending *agentReturn
}

// agentReturn is the person coming back to a pane that finished turns while
// they were away.
type agentReturn struct {
	session, window, who string
	// since is when they looked away, in unix nanoseconds.
	since int64
	// turns is how many turns finished since then, by the pane's count.
	turns uint64
	// state is the pane's agent state now.
	state string
}

// InboxRecapMsg is an agent-activity reply for the Inbox's detail.
type InboxRecapMsg struct {
	Key   string
	Recap *session.AgentActivityRecap
	Err   error
}

// AgentReturnRecapMsg is an agent-activity reply for the return toast.
type AgentReturnRecapMsg struct {
	Return agentReturn
	Recap  *session.AgentActivityRecap
	Err    error
}

// recapSettings is [agents.recap] with its defaults applied.
func (m *OS) recapSettings() config.ResolvedRecap {
	if m.UserConfig == nil {
		return config.RecapConfig{}.Resolved()
	}
	return m.UserConfig.Agents.Recap.Resolved()
}

// agentActivityRecapCall reads a pane's recap since a time.
func agentActivityRecapCall(call inboxVerbCall, sessionID, windowID string, since int64) (*session.AgentActivityRecap, error) {
	params := map[string]any{"session": sessionID, "window": windowID, "recap": true, "limit": 1}
	if since > 0 {
		params["since"] = since
	}
	raw, err := call("agent-activity", params, inboxRecapTimeout)
	if err != nil {
		return nil, err
	}
	var res struct {
		Recap *session.AgentActivityRecap `json:"recap"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if res.Recap == nil {
		return nil, errors.New("the daemon sent no recap")
	}
	return res.Recap, nil
}

// inboxRecapFor reports whether the Inbox shows a recap for an item: a
// finished turn of a pane on this machine, with the recap not turned off.
func (m *OS) inboxRecapFor(it session.AttentionItem) bool {
	return it.Kind == session.AttentionFinished && it.Window != "" && it.Host == "" && !it.Stale &&
		m.recapSettings().Mode != config.RecapOff
}

// inboxRecapKey names the recap an item shows.
func (m *OS) inboxRecapKey(it session.AttentionItem) (string, int64) {
	since, _ := m.agentAwaySince(it.Window)
	return it.ID + ":" + strconv.FormatUint(it.Seq, 10) + ":" + strconv.FormatInt(since, 10), since
}

// InboxRecapFetch reads the recap of the selected finished item when the view
// does not hold it yet. It is called after every input and every Inbox
// delivery while the Inbox is up, and costs a comparison when there is
// nothing to read.
func (m *OS) InboxRecapFetch() tea.Cmd {
	if !m.ShowInbox || m.Inbox.Peek != nil {
		return nil
	}
	it, ok := m.inboxSelected()
	if !ok || !m.inboxRecapFor(it) {
		return nil
	}
	key, since := m.inboxRecapKey(it)
	v := &m.Inbox.recap
	if v.key == key {
		return nil
	}
	if m.DaemonClient == nil && m.Inbox.call == nil {
		return nil
	}
	pending := v.pending
	*v = inboxRecapView{key: key, loading: true, since: since, pending: pending}
	call, sessionID, windowID := m.inboxCaller(), it.Session, it.Window
	return func() tea.Msg {
		rc, err := agentActivityRecapCall(call, sessionID, windowID, since)
		return InboxRecapMsg{Key: key, Recap: rc, Err: err}
	}
}

// applyInboxRecap takes an agent-activity reply for the Inbox's detail.
func (m *OS) applyInboxRecap(msg InboxRecapMsg) {
	v := &m.Inbox.recap
	if v.key != msg.Key {
		return
	}
	v.loading, v.recap, v.err = false, msg.Recap, msg.Err
}

// inboxRecapDetail is the detail under the list for a finished item: what
// the agent did while the person was away, and the agent's model, context
// and cost when the pane reported them.
func (m *OS) inboxRecapDetail(it session.AttentionItem) (func(width int) []string, bool) {
	if !m.inboxRecapFor(it) {
		return nil, false
	}
	return func(width int) []string { return m.inboxRecapLines(it, width, time.Now()) }, true
}

// inboxRecapLines draws the recap for an item.
func (m *OS) inboxRecapLines(it session.AttentionItem, width int, now time.Time) []string {
	width = max(width-2, 8)
	var lines []string
	add := func(s string) {
		for _, l := range wrapPlain(s, width) {
			lines = append(lines, "  "+l)
		}
	}
	v := &m.Inbox.recap
	key, _ := m.inboxRecapKey(it)
	switch {
	case v.key != key || v.loading:
		add("Reading what " + inboxWho(it) + " did...")
	case v.err != nil || v.recap == nil || recapEmpty(v.recap):
		// A daemon without the ring, or a pane whose harness sends no
		// hooks: the turn's own line is all there is.
		if s := printableTitle(it.Summary); s != "" {
			add("Last said: " + s)
		}
	default:
		rc := v.recap
		if v.since > 0 {
			add("While you were away (" + inboxWait(v.since, now) + ")")
		} else if rc.Since > 0 {
			add("Since " + time.Unix(0, rc.Since).Format("15:04") + " (" + inboxWait(rc.Since, now) + " ago)")
		}
		line := recapCount(int(rc.Turns), "turn", "turns") + "."
		if rc.FilesTotal > 0 {
			line += " " + recapCount(rc.FilesTotal, "file", "files") + ": " + recapFiles(rc.Files, rc.FilesTotal, 2)
		}
		add(line)
		line = recapCount(rc.Commands, "command", "commands") + "."
		if t := rc.Tests; t != nil {
			line += " Tests: " + printableTitle(t.Cmdline) + " " + recapTestWord(t.OK) + " " + inboxWait(t.At, now) + " ago"
		}
		add(line)
		if rc.LastSaid != "" {
			add("Last said: " + printableTitle(rc.LastSaid))
		}
	}
	if facts := m.agentFactsLine(it.Session, it.Window, it.Harness); facts != "" {
		add(facts)
	}
	return lines
}

// recapEmpty reports whether a recap says nothing: no ring, or nothing in it
// since the time asked.
func recapEmpty(rc *session.AgentActivityRecap) bool {
	return rc.Turns == 0 && rc.FilesTotal == 0 && rc.Commands == 0 && rc.Tests == nil && rc.LastSaid == ""
}

// recapCount is n and the word for it: 1 turn, 3 turns.
func recapCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// recapFiles names the first shown files and says how many more.
func recapFiles(files []string, total, shown int) string {
	var names []string
	for _, f := range files {
		if len(names) == shown {
			break
		}
		names = append(names, printableTitle(f))
	}
	more := total - len(names)
	switch {
	case more > 0:
		return strings.Join(names, ", ") + " and " + strconv.Itoa(more) + " more"
	case len(names) > 1:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	return strings.Join(names, "")
}

// recapTestWord says how a test run went.
func recapTestWord(ok *bool) string {
	switch {
	case ok == nil:
		return "ran"
	case *ok:
		return "passed"
	}
	return "failed"
}

// recapTestName is a test command cut to what names it: "go test ./..." is
// "go test", "pytest -x tests/" is "pytest".
func recapTestName(cmdline string) string {
	words := strings.Fields(printableTitle(cmdline))
	var keep []string
	for _, w := range words {
		if len(keep) == 2 || strings.HasPrefix(w, "-") || strings.ContainsAny(w, "./=") {
			break
		}
		keep = append(keep, w)
	}
	if len(keep) == 0 && len(words) > 0 {
		keep = words[:1]
	}
	return strings.Join(keep, " ")
}

// agentPaneMeta is the metadata a pane of this machine reported, as this
// client holds it.
func (m *OS) agentPaneMeta(sessionID, windowID string) []sessiontree.MetaToken {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		for _, w := range m.Windows {
			if w != nil && w.ID == windowID {
				return w.AgentMeta
			}
		}
	}
	if m.DaemonClient == nil || m.AttachedHost != "" {
		return nil
	}
	for _, w := range m.DaemonClient.SessionWindows(sessionID) {
		if w.ID == windowID {
			return agentMetaFromWire(nil, w.AgentMeta)
		}
	}
	return nil
}

// agentFactsLine is the one line of facts about an agent the Inbox's detail
// and the peek show: "claude · opus 4.7 · 42% ctx · $1.20 · plan 3/7". A fact
// the harness did not state is left out with its separator, and a pane that
// stated none of the metadata gets no line, not a line that names only the
// harness.
func (m *OS) agentFactsLine(sessionID, windowID, harness string) string {
	meta := m.agentPaneMeta(sessionID, windowID)
	if len(meta) == 0 {
		return ""
	}
	var parts []string
	if model := printableTitle(sidebarAgentMetaValue(meta, "model")); model != "" {
		parts = append(parts, model)
	}
	if ctx := printableTitle(sidebarAgentMetaValue(meta, "context")); ctx != "" {
		if pct, ok := sidebarContextPercent(ctx); ok {
			ctx = strconv.Itoa(int(pct)) + "% ctx"
		}
		parts = append(parts, ctx)
	}
	if cost := printableTitle(sidebarAgentMetaValue(meta, "cost")); cost != "" {
		parts = append(parts, cost)
	}
	if plan := printableTitle(sidebarAgentMetaValue(meta, "plan")); plan != "" {
		parts = append(parts, "plan "+plan)
	}
	if len(parts) == 0 {
		return ""
	}
	if h := sidebarHarnessLabel(harness); h != "" {
		parts = append([]string{h}, parts...)
	}
	return strings.Join(parts, sepWord())
}

// noteAgentReturn is focus landing on a pane. When the person was away from
// it for at least [agents.recap] away and it finished a turn meanwhile, the
// dock is to say what it did, which AgentRecapFetch reads. It runs before the
// focus marks the pane seen, since those marks are what it measures from.
func (m *OS) noteAgentReturn(w *terminal.Window) {
	if w == nil || w.ID == "" || (w.AgentState == "" && w.AgentCompletionSeq == 0) {
		return
	}
	since, ok := m.agentAwaySince(w.ID)
	if !ok {
		return
	}
	seen := m.SidebarAgentSeenSeq[w.ID]
	if w.AgentCompletionSeq <= seen {
		return
	}
	rs := m.recapSettings()
	if rs.Mode != config.RecapToast {
		return
	}
	if time.Duration(agentSeenAtNow()-since) < rs.Away {
		return
	}
	who := printableTitle(m.railTitleShown(w))
	if who == "" {
		who = shortWindowLabel(w.ID)
	}
	m.Inbox.recap.pending = &agentReturn{
		session: m.sidebarCurrentSessionID(),
		window:  w.ID,
		who:     who,
		since:   since,
		turns:   w.AgentCompletionSeq - seen,
		state:   w.AgentState,
	}
}

// AgentRecapFetch reads the recap for a return noteAgentReturn recorded. It
// is called after every input and costs a nil check when there is none.
func (m *OS) AgentRecapFetch() tea.Cmd {
	ret := m.Inbox.recap.pending
	if ret == nil {
		return nil
	}
	m.Inbox.recap.pending = nil
	r := *ret
	if !m.IsDaemonSession || (m.Inbox.call == nil && (m.DaemonClient == nil || m.AttachedHost != "")) {
		// No ring to read here: the dock says what this client counted.
		m.showAgentReturn(r, nil)
		return nil
	}
	call := m.inboxCaller()
	return func() tea.Msg {
		rc, err := agentActivityRecapCall(call, r.session, r.window, r.since)
		return AgentReturnRecapMsg{Return: r, Recap: rc, Err: err}
	}
}

// applyAgentReturnRecap says the return in the dock. A recap that could not
// be read still says the turns this client counted.
func (m *OS) applyAgentReturnRecap(msg AgentReturnRecapMsg) {
	rc := msg.Recap
	if msg.Err != nil {
		rc = nil
	}
	m.showAgentReturn(msg.Return, rc)
}

// showAgentReturn puts the recap line in the dock.
func (m *OS) showAgentReturn(r agentReturn, rc *session.AgentActivityRecap) {
	m.ShowNotification(agentReturnLine(r, rc, time.Now()), "info", m.Settings.NotificationDuration*2)
}

// agentReturnLine is the dock's one line: "api while you were away (42m): 3
// turns, 6 files, 11 commands, go test passed, at prompt".
func agentReturnLine(r agentReturn, rc *session.AgentActivityRecap, now time.Time) string {
	turns := r.turns
	state := r.state
	var parts []string
	if rc != nil {
		turns = max(turns, rc.Turns)
		if rc.State != "" {
			state = rc.State
		}
	}
	parts = append(parts, recapCount(int(turns), "turn", "turns"))
	if rc != nil {
		if rc.FilesTotal > 0 {
			parts = append(parts, recapCount(rc.FilesTotal, "file", "files"))
		}
		if rc.Commands > 0 {
			parts = append(parts, recapCount(rc.Commands, "command", "commands"))
		}
		if t := rc.Tests; t != nil {
			if name := recapTestName(t.Cmdline); name != "" {
				parts = append(parts, name+" "+recapTestWord(t.OK))
			}
		}
	}
	if where := agentReturnState(state); where != "" {
		parts = append(parts, where)
	}
	return r.who + " while you were away (" + inboxWait(r.since, now) + "): " + strings.Join(parts, ", ")
}

// agentReturnState says where an agent is now, in the words the dock line
// ends with.
func agentReturnState(state string) string {
	switch state {
	case "idle", "done":
		return "at prompt"
	case "":
		return ""
	}
	return sidebarStateWords(state)
}
