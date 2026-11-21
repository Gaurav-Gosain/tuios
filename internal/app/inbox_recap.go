package app

import (
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const inboxRecapTimeout = 5 * time.Second

type inboxRecapView struct {
	key     string
	loading bool
	err     error
	recap   *session.AgentActivityRecap
	since   int64
	pending *agentReturn
}

type agentReturn struct {
	session, window, who string
	since                int64
	turns                uint64
	state                string
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

func (m *OS) recapSettings() config.ResolvedRecap {
	if m.UserConfig == nil {
		return config.RecapConfig{}.Resolved()
	}
	return m.UserConfig.Agents.Recap.Resolved()
}

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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if res.Recap == nil {
		return nil, errors.New("the daemon sent no recap")
	}
	return res.Recap, nil
}

func (m *OS) inboxRecapFor(it session.AttentionItem) bool {
	return it.Kind == session.AttentionFinished && it.Window != "" && it.Host == "" && !it.Stale &&
		m.recapSettings().Mode != config.RecapOff
}

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
	call, sessionID, windowID := m.inboxActCaller(), it.Session, it.Window
	return func() tea.Msg {
		rc, err := agentActivityRecapCall(call, sessionID, windowID, since)
		return InboxRecapMsg{Key: key, Recap: rc, Err: err}
	}
}

func (m *OS) applyInboxRecap(msg InboxRecapMsg) {
	m.cockpitTouch()
	v := &m.Inbox.recap
	if v.key != msg.Key {
		return
	}
	v.loading, v.recap, v.err = false, msg.Recap, msg.Err
}

func (m *OS) inboxRecapDetail(it session.AttentionItem) (func(width int) []string, bool) {
	if !m.inboxRecapFor(it) {
		return nil, false
	}
	return func(width int) []string { return m.inboxRecapLines(it, width, time.Now()) }, true
}

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
		if s := printableTitle(it.Summary); s != "" {
			add("Last said: " + s)
		}
	default:
		for _, s := range recapSentences(v.recap, v.since, now, v.recap.Folder) {
			add(s)
		}
	}
	if facts := m.agentFactsLine(it.Session, it.Window, it.Harness); facts != "" {
		add(facts)
	}
	if it.Host == "" {
		for _, l := range subagentDetailLines(m.paneSubagentList(it.Session, it.Window)) {
			add(l)
		}
	}
	return lines
}

func recapEmpty(rc *session.AgentActivityRecap) bool {
	return rc.Turns == 0 && rc.FilesTotal == 0 && rc.Commands == 0 && rc.Tests == nil && rc.LastSaid == ""
}

func recapCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func recapFiles(files []string, total, shown int, root string) string {
	var names []string
	for _, f := range files {
		if len(names) == shown {
			break
		}
		names = append(names, printableTitle(recapPath(f, root)))
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

func recapTestWord(ok *bool) string {
	switch {
	case ok == nil:
		return "ran"
	case *ok:
		return "passed"
	}
	return "failed"
}

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

func (m *OS) findPane(sessionID, windowID string) (*terminal.Window, *session.WindowSummary) {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		for _, w := range m.Windows {
			if w != nil && w.ID == windowID {
				return w, nil
			}
		}
	}
	if m.DaemonClient == nil || m.AttachedHost != "" {
		return nil, nil
	}
	sums := m.DaemonClient.SessionWindows(sessionID)
	for i := range sums {
		if sums[i].ID == windowID {
			return nil, &sums[i]
		}
	}
	return nil, nil
}

func (m *OS) agentPaneMeta(sessionID, windowID string) []sessiontree.MetaToken {
	w, sum := m.findPane(sessionID, windowID)
	switch {
	case w != nil:
		return w.AgentMeta
	case sum != nil:
		return agentMetaFromWire(nil, sum.AgentMeta)
	}
	return nil
}

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
		m.showAgentReturn(r, nil)
		return nil
	}
	call := m.inboxCaller()
	return func() tea.Msg {
		rc, err := agentActivityRecapCall(call, r.session, r.window, r.since)
		return AgentReturnRecapMsg{Return: r, Recap: rc, Err: err}
	}
}

func (m *OS) applyAgentReturnRecap(msg AgentReturnRecapMsg) {
	rc := msg.Recap
	if msg.Err != nil {
		rc = nil
	}
	m.showAgentReturn(msg.Return, rc)
}

func (m *OS) showAgentReturn(r agentReturn, rc *session.AgentActivityRecap) {
	m.ShowNotification(agentReturnLine(r, rc, time.Now()), "info", m.Settings.NotificationDuration*2)
}

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
			if name := recapTestName(transcriptview.CommandLabel(t.Cmdline)); name != "" {
				parts = append(parts, name+" "+recapTestWord(t.OK))
			}
		}
	}
	if where := agentReturnState(state); where != "" {
		parts = append(parts, where)
	}
	return r.who + " while you were away (" + units.Waited(r.since, now) + "): " + strings.Join(parts, ", ")
}

func agentReturnState(state string) string {
	switch state {
	case "idle", "done":
		return "at prompt"
	case "":
		return ""
	}
	return sidebarStateWords(state)
}

func recapSentences(rc *session.AgentActivityRecap, since int64, now time.Time, root string) []string {
	var out []string
	if since > 0 {
		out = append(out, "While you were away ("+units.Waited(since, now)+")")
	} else if rc.Since > 0 {
		out = append(out, "Since "+time.Unix(0, rc.Since).Format("15:04")+" ("+units.Waited(rc.Since, now)+" ago)")
	}
	line := recapCount(int(rc.Turns), "turn", "turns") + "."
	if rc.FilesTotal > 0 {
		line += " " + recapCount(rc.FilesTotal, "file", "files") + ": " + recapFiles(rc.Files, rc.FilesTotal, 2, root)
	}
	out = append(out, line)
	line = recapCount(rc.Commands, "command", "commands") + "."
	if t := rc.Tests; t != nil {
		line += " Tests: " + recapTestCommand(t.Cmdline) + " " + recapTestWord(t.OK) + " " + units.Waited(t.At, now) + " ago"
	}
	out = append(out, line)
	if rc.LastSaid != "" {
		out = append(out, "Last said: "+printableTitle(rc.LastSaid))
	}
	return out
}

func recapPath(f, root string) string {
	if root != "" && root != "/" {
		if rel, ok := strings.CutPrefix(f, strings.TrimSuffix(root, "/")+"/"); ok && rel != "" {
			return rel
		}
	}
	return ckHomePath(f)
}

func recapTestCommand(cmdline string) string {
	const most = 4
	var keep []string
	for _, w := range strings.Fields(printableTitle(transcriptview.CommandLabel(cmdline))) {
		if strings.ContainsAny(w, "|;&<>") {
			break
		}
		if strings.HasPrefix(w, "-") || len(keep) == 0 && strings.Contains(w, "=") {
			continue
		}
		if len(keep) == most {
			keep = append(keep, overlay.Ellipsis())
			break
		}
		if len(w) > 2 && strings.HasSuffix(w, "/") {
			w = strings.TrimSuffix(w, "/")
		}
		keep = append(keep, w)
	}
	if len(keep) == 0 {
		return printableTitle(transcriptview.CommandLabel(cmdline))
	}
	return strings.Join(keep, " ")
}
