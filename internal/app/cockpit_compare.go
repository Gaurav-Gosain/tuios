package app

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

const ckCompareTimeout = 30 * time.Second

const ckJudgeTimeout = 3 * time.Minute

type ckCompare struct {
	session            string
	gen                uint64
	loading            bool
	err                string
	res                *ckCompareResult
	focus              int
	focusOn            string
	armed              string
	armedGen           uint64
	confirm            bool
	keepSig            string
	warnScroll         int
	busy, note         string
	verdictAt, askedAt int64
}

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
	FileList     []worktree.FileStat `json:"file_list"`
	Lines        []worktree.DiffLine `json:"lines"`
	Notes        []string            `json:"notes"`
	Ignored      []string            `json:"ignored"`
	IgnoredCount int                 `json:"ignored_count"`
	IgnoredError string              `json:"ignored_error"`
	IgnoredHash  string              `json:"ignored_hash"`
}

type ckCompareMsg struct {
	gen uint64
	res *ckCompareResult
	err error
}

type ckFanActMsg struct {
	gen        uint64
	verb, text string
	err        error
}

// CockpitCompareOpen reports whether the comparison is up.
func (m *OS) CockpitCompareOpen() bool { return m.ShowInbox && m.Inbox.ck.cmp != nil }

func (m *OS) ckFanSession() string {
	e, ok := m.cockpitAgent()
	if !ok || e.Host != "" || m.ckWorktreeOf(e).group == "" {
		return ""
	}
	return m.ckAgentSession(e)
}

// CockpitCompare is c: it opens the comparison of the fan under the cursor.
func (m *OS) CockpitCompare() tea.Cmd {
	s := m.ckFanSession()
	if s == "" {
		m.ShowNotification("Select an attempt of a fan to compare its attempts.", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.Inbox.ck.cmp = &ckCompare{session: s, focusOn: s}
	m.cockpitTouch()
	return m.ckCompareFetch()
}

// CockpitCompareClose goes back to the agent's focus.
func (m *OS) CockpitCompareClose() {
	m.Inbox.ck.cmp = nil
	m.cockpitTouch()
}

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
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return ckCompareMsg{gen: gen, err: err}
		}
		return ckCompareMsg{gen: gen, res: &res}
	}
}

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
	cp.confirm = false
	cp.armedGen = cp.gen
	if a, ok := cp.ckFocused(); !ok || a.Session != cp.armed || ckIgnoredFailed(msg.res.Rows, cp.focus) != "" {
		return nil
	}
	if sig := ckKeepSig(msg.res.Rows, cp.focus); sig != cp.keepSig {
		cp.note = "The attempts changed since the list was read. Read the list again, then press K to keep."
		cp.keepSig, cp.warnScroll = sig, 0
		return nil
	}
	return m.ckKeepNow()
}

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

func ckIgnoredFailed(rows []ckFanAttempt, keep int) string {
	var parts []string
	for i, r := range rows {
		if i != keep && r.IgnoredError != "" {
			parts = append(parts, ckLetter(i)+": "+printableTitle(r.IgnoredError))
		}
	}
	return strings.Join(parts, sepWord())
}

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
	motion := m.ListKeys.Keys(key, false)
	switch {
	case key == "K", key == "up", key == "down", key == "j", key == "k", motion == listnav.PageUp, motion == listnav.PageDown:
	default:
		cp.armed = ""
	}
	switch motion {
	case listnav.PageUp:
		cp.warnScroll = max(cp.warnScroll-listOverlayRows, 0)
		return nil
	case listnav.PageDown:
		cp.warnScroll += listOverlayRows
		return nil
	}
	switch key {
	case "esc", "q", "backspace":
		m.CockpitCompareClose()
	case "down", "j":
		cp.warnScroll++
	case "up", "k":
		cp.warnScroll = max(cp.warnScroll-1, 0)
	case "left", "h", "shift+tab":
		cp.focus = max(cp.focus-1, 0)
	case "right", "l", "tab":
		cp.focus = min(cp.focus+1, max(n-1, 0))
	case "r":
		return m.ckCompareFetch()
	case "J":
		return m.ckJudge()
	case "K":
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

func (cp *ckCompare) ckFocused() (ckFanAttempt, bool) {
	if cp.res == nil || cp.focus >= len(cp.res.Rows) {
		return ckFanAttempt{}, false
	}
	return cp.res.Rows[cp.focus], true
}

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
		raw, err := call("judge-fan", map[string]any{"session": sess}, ckJudgeTimeout)
		if err != nil {
			return ckFanActMsg{gen: gen, verb: "judge", err: err}
		}
		var res struct {
			JudgeSession string `json:"judge_session"`
			Ready        bool   `json:"ready"`
			Reason       string `json:"reason"`
		}
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return ckFanActMsg{gen: gen, verb: "judge", err: errfmt.Errorf("read the judge-fan answer: %w", err)}
		}
		text := "A judge reads the attempts in " + res.JudgeSession + ". Its verdict shows here when it reports."
		if !res.Ready && res.Reason != "" {
			text = "The judge in " + res.JudgeSession + " is not ready: " + res.Reason
		}
		return ckFanActMsg{gen: gen, verb: "judge", text: text}
	}
}

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
		cp.armed, cp.confirm, cp.warnScroll, cp.note = a.Session, false, 0, ""
		cmd := m.ckCompareFetch()
		cp.armedGen = cp.gen
		return cmd
	}
	if cp.loading || cp.confirm || cp.armedGen != cp.gen {
		return nil
	}
	if ckIgnoredFailed(cp.res.Rows, cp.focus) != "" {
		cmd := m.ckCompareFetch()
		cp.armedGen = cp.gen
		return cmd
	}
	cp.keepSig, cp.confirm, cp.note = ckKeepSig(cp.res.Rows, cp.focus), true, ""
	return m.ckCompareFetch()
}

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
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return ckFanActMsg{gen: gen, verb: "keep", err: errfmt.Errorf("read the keep answer: %w", err)}
		}
		n := len(res.Removed) - res.Left
		text := "Kept " + letter + ", " + kept + ". Removed " + strconv.Itoa(n) + " other " + map[bool]string{true: "attempt", false: "attempts"}[n == 1] + ". Their branches stay."
		if res.Left > 0 {
			text += " " + strconv.Itoa(res.Left) + " could not be removed: tuios worktree ls shows them."
		}
		return ckFanActMsg{gen: gen, verb: "keep", text: text}
	}
}

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
		if session.VerbErrCode(msg.err) == session.ErrVerbUnknownVerb {
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

func (m *OS) ckCompareGo() tea.Cmd {
	cp := m.Inbox.ck.cmp
	a, ok := cp.ckFocused()
	if !ok {
		return nil
	}
	for i, r := range m.cockpitRows() {
		if !r.selectable() || r.agent < 0 || r.agent >= len(m.Inbox.ck.agents) {
			continue
		}
		if e := m.Inbox.ck.agents[r.agent]; e.Host == "" && m.ckAgentSession(e) == a.Session {
			m.cockpitSetCursor(i)
			m.cockpitTouch()
			m.cockpitSync()
			m.CockpitCompareClose()
			return m.CockpitGo()
		}
	}
	cp.note = "The pane of " + a.Session + " is not in the list."
	return nil
}
