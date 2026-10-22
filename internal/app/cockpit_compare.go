package app

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// The fan comparison of the agents view (DESIGN.md 3.4, frame g): every
// attempt of a fan side by side, with the judge's verdict on top. c on an
// attempt opens it, j asks an agent to judge, k twice keeps one attempt and
// removes the others with their changes stashed, and esc goes back.
//
// Cost: compare-fan runs when the view opens and after j and r, never on a
// timer. It reads git in each worktree under the daemon's own timeout. The
// verdict arrives with the session listing the view already follows, and a
// new one reads the comparison again.

// ckCompareTimeout bounds compare-fan, which runs git in every attempt.
const ckCompareTimeout = 30 * time.Second

// ckJudgeTimeout bounds judge-fan, which waits for the judge to be ready.
const ckJudgeTimeout = 3 * time.Minute

// ckCompare is the open comparison.
type ckCompare struct {
	// session is the fan session it was opened on, and gen counts its reads.
	session string
	gen     uint64
	loading bool
	err     string
	res     *ckCompareResult
	// focus is the attempt the keys act on, and focusOn the session it goes
	// to once the attempts are read.
	focus   int
	focusOn string
	// armed is the attempt k was pressed on once, "" when none. The first
	// k reads the attempts again, so the files a keep deletes are listed as
	// they are now; armedGen is that read, and a second k waits for it.
	armed    string
	armedGen uint64
	// confirm says the second k asked for one more read: the keep runs
	// when it shows what the warning showed, keepSig. warnScroll is the
	// first line of the warning drawn.
	confirm    bool
	keepSig    string
	warnScroll int
	// busy says what runs now ("judge", "keep"), and note what last ended.
	busy, note string
	// verdictAt is the verdict the result holds. askedAt is the newest
	// verdict the session listing showed that a read was asked for, so each
	// new verdict is read once, never in a loop.
	verdictAt, askedAt int64
}

// ckCompareResult is compare-fan's answer.
type ckCompareResult struct {
	Group    string         `json:"group"`
	FanID    string         `json:"fan_id"`
	Repo     string         `json:"repo"`
	RepoRoot string         `json:"repo_root"`
	Base     string         `json:"base"`
	Prompt   string         `json:"prompt"`
	Rows     []ckFanAttempt `json:"rows"`
	Verdict  *struct {
		Pick    string   `json:"pick"`
		Summary string   `json:"summary"`
		Reasons []string `json:"reasons"`
		By      string   `json:"by"`
		Judge   string   `json:"judge"`
		At      int64    `json:"at"`
	} `json:"verdict"`
}

// ckFanAttempt is one row of compare-fan.
type ckFanAttempt struct {
	Session   string `json:"session"`
	Branch    string `json:"branch"`
	Agent     string `json:"agent"`
	Harness   string `json:"harness"`
	State     string `json:"state"`
	Files     *int   `json:"files"`
	Added     *int   `json:"added"`
	Removed   *int   `json:"removed"`
	Gone      bool   `json:"gone"`
	Note      string `json:"note"`
	Cost      string `json:"cost"`
	StartedAt int64  `json:"started_at"`
	DoneAt    int64  `json:"done_at"`
	Verify    *struct {
		Command string `json:"command"`
		State   string `json:"state"`
	} `json:"verify"`
	FileList []worktree.FileStat `json:"file_list"`
	Lines    []worktree.DiffLine `json:"lines"`
	Notes    []string            `json:"notes"`
	// Ignored are files git ignores in the attempt's worktree, which a keep
	// deletes with it, IgnoredCount how many there are in all, and
	// IgnoredError why they could not be listed.
	Ignored      []string `json:"ignored"`
	IgnoredCount int      `json:"ignored_count"`
	IgnoredError string   `json:"ignored_error"`
	IgnoredHash  string   `json:"ignored_hash"`
}

// ckCompareMsg is compare-fan's answer for read gen.
type ckCompareMsg struct {
	gen uint64
	res *ckCompareResult
	err error
}

// ckFanActMsg is the end of a judge-fan or keep-fan call.
type ckFanActMsg struct {
	gen        uint64
	verb, text string
	err        error
}

// CockpitCompareOpen reports whether the comparison is up.
func (m *OS) CockpitCompareOpen() bool { return m.ShowInbox && m.Inbox.ck.cmp != nil }

// ckFanSession is the session of the fan the selected row belongs to, "" when
// it is not an attempt of a fan on this machine.
func (m *OS) ckFanSession() string {
	e, ok := m.cockpitAgent()
	if !ok || e.Host != "" || m.ckWorktreeOf(e).group == "" {
		return ""
	}
	return e.SessionID
}

