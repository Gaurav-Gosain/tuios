package app

import (
	"encoding/json"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// The compare view: the attempts of the reviewed pane's fan side by side,
// reached with w from the review of one of them.
//
// Each row is one attempt: its agent and state, what it changed against the
// fan's base, and the last check. enter reviews an attempt, m marks one and d
// diffs the two marked, V runs one command in every attempt (verify-fan, in a
// window with no grants), and K keeps the attempt under the cursor and
// removes the others (keep-fan) after a confirmation that names them. esc goes
// back to the review the view was opened from.
//
// The rows are read with compare-fan when the view opens. While a check runs
// they are read again every second, without counting, until none runs; the
// view costs nothing otherwise.

// ReviewCompare opens the compare view, for a pane in a fan.
func (m *OS) ReviewCompare() tea.Cmd {
	r := &m.review
	if r.fan == nil {
		m.ShowNotification(r.who+" is not part of a fan, so there are no attempts to compare", "info", m.Settings.NotificationDuration)
		return nil
	}
	if r.compare == nil {
		r.compare = &reviewCompare{group: r.fan.Group, repo: r.fan.Repo, base: r.fan.Base, rows: slices.Clone(r.fan.Rows)}
	}
	c := r.compare
	c.shown, c.confirmKeep, c.from, c.fromWho = true, "", r.query, r.who
	r.backToCompare = false
	r.editor = nil
	if r.diff != nil {
		if i := slices.IndexFunc(c.rows, func(row reviewFanRow) bool { return row.Session == r.diff.Session }); i >= 0 {
			c.cursor = i
		}
	}
	return m.reviewCompareCmd(true, 0)
}

// reviewCompareCmd reads the fan's rows, with changes counted or not.
func (m *OS) reviewCompareCmd(changes bool, tickGen uint64) tea.Cmd {
	r := &m.review
	if r.compare == nil || len(r.compare.rows) == 0 && r.fan == nil {
		return nil
	}
	name := ""
	if len(r.compare.rows) > 0 {
		name = r.compare.rows[0].Session
	} else {
		name = r.fan.Rows[0].Session
	}
	if changes {
		r.compare.loading = true
	}
	call := m.inboxCaller()
	gen := r.gen
	return func() tea.Msg {
		msg := ReviewCompareMsg{Gen: gen, TickGen: tickGen, Changes: changes}
		timeout := reviewVerbTimeout
		if changes {
			timeout = reviewFanTimeout
		}
		raw, err := call("compare-fan", map[string]any{"session": name, "changes": changes}, timeout)
		if err != nil {
			msg.Err = err
			return msg
		}
		var fan reviewFanResult
		if err := json.Unmarshal(raw, &fan); err != nil {
			msg.Err = err
			return msg
		}
		msg.Fan = &fan
		return msg
	}
}

// applyReviewCompare takes the rows compare-fan answered with. A read without
// counts keeps the counts the rows already had.
func (m *OS) applyReviewCompare(msg ReviewCompareMsg) tea.Cmd {
	r := &m.review
	c := r.compare
	if msg.Gen != r.gen || c == nil {
		return nil
	}
	if msg.Changes {
		c.loading = false
	}
	if msg.Err != nil {
		if msg.Changes {
			m.ShowNotification("The attempts could not be read: "+reviewErrorText(msg.Err), "error", m.Settings.NotificationDuration*2)
		}
		return nil
	}
	selected := ""
	if c.cursor >= 0 && c.cursor < len(c.rows) {
		selected = c.rows[c.cursor].Session
	}
	rows := msg.Fan.Rows
	if !msg.Changes {
		for i := range rows {
			if j := slices.IndexFunc(c.rows, func(old reviewFanRow) bool { return old.Session == rows[i].Session }); j >= 0 {
				old := c.rows[j]
				rows[i].Files, rows[i].Added, rows[i].Removed, rows[i].Dirty = old.Files, old.Added, old.Removed, old.Dirty
			}
		}
	}
	c.rows = rows
	c.group, c.repo, c.base = msg.Fan.Group, msg.Fan.Repo, msg.Fan.Base
	c.cursor = max(slices.IndexFunc(c.rows, func(row reviewFanRow) bool { return row.Session == selected }), 0)
	c.marks = slices.DeleteFunc(c.marks, func(s string) bool {
		return !slices.ContainsFunc(c.rows, func(row reviewFanRow) bool { return row.Session == s })
	})
	if r.fan != nil {
		r.fan.Rows = slices.Clone(c.rows)
	}
	if msg.TickGen != 0 && msg.TickGen != c.tickGen {
		return nil
	}
	return m.reviewTickIfRunning()
}

// reviewTickIfRunning schedules the next read while a check runs in an
// attempt and the view is open. Nothing is scheduled otherwise.
func (m *OS) reviewTickIfRunning() tea.Cmd {
	r := &m.review
	c := r.compare
	if c == nil || !r.open || !slices.ContainsFunc(c.rows, func(row reviewFanRow) bool {
		return row.Verify != nil && row.Verify.State == session.VerifyRunning
	}) {
		return nil
	}
	gen, tickGen := r.gen, c.tickGen
	return tea.Tick(reviewTickEvery, func(time.Time) tea.Msg { return ReviewTickMsg{Gen: gen, TickGen: tickGen} })
}

// applyReviewTick reads the rows again for a tick of the current chain.
func (m *OS) applyReviewTick(msg ReviewTickMsg) tea.Cmd {
	r := &m.review
	if msg.Gen != r.gen || r.compare == nil || msg.TickGen != r.compare.tickGen || !r.open {
		return nil
	}
	return m.reviewCompareCmd(false, msg.TickGen)
}

// ReviewCompareMove moves the compare view's cursor.
func (m *OS) ReviewCompareMove(delta int) {
	c := m.review.compare
	if c == nil || len(c.rows) == 0 {
		return
	}
	c.cursor = min(max(c.cursor+delta, 0), len(c.rows)-1)
}

// reviewCompareSelected is the attempt under the cursor.
func (m *OS) reviewCompareSelected() (reviewFanRow, bool) {
	c := m.review.compare
	if c == nil || c.cursor < 0 || c.cursor >= len(c.rows) {
		return reviewFanRow{}, false
	}
	return c.rows[c.cursor], true
}

// ReviewCompareBack leaves the compare view for the review it was opened
// from.
func (m *OS) ReviewCompareBack() tea.Cmd {
	r := &m.review
	c := r.compare
	if c == nil {
		return nil
	}
	c.shown, c.confirmKeep = false, ""
	r.editor = nil
	if r.query != c.from {
		r.query, r.who = c.from, c.fromWho
		r.clearDiff()
		return m.reviewReload()
	}
	return nil
}

// reviewAgentWindow is the pane of a session the agent runs in, as far as
// this client knows: the first with an agent in it. Empty lets the daemon
// take the session's focused pane.
func (m *OS) reviewAgentWindow(sessionName string) string {
	if sessionName == m.SessionName {
		for _, w := range m.Windows {
			if w != nil && (w.AgentHarness != "" || w.AgentState != "") {
				return w.ID
			}
		}
		return ""
	}
	if m.DaemonClient == nil {
		return ""
	}
	for _, w := range m.DaemonClient.SessionWindows(sessionName) {
		if w.AgentHarness != "" || w.AgentState != "" {
			return w.ID
		}
	}
	return ""
}

// ReviewCompareOpen reviews the attempt under the cursor. esc comes back.
func (m *OS) ReviewCompareOpen() tea.Cmd {
	row, ok := m.reviewCompareSelected()
	if !ok || row.Gone {
		return nil
	}
	return m.reviewFromCompare(reviewQuery{Session: row.Session, Window: m.reviewAgentWindow(row.Session)}, row.Session)
}

// reviewFromCompare shows a review asked for in the compare view.
func (m *OS) reviewFromCompare(q reviewQuery, who string) tea.Cmd {
	r := &m.review
	r.compare.shown = false
	r.backToCompare = true
	r.who = who
	r.query = q
	r.clearDiff()
	return m.reviewReload()
}

// ReviewCompareMark marks or unmarks the attempt under the cursor. Two marks
// enable d; a third replaces the older.
func (m *OS) ReviewCompareMark() {
	c := m.review.compare
	row, ok := m.reviewCompareSelected()
	if !ok {
		return
	}
	if i := slices.Index(c.marks, row.Session); i >= 0 {
		c.marks = slices.Delete(c.marks, i, i+1)
		return
	}
	c.marks = append(c.marks, row.Session)
	if len(c.marks) > 2 {
		c.marks = c.marks[len(c.marks)-2:]
	}
}

// ReviewCompareDiff diffs the two marked attempts with each other.
func (m *OS) ReviewCompareDiff() tea.Cmd {
	c := m.review.compare
	if c == nil || len(c.marks) != 2 {
		m.ShowNotification("Mark two attempts with m, then d diffs them", "info", m.Settings.NotificationDuration)
		return nil
	}
	a, b := c.marks[0], c.marks[1]
	return m.reviewFromCompare(reviewQuery{Session: a, Window: m.reviewAgentWindow(a), Against: b}, a)
}

// ReviewCompareVerifyPrompt opens the verify line, filled with the last
// command checked in any attempt.
func (m *OS) ReviewCompareVerifyPrompt() {
	c := m.review.compare
	if c == nil {
		return
	}
	last, at := "", int64(0)
	for _, row := range c.rows {
		if row.Verify != nil && row.Verify.StartedAt >= at && row.Verify.Command != "" {
			last, at = row.Verify.Command, row.Verify.StartedAt
		}
	}
	m.review.editor = &reviewEditor{kind: reviewEditVerify, draft: last, automated: m.ProcessingRemoteKeys, hunkIdx: -1, lineIdx: -1}
}

// reviewVerifyCmd runs a command in every attempt with verify-fan.
func (m *OS) reviewVerifyCmd(command string) tea.Cmd {
	r := &m.review
	c := r.compare
	row, ok := m.reviewCompareSelected()
	if c == nil || !ok {
		return nil
	}
	call := m.inboxCaller()
	gen := r.gen
	name := row.Session
	return func() tea.Msg {
		msg := ReviewVerifyMsg{Gen: gen, Command: command}
		raw, err := call("verify-fan", map[string]any{"session": name, "command": command}, reviewVerbTimeout)
		if err != nil {
			msg.Err = err
			return msg
		}
		var res struct {
			Sessions []string `json:"sessions"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			msg.Err = err
			return msg
		}
		msg.Sessions = res.Sessions
		return msg
	}
}

// applyReviewVerify marks the attempts a check started in and starts reading
// them until it ends.
func (m *OS) applyReviewVerify(msg ReviewVerifyMsg) tea.Cmd {
	r := &m.review
	if msg.Err != nil {
		m.ShowNotification("The check did not start: "+reviewErrorText(msg.Err), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	m.ShowNotification("Checking "+reviewCount(len(msg.Sessions), "attempt")+": "+msg.Command, "info", m.Settings.NotificationDuration)
	c := r.compare
	if msg.Gen != r.gen || c == nil {
		return nil
	}
	now := time.Now().UnixNano()
	for i := range c.rows {
		if slices.Contains(msg.Sessions, c.rows[i].Session) {
			c.rows[i].Verify = &session.FanVerify{Command: msg.Command, State: session.VerifyRunning, StartedAt: now}
		}
	}
	c.tickGen++
	return m.reviewTickIfRunning()
}

// ReviewCompareKeep asks to keep the attempt under the cursor.
func (m *OS) ReviewCompareKeep() {
	c := m.review.compare
	row, ok := m.reviewCompareSelected()
	if !ok || row.Gone {
		return
	}
	if len(c.rows) < 2 {
		m.ShowNotification(row.Session+" is the only attempt left", "info", m.Settings.NotificationDuration)
		return
	}
	c.confirmKeep = row.Session
}

// reviewKeepOthers is every attempt keeping name would remove.
func (c *reviewCompare) reviewKeepOthers(name string) []string {
	var out []string
	for _, row := range c.rows {
		if row.Session != name {
			out = append(out, row.Session)
		}
	}
	return out
}

// ReviewCompareConfirm answers the keep question: yes runs keep-fan, anything
// else keeps everything.
func (m *OS) ReviewCompareConfirm(yes bool) tea.Cmd {
	r := &m.review
	c := r.compare
	if c == nil || c.confirmKeep == "" {
		return nil
	}
	name := c.confirmKeep
	c.confirmKeep = ""
	if !yes {
		return nil
	}
	if m.ProcessingRemoteKeys {
		m.ShowNotification(reviewRemoteRefusal, "error", m.Settings.NotificationDuration)
		return nil
	}
	call := m.inboxCaller()
	gen := r.gen
	return func() tea.Msg {
		msg := ReviewKeptMsg{Gen: gen, Kept: name}
		raw, err := call("keep-fan", map[string]any{"session": name}, reviewFanTimeout)
		if err != nil {
			msg.Err = err
			return msg
		}
		var res struct {
			Kept    string          `json:"kept"`
			Removed []reviewKeepRow `json:"removed"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			msg.Err = err
			return msg
		}
		msg.Kept, msg.Removed = res.Kept, res.Removed
		return msg
	}
}

// applyReviewKept says what keep-fan removed and left, and reads the rows
// again. esc from the view then goes to the kept attempt's review.
func (m *OS) applyReviewKept(msg ReviewKeptMsg) tea.Cmd {
	r := &m.review
	if msg.Err != nil {
		m.ShowNotification("Nothing was kept: "+reviewErrorText(msg.Err), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	var removed, left []string
	for _, row := range msg.Removed {
		if row.Removed {
			removed = append(removed, row.Session)
		} else {
			left = append(left, row.Session+" ("+row.Note+")")
		}
	}
	text := "Kept " + msg.Kept + "."
	if len(removed) > 0 {
		text += " Removed " + strings.Join(removed, ", ") + ". Branches stay."
	}
	kind := "info"
	if len(left) > 0 {
		text += " Left " + strings.Join(left, "; ") + ". tuios fan keep --stash moves uncommitted work aside."
		kind = "error"
	}
	m.ShowNotification(text, kind, m.Settings.NotificationDuration*2)
	c := r.compare
	if msg.Gen != r.gen || c == nil {
		return nil
	}
	c.rows = slices.DeleteFunc(c.rows, func(row reviewFanRow) bool { return slices.Contains(removed, row.Session) })
	c.marks = nil
	c.cursor = max(slices.IndexFunc(c.rows, func(row reviewFanRow) bool { return row.Session == msg.Kept }), 0)
	if slices.Contains(removed, c.from.Session) {
		c.from = reviewQuery{Session: msg.Kept, Window: m.reviewAgentWindow(msg.Kept)}
		c.fromWho = msg.Kept
	}
	if r.fan != nil {
		r.fan.Rows = slices.Clone(c.rows)
	}
	return m.reviewCompareCmd(true, 0)
}

// reviewCheckWords says an attempt's last check, in full on a wide screen and
// in a word or two on a narrow one.
func reviewCheckWords(row reviewFanRow, narrow bool) string {
	if v := row.Verify; v != nil {
		cmd := reviewText(v.Command)
		switch v.State {
		case session.VerifyRunning:
			if narrow {
				return "running"
			}
			return "running: " + cmd
		case session.VerifyPassed:
			if narrow {
				return "passed"
			}
			return cmd + " passed"
		case session.VerifyFailed:
			switch {
			case v.Exit != nil && narrow:
				return "failed " + strconv.Itoa(*v.Exit)
			case narrow:
				return "failed"
			case v.Exit != nil:
				return cmd + " failed, exit " + strconv.Itoa(*v.Exit)
			case v.Note != "":
				return cmd + " failed: " + reviewText(v.Note)
			}
			return cmd + " failed"
		}
	}
	if lc := row.LastCommand; lc != nil && lc.Exit != nil {
		if narrow {
			return "exit " + strconv.Itoa(*lc.Exit)
		}
		return "last command exited " + strconv.Itoa(*lc.Exit) + ": " + reviewText(lc.Cmdline)
	}
	if narrow {
		return "-"
	}
	return "not run"
}

// reviewCheckAge is how long ago an attempt's check or last command ran.
func reviewCheckAge(row reviewFanRow, now time.Time) string {
	at := int64(0)
	if v := row.Verify; v != nil {
		at = max(v.FinishedAt, v.StartedAt)
	}
	if lc := row.LastCommand; lc != nil && lc.At > at {
		at = lc.At
	}
	if at == 0 {
		return "-"
	}
	return inboxWait(at, now)
}

// reviewCheckColor colours a check by how it went.
func reviewCheckColor(row reviewFanRow, pal overlay.Palette) color.Color {
	if v := row.Verify; v != nil {
		switch v.State {
		case session.VerifyPassed:
			return pal.Success
		case session.VerifyFailed:
			return pal.Warn
		}
		return pal.Info
	}
	if lc := row.LastCommand; lc != nil && lc.Exit != nil && *lc.Exit != 0 {
		return pal.Warn
	}
	return pal.FgDim
}

// renderReviewCompare draws the compare view: one row per attempt, what it
// changed and its last check, and the keys.
func (m *OS) renderReviewCompare(w, h int, pal overlay.Palette) string {
	c := m.review.compare
	inner := w - 2
	narrow := inner < 100
	now := time.Now()

	title := "Compare  " + reviewText(c.group)
	if c.repo != "" {
		title += " in " + reviewText(c.repo)
	}
	title += ", " + reviewCount(len(c.rows), "attempt")
	if c.base != "" {
		title += " vs " + reviewText(c.base)
	}
	if c.loading {
		title += ", counting"
	}

	sessW := 10
	for _, row := range c.rows {
		sessW = max(sessW, ansi.StringWidth(reviewText(row.Session)))
	}
	sessW = min(sessW, max(inner/4, 12))
	agentW, stateW, filesW, diffW, ageW := 8, 10, 6, 13, 6
	if narrow {
		agentW, stateW, filesW, diffW, ageW = 7, 8, 5, 11, 4
	}
	checkW := max(inner-4-sessW-1-agentW-stateW-filesW-2-diffW-ageW-1, 6)
	left := func(s string, n int) string {
		s = ansi.Truncate(s, n, "")
		return s + strings.Repeat(" ", max(n-ansi.StringWidth(s), 0))
	}
	right := func(s string, n int) string {
		s = ansi.Truncate(s, n, "")
		return strings.Repeat(" ", max(n-ansi.StringWidth(s), 0)) + s
	}

	body := make([]string, 0, h-2)
	body = append(body, reviewPaint([]reviewSeg{{pal.FgMute,
		"    " + left("session", sessW) + " " + left("agent", agentW) + left("state", stateW) +
			right("files", filesW) + "  " + left("+/-", diffW) + left("check", checkW) + right("age", ageW), false}}, inner, nil))
	listH := h - 2 - 4
	start := 0
	if c.cursor >= listH {
		start = c.cursor - listH + 1
	}
	for i := start; i < len(c.rows) && i-start < listH; i++ {
		row := c.rows[i]
		var bg color.Color
		cur, mark := " ", " "
		if i == c.cursor {
			bg, cur = pal.RowSel, overlay.SigilMark()
		}
		if slices.Contains(c.marks, row.Session) {
			mark = "*"
		}
		files, diff := "-", "-"
		if row.Files != nil {
			files = strconv.Itoa(*row.Files)
		} else if c.loading {
			files = "..."
		}
		if row.Added != nil && row.Removed != nil {
			diff = "+" + strconv.Itoa(*row.Added) + " -" + strconv.Itoa(*row.Removed)
		}
		state := reviewText(row.State)
		if row.Gone {
			state = "gone"
		}
		agent := reviewText(row.Agent)
		if agent == "" {
			agent = reviewText(row.Harness)
		}
		body = append(body, reviewPaint([]reviewSeg{
			{pal.AccentBright, " " + cur + mark + " ", false},
			{pal.Fg, left(reviewText(row.Session), sessW) + " ", i == c.cursor},
			{pal.FgDim, left(agent, agentW) + left(state, stateW) + right(files, filesW) + "  ", false},
			{pal.FgDim, left(diff, diffW), false},
			{reviewCheckColor(row, pal), left(reviewCheckWords(row, narrow), checkW), false},
			{pal.FgMute, right(reviewCheckAge(row, now), ageW), false},
		}, inner, bg))
	}
	for len(body) < h-2-3 {
		body = append(body, strings.Repeat(" ", inner))
	}
	question := strings.Repeat(" ", inner)
	if c.confirmKeep != "" {
		others := c.reviewKeepOthers(c.confirmKeep)
		question = reviewPaint([]reviewSeg{{pal.Warning, " Keep " + reviewText(c.confirmKeep) + " and remove " + reviewText(strings.Join(others, ", ")) +
			"? Their worktrees and sessions go; branches stay.", true}}, inner, nil)
	} else if len(c.marks) > 0 {
		question = reviewPaint([]reviewSeg{{pal.FgDim, " Marked: " + reviewText(strings.Join(c.marks, ", ")), false}}, inner, nil)
	}
	body = append(body, question)
	body = append(body, m.reviewStatusRule(inner, pal))

	var footer string
	switch {
	case c.confirmKeep != "":
		footer = reviewHints([]overlay.Hint{{Key: "y", Label: "keep it, remove the others"}, {Key: "n", Label: "cancel"}}, inner, pal)
	case m.review.editor != nil && m.review.editor.kind == reviewEditVerify:
		footer = m.reviewPromptLine("Run in every attempt: ", m.review.editor.draft, []overlay.Hint{{Key: overlay.EnterGlyph, Label: "run"}, {Key: "esc", Label: "cancel"}}, inner, pal)
	default:
		hints := []overlay.Hint{{Key: overlay.EnterGlyph, Label: "review"}, {Key: "m", Label: "mark"}}
		if len(c.marks) == 2 {
			hints = append(hints, overlay.Hint{Key: "d", Label: "diff marked"})
		}
		hints = append(hints, overlay.Hint{Key: "V", Label: "verify"}, overlay.Hint{Key: "K", Label: "keep"}, overlay.Hint{Key: "esc", Label: "back"})
		footer = reviewHints(hints, inner, pal)
	}
	body = append(body, footer)
	return reviewFrame(w, h, title, body, pal.Accent)
}