// CockpitCompare is c: it opens the comparison of the fan under the cursor.
func (m *OS) CockpitCompare() tea.Cmd {
	s := m.ckFanSession()
	if s == "" {
		m.ShowNotification("Select an attempt of a fan to compare its attempts.", "info", m.Settings.NotificationDuration)
		return nil
	}
	// The attempt under the cursor is where the keys start.
	m.Inbox.ck.cmp = &ckCompare{session: s, focusOn: s}
	m.cockpitTouch()
	return m.ckCompareFetch()
}

// CockpitCompareClose goes back to the agent's focus.
func (m *OS) CockpitCompareClose() {
	m.Inbox.ck.cmp = nil
	m.cockpitTouch()
}

// ckCompareFetch reads the comparison again.
func (m *OS) ckCompareFetch() tea.Cmd {
	cp := m.Inbox.ck.cmp
	if cp == nil || (m.DaemonClient == nil && m.Inbox.call == nil) {
		return nil
	}
	cp.gen++
	cp.loading = true
	gen, sess, call := cp.gen, cp.session, m.inboxCaller()
	return func() tea.Msg {
		raw, err := call("compare-fan", map[string]any{"session": sess, "detail": true}, ckCompareTimeout)
		if err != nil {
			return ckCompareMsg{gen: gen, err: err}
		}
		var res ckCompareResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return ckCompareMsg{gen: gen, err: err}
		}
		return ckCompareMsg{gen: gen, res: &res}
	}
}

// applyCompare takes compare-fan's answer.
func (m *OS) applyCompare(msg ckCompareMsg) tea.Cmd {
	cp := m.Inbox.ck.cmp
	if cp == nil || msg.gen != cp.gen {
		return nil
	}
	cp.loading = false
	if msg.err != nil {
		cp.err = "tuios cannot compare the attempts now: " + printableTitle(msg.err.Error())
		cp.confirm = false
		m.cockpitTouch()
		return nil
	}
	cp.err, cp.res = "", msg.res
	cp.focus = min(cp.focus, max(len(msg.res.Rows)-1, 0))
	if cp.focusOn != "" {
		for i, r := range msg.res.Rows {
			if r.Session == cp.focusOn {
				cp.focus = i
			}
		}
		cp.focusOn = ""
	}
	if v := msg.res.Verdict; v != nil && v.At != cp.verdictAt {
		cp.verdictAt, cp.note = v.At, ""
		// The verdict's pick is where the keys start.
		for i, r := range msg.res.Rows {
			if r.Session == v.Pick && cp.armed == "" {
				cp.focus = i
			}
		}
	}
	m.cockpitTouch()
	if !cp.confirm {
		return nil
	}
	// The read the second k asked for: keep only what was shown.
	cp.confirm = false
	cp.armedGen = cp.gen
	if a, ok := cp.ckFocused(); !ok || a.Session != cp.armed || ckIgnoredFailed(msg.res.Rows, cp.focus) != "" {
		return nil
	}
	if sig := ckKeepSig(msg.res.Rows, cp.focus); sig != cp.keepSig {
		cp.note = "The attempts changed since the list was read. Read the list again, then press k to keep."
		cp.keepSig, cp.warnScroll = sig, 0
		return nil
	}
	return m.ckKeepNow()
}

// ckKeepSig is what a keep of attempt keep removes, as a read showed it:
// the other attempts and the ignored files in each.
func ckKeepSig(rows []ckFanAttempt, keep int) string {
	var b strings.Builder
	for i, r := range rows {
		if i == keep {
			continue
		}
		b.WriteString(r.Session)
		b.WriteString("\x00")
		b.WriteString(r.IgnoredHash)
		b.WriteString("\x00")
	}
	return b.String()
}

// ckIgnoredFailed names the attempts whose ignored files could not be
// listed, with why, "" when every list was read. A keep waits for a list.
func ckIgnoredFailed(rows []ckFanAttempt, keep int) string {
	var parts []string
	for i, r := range rows {
		if i != keep && r.IgnoredError != "" {
			parts = append(parts, ckLetter(i)+": "+printableTitle(r.IgnoredError))
		}
	}
	return strings.Join(parts, sepWord())
}

// cockpitCompareStale reads the comparison again when the listing carries a
// verdict the view does not show yet. Update asks it after every listing.
func (m *OS) cockpitCompareStale() tea.Cmd {
	cp := m.Inbox.ck.cmp
	if cp == nil || cp.loading || cp.res == nil {
		return nil
	}
	newest := int64(0)
	for _, w := range m.Inbox.ck.branches {
		if w.group == cp.res.Group && w.fanID == cp.res.FanID && w.repo == cp.res.RepoRoot {
			newest = max(newest, w.verdictAt)
		}
	}
	if newest > cp.askedAt && newest > cp.verdictAt {
		cp.askedAt = newest
		return m.ckCompareFetch()
	}
	return nil
}

// ckLetter is an attempt's letter: A, B, C.
func ckLetter(i int) string { return string(rune('A' + i%26)) }

// CockpitCompareKey takes a key while the comparison is up.
func (m *OS) CockpitCompareKey(key string) tea.Cmd {
	cp := m.Inbox.ck.cmp
	if cp == nil {
		return nil
	}
	m.cockpitTouch()
	n := 0
	if cp.res != nil {
		n = len(cp.res.Rows)
	}
	switch key {
	case "k", "up", "down":
	default:
		cp.armed = ""
	}
	switch key {
	case "esc", "q", "backspace":
		m.CockpitCompareClose()
	case "down":
		cp.warnScroll++
	case "up":
		cp.warnScroll = max(cp.warnScroll-1, 0)
	case "left", "h", "shift+tab":
		cp.focus = max(cp.focus-1, 0)
	case "right", "l", "tab":
		cp.focus = min(cp.focus+1, max(n-1, 0))
	case "r":
		return m.ckCompareFetch()
	case "j":
		return m.ckJudge()
	case "k":
		return m.ckKeep()
	case "enter":
		return m.ckCompareGo()
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			if i := int(key[0] - '1'); i < n {
				cp.focus = i
			}
		}
	}
	return nil
}

// ckCompareAction runs a button of the comparison.
func (m *OS) ckCompareAction(action string) tea.Cmd {
	cp := m.Inbox.ck.cmp
	if cp == nil {
		return nil
	}
	switch {
	case action == "cmp:keep":
		return m.ckKeep()
	case action == "cmp:judge":
		cp.armed = ""
		return m.ckJudge()
	case action == "cmp:back":
		m.CockpitCompareClose()
	case strings.HasPrefix(action, "cmp:focus:"):
		i, _ := strconv.Atoi(strings.TrimPrefix(action, "cmp:focus:"))
		if cp.focus != i {
			cp.armed = ""
		}
		cp.focus = i
		m.cockpitTouch()
	}
	return nil
}

// ckFocused is the attempt the keys act on.
func (cp *ckCompare) ckFocused() (ckFanAttempt, bool) {
	if cp.res == nil || cp.focus >= len(cp.res.Rows) {
		return ckFanAttempt{}, false
	}
	return cp.res.Rows[cp.focus], true
}

// ckJudge is j: it starts an agent that judges the attempts.
func (m *OS) ckJudge() tea.Cmd {
	cp := m.Inbox.ck.cmp
	if cp == nil || cp.busy != "" || cp.res == nil {
		return nil
	}
	if len(cp.res.Rows) < 2 {
		cp.note = "There is one attempt left, so there is nothing to judge."
		return nil
	}
	cp.busy, cp.note = "judge", ""
	gen, sess, call := cp.gen, cp.session, m.inboxCaller()
	return func() tea.Msg {
		// The daemon finds the judge's program on its own PATH, as fan
		// does: this client may be on another machine.
		raw, err := call("judge-fan", map[string]any{"session": sess}, ckJudgeTimeout)
		if err != nil {
			return ckFanActMsg{gen: gen, verb: "judge", err: err}
		}
		var res struct {
			JudgeSession string `json:"judge_session"`
			Ready        bool   `json:"ready"`
			Reason       string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &res)
		text := "A judge reads the attempts in " + res.JudgeSession + ". Its verdict shows here when it reports."
		if !res.Ready && res.Reason != "" {
			text = "The judge in " + res.JudgeSession + " is not ready: " + res.Reason
		}
		return ckFanActMsg{gen: gen, verb: "judge", text: text}
	}
}

// ckKeep is k: the first press says what a keep does, the second keeps the
// focused attempt and removes the others, their changes stashed.
func (m *OS) ckKeep() tea.Cmd {
	cp := m.Inbox.ck.cmp
	if cp == nil || cp.busy != "" {
		return nil
	}
	a, ok := cp.ckFocused()
	if !ok {
		return nil
	}
	if cp.armed != a.Session {
		// The first k reads the attempts again: the warning lists the
		// files a keep deletes as they are now.
		cp.armed, cp.confirm, cp.warnScroll, cp.note = a.Session, false, 0, ""
		cmd := m.ckCompareFetch()
		cp.armedGen = cp.gen
		return cmd
	}
	if cp.loading || cp.confirm || cp.armedGen != cp.gen {
		// The second k waits for that read.
		return nil
	}
	if ckIgnoredFailed(cp.res.Rows, cp.focus) != "" {
		// No keep on a list that could not be read: the warning says why,
		// and k reads the list again.
		cmd := m.ckCompareFetch()
		cp.armedGen = cp.gen
		return cmd
	}
	// The second k reads once more, and keeps only when nothing changed.
	cp.keepSig, cp.confirm, cp.note = ckKeepSig(cp.res.Rows, cp.focus), true, ""
	return m.ckCompareFetch()
}

// ckKeepNow keeps the focused attempt and removes the others, as the last
// read showed them.
func (m *OS) ckKeepNow() tea.Cmd {
	cp := m.Inbox.ck.cmp
	a, ok := cp.ckFocused()
	if !ok {
		return nil
	}
	var siblings []string
	ignored := map[string]string{}
	for i, r := range cp.res.Rows {
		if i != cp.focus {
			siblings = append(siblings, r.Session)
			ignored[r.Session] = r.IgnoredHash
		}
	}
	cp.armed, cp.busy, cp.note = "", "keep", ""
	gen, call, kept := cp.gen, m.inboxCaller(), a.Session
	group, root, fanID := cp.res.Group, cp.res.RepoRoot, cp.res.FanID
	letter := ckLetter(cp.focus)
	return func() tea.Msg {
		// The fan is named too, so a session renamed since the read is not
		// kept in its place.
		raw, err := call("keep-fan", map[string]any{"session": kept, "stash": true, "group": group, "repo_root": root, "fan_id": fanID,
			"siblings": siblings, "ignored": ignored}, ckCompareTimeout)
		if err != nil {
			return ckFanActMsg{gen: gen, verb: "keep", err: err}
		}
		var res struct {
			Removed []struct {
				Removed bool `json:"removed"`
			} `json:"removed"`
			Left int `json:"left"`
		}
		_ = json.Unmarshal(raw, &res)
		n := len(res.Removed) - res.Left
		text := "Kept " + letter + ", " + kept + ". Removed " + strconv.Itoa(n) + " other " + map[bool]string{true: "attempt", false: "attempts"}[n == 1] + ". Their branches stay."
		if res.Left > 0 {
			text += " " + strconv.Itoa(res.Left) + " could not be removed: tuios worktree ls shows them."
		}
		return ckFanActMsg{gen: gen, verb: "keep", text: text}
	}
}

// applyFanAct takes the end of a judge or a keep.
func (m *OS) applyFanAct(msg ckFanActMsg) tea.Cmd {
	cp := m.Inbox.ck.cmp
	if cp == nil || msg.gen != cp.gen {
		if msg.verb == "keep" && msg.err == nil {
			m.ShowNotification(msg.text, "info", m.Settings.NotificationDuration)
		}
		return nil
	}
	cp.busy = ""
	m.cockpitTouch()
	if msg.err != nil {
		what := "start a judge"
		if msg.verb == "keep" {
			what = "keep the attempt"
		}
		text := printableTitle(msg.err.Error())
		if verr, ok := errors.AsType[*session.VerbCallError](msg.err); ok && verr.Code == session.ErrVerbUnknownVerb {
			text = "this daemon is older than the view. " + m.restartHint()
		}
		cp.note = "tuios could not " + what + ": " + text
		return nil
	}
	if msg.verb == "keep" {
		m.CockpitCompareClose()
		m.ShowNotification(msg.text, "info", m.Settings.NotificationDuration)
		return nil
	}
	cp.note = msg.text
	return m.ckCompareFetch()
}

// ckCompareGo is enter: the focused attempt's pane.
func (m *OS) ckCompareGo() tea.Cmd {
	cp := m.Inbox.ck.cmp
	a, ok := cp.ckFocused()
	if !ok {
		return nil
	}
	for _, e := range m.Inbox.ck.agents {
		if e.Host == "" && e.SessionID == a.Session {
			if m.cockpitSelectKey("a:" + agentKey(e)) {
				m.CockpitCompareClose()
				return m.CockpitGo()
			}
		}
	}
	cp.note = "The pane of " + a.Session + " is not in the list."
	return nil
}
