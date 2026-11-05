package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The fan comparison: every attempt of a fan side by side, with the judge's
// verdict on top, as the TUI's (internal/app/cockpit_compare.go). c on an
// attempt in the agents view, or its Compare attempts button, opens it. j
// asks an agent to judge, k twice keeps one attempt and removes the others
// with their changes stashed, Enter opens the attempt's pane, and Esc goes
// back.
//
// Cost: compare-fan runs when the view opens, after j, and when a daemon
// event says the fan's verdict came, never on a timer. It runs git in each
// worktree under the daemon's own timeout.

// ckCompareTimeout bounds compare-fan, which runs git in every attempt.
const ckCompareTimeout = 30 * time.Second

// ckJudgeTimeout bounds judge-fan, which waits for the judge to be ready.
const ckJudgeTimeout = 3 * time.Minute

// ckCompare is the open comparison.
type ckCompare struct {
	open    bool
	session string
	gen     uint64
	loading bool
	err     string
	res     *ckCompareResult
	focus   int
	focusOn string
	// armed is the attempt k was pressed on once; armedGen is the read the
	// first k asked for, which a second k waits for. confirm says the
	// second k asked for one more read, and keepSig is what it must show
	// again for the keep to run.
	armed    string
	armedGen uint64
	// askJudge holds that the judge chord asked to start a judge: Enter
	// starts it.
	askJudge bool
	confirm  bool
	keepSig  string
	// busy is what runs now ("judge", "keep"), note what last ended.
	busy, note string
	verdictAt  int64
	// judge is the session of the judge j started, whose turn ending may
	// carry the verdict on a daemon that sends no fan-verdict.
	judge string
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
	Session string `json:"session"`
	Branch  string `json:"branch"`
	Agent   string `json:"agent"`
	Harness string `json:"harness"`
	State   string `json:"state"`
	Files   *int   `json:"files"`
	Added   *int   `json:"added"`
	Removed *int   `json:"removed"`
	Gone    bool   `json:"gone"`
	Note    string `json:"note"`
	Cost    string `json:"cost"`
	Verify  *struct {
		Command string `json:"command"`
		State   string `json:"state"`
	} `json:"verify"`
	FileList     []worktree.FileStat `json:"file_list"`
	Ignored      []string            `json:"ignored"`
	IgnoredCount int                 `json:"ignored_count"`
	IgnoredError string              `json:"ignored_error"`
	IgnoredHash  string              `json:"ignored_hash"`
}

// ckLetter is an attempt's letter: A, B, C.
func ckLetter(i int) string { return string(rune('A' + i%26)) }

// ckFanLetters are the letters of the fans' attempts by session: A for
// the fan's first session, B for its -2, as the TUI and the comparison
// name them. A session alone in its group has no letter.
func ckFanLetters(agents []client.Agent) map[string]string {
	groups := map[string][]string{}
	for _, ag := range agents {
		if ckFanOf(ag) && !slices.Contains(groups[ag.Group], ag.Session) {
			groups[ag.Group] = append(groups[ag.Group], ag.Session)
		}
	}
	var out map[string]string
	for _, ss := range groups {
		if len(ss) < 2 {
			continue
		}
		slices.SortFunc(ss, ckFanOrder)
		if out == nil {
			out = map[string]string{}
		}
		for i, s := range ss {
			out[s] = ckLetter(i)
		}
	}
	return out
}

// ckFanOrder sorts a fan's sessions the way fan numbers them: the stem,
// then -2, -3 and on.
func ckFanOrder(a, b string) int {
	num := func(s string) (string, int) {
		if i := strings.LastIndexByte(s, '-'); i > 0 {
			if n, err := strconv.Atoi(s[i+1:]); err == nil && n > 1 {
				return s[:i], n
			}
		}
		return s, 1
	}
	sa, na := num(a)
	sb, nb := num(b)
	if sa != sb {
		return strings.Compare(a, b)
	}
	return na - nb
}

// ckAttemptLetter is the agent's letter in its fan, "" when it is not an
// attempt of one.
func (a *App) ckAttemptLetter(ag client.Agent) string {
	if !ckFanOf(ag) {
		return ""
	}
	return a.ck.letters[ag.Session]
}

// ckAgentName is the name an agent's row shows. An attempt of a fan
// shows the fan's task, with its letter beside it: the attempts' own
// names are the harness's, "claude" twice (council round 3, visual S5).
func (a *App) ckAgentName(ag client.Agent) string {
	if a.ckAttemptLetter(ag) == "" {
		return ag.Name
	}
	if p, _ := ag.Meta["prompt"].(string); strings.TrimSpace(p) != "" {
		line, _, _ := strings.Cut(strings.TrimSpace(p), "\n")
		return line
	}
	// The fan's group is its task as a slug: "fan/try-two-ways".
	g := ag.Group
	if i := strings.LastIndexByte(g, '/'); i >= 0 {
		g = g[i+1:]
	}
	if i := strings.LastIndex(g, "fan-"); i >= 0 && i+4 < len(g) {
		g = g[i+4:]
	}
	if g = strings.TrimSpace(strings.ReplaceAll(g, "-", " ")); g != "" {
		return g
	}
	return ag.Name
}

// ckFanOf reports whether the agent is an attempt of a fan on this machine.
func ckFanOf(ag client.Agent) bool { return ag.Group != "" && ag.Host == "" && ag.Session != "" }

// ckHasSiblings reports whether the agent is an attempt of a fan with
// another attempt listed: the comparison has something to compare.
func (a *App) ckHasSiblings(ag client.Agent) bool {
	if !ckFanOf(ag) {
		return false
	}
	for _, x := range a.ck.agents {
		if x.Group == ag.Group && x.Host == "" && x.Session != ag.Session {
			return true
		}
	}
	return false
}

// openCompare is c: the comparison of the selected attempt's fan.
func (a *App) openCompare() {
	ag, ok := a.ckSelectedAgent()
	if !ok || !ckFanOf(ag) {
		a.toast(locales.CompareSelectAttempt())
		return
	}
	a.ck.cmp = &ckCompare{open: true, session: ag.Session, focusOn: ag.Session}
	a.compareFetch()
}

// compareFetch reads the comparison again.
func (a *App) compareFetch() {
	cp := a.ck.cmp
	cl := a.cockpitClient()
	if cp == nil || cl == nil {
		return
	}
	cp.gen++
	cp.loading = true
	gen, sess := cp.gen, cp.session
	go func() {
		raw, err := cl.LongVerb("compare-fan", map[string]any{"session": sess, "detail": true}, ckCompareTimeout)
		var res ckCompareResult
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		a.update(func() { a.applyCompare(gen, &res, err) })
	}()
}

// applyCompare takes compare-fan's answer.
func (a *App) applyCompare(gen uint64, res *ckCompareResult, err error) {
	cp := a.ck.cmp
	if cp == nil || gen != cp.gen {
		return
	}
	cp.loading = false
	if err != nil {
		cp.err = locales.CompareCannotRead(flat(err.Error()))
		cp.confirm = false
		return
	}
	cp.err, cp.res = "", res
	cp.focus = min(cp.focus, max(len(res.Rows)-1, 0))
	if cp.focusOn != "" {
		for i, r := range res.Rows {
			if r.Session == cp.focusOn {
				cp.focus = i
			}
		}
		cp.focusOn = ""
	}
	if v := res.Verdict; v != nil && v.At != cp.verdictAt {
		cp.verdictAt, cp.note = v.At, ""
		for i, r := range res.Rows {
			if r.Session == v.Pick && cp.armed == "" {
				cp.focus = i
			}
		}
	}
	if !cp.confirm {
		return
	}
	// The read the second k asked for: keep only what was shown.
	cp.confirm = false
	cp.armedGen = cp.gen
	if r, ok := cp.focused(); !ok || r.Session != cp.armed || ckIgnoredFailed(res.Rows, cp.focus) != "" {
		return
	}
	if sig := ckKeepSig(res.Rows, cp.focus); sig != cp.keepSig {
		cp.note = locales.CompareChanged()
		cp.keepSig = sig
		return
	}
	a.compareKeepNow()
}

// compareEvent reads the comparison again when an event names a session of
// the fan or its judge: a verdict came, or an attempt or the judge changed
// state. A keep that waits for a second k keeps the list it showed.
func (a *App) compareEvent(sess string) {
	cp := a.ck.cmp
	if cp == nil || !cp.open || cp.loading || cp.busy != "" || cp.armed != "" || cp.res == nil {
		return
	}
	ours := sess == cp.judge
	for _, r := range cp.res.Rows {
		ours = ours || r.Session == sess
	}
	if ours {
		a.compareFetch()
	}
}

func (cp *ckCompare) focused() (ckFanAttempt, bool) {
	if cp.res == nil || cp.focus >= len(cp.res.Rows) {
		return ckFanAttempt{}, false
	}
	return cp.res.Rows[cp.focus], true
}

// ckKeepSig is what a keep of attempt keep removes, as a read showed it.
func ckKeepSig(rows []ckFanAttempt, keep int) string {
	var b strings.Builder
	for i, r := range rows {
		if i != keep {
			b.WriteString(r.Session + "\x00" + r.IgnoredHash + "\x00")
		}
	}
	return b.String()
}

// ckIgnoredFailed names the attempts whose ignored files could not be
// listed, "" when every list was read. A keep waits for a list.
func ckIgnoredFailed(rows []ckFanAttempt, keep int) string {
	var parts []string
	for i, r := range rows {
		if i != keep && r.IgnoredError != "" {
			parts = append(parts, ckLetter(i)+": "+flat(r.IgnoredError))
		}
	}
	return strings.Join(parts, " · ")
}

// compareJudge starts an agent that judges the attempts.
func (a *App) compareJudge() {
	cp := a.ck.cmp
	cl := a.cockpitClient()
	if cp == nil || cl == nil || cp.busy != "" || cp.res == nil {
		return
	}
	cp.armed = ""
	if len(cp.res.Rows) < 2 {
		cp.note = locales.CompareOneLeft()
		return
	}
	cp.busy, cp.note = "judge", ""
	gen, sess := cp.gen, cp.session
	go func() {
		raw, err := cl.LongVerb("judge-fan", map[string]any{"session": sess}, ckJudgeTimeout)
		var res struct {
			JudgeSession string `json:"judge_session"`
			Ready        bool   `json:"ready"`
			Reason       string `json:"reason"`
		}
		if err == nil {
			_ = json.Unmarshal(raw, &res)
		}
		a.update(func() {
			cp := a.ck.cmp
			if cp == nil || cp.gen < gen {
				return
			}
			cp.busy = ""
			switch {
			case err != nil:
				cp.note = locales.CompareJudgeFailed(compareErr(err))
				return
			case !res.Ready && res.Reason != "":
				cp.judge = res.JudgeSession
				cp.note = locales.CompareJudgeNotReady(res.JudgeSession, flat(res.Reason))
			default:
				cp.judge = res.JudgeSession
				cp.note = locales.CompareJudgeStarted(res.JudgeSession)
			}
			a.refreshCockpit()
			a.compareFetch()
		})
	}()
}

// compareErr is a verb's error in words.
func compareErr(err error) string {
	if verr, ok := errors.AsType[*session.VerbCallError](err); ok && verr.Code == session.ErrVerbUnknownVerb {
		return locales.CompareOldDaemon()
	}
	return flat(err.Error())
}

// compareKeep is the keep chord or the Keep button: the first says what a
// keep does; Enter, or a second click, keeps the focused attempt and
// removes the others (confirmed).
func (a *App) compareKeep(confirmed bool) {
	cp := a.ck.cmp
	if cp == nil || cp.busy != "" {
		return
	}
	r, ok := cp.focused()
	if !ok {
		return
	}
	if !ckKeepable(r) {
		cp.armed, cp.note = "", locales.CompareNothingToKeep(ckLetter(cp.focus))
		return
	}
	if cp.armed != r.Session {
		// The first press reads the attempts again: the warning lists the
		// files a keep deletes as they are now.
		cp.armed, cp.confirm, cp.note, cp.askJudge = r.Session, false, "", false
		a.compareFetch()
		cp.armedGen = cp.gen
		return
	}
	if !confirmed {
		// The chord again does nothing: Enter keeps.
		return
	}
	if cp.loading || cp.confirm || cp.armedGen != cp.gen {
		return
	}
	if ckIgnoredFailed(cp.res.Rows, cp.focus) != "" {
		a.compareFetch()
		cp.armedGen = cp.gen
		return
	}
	// The second k reads once more, and keeps only when nothing changed.
	cp.keepSig, cp.confirm, cp.note = ckKeepSig(cp.res.Rows, cp.focus), true, ""
	a.compareFetch()
}

// compareKeepNow keeps the focused attempt and removes the others, as the
// last read showed them.
func (a *App) compareKeepNow() {
	cp := a.ck.cmp
	cl := a.cockpitClient()
	r, ok := cp.focused()
	if !ok || cl == nil {
		return
	}
	var siblings []string
	ignored := map[string]string{}
	for i, x := range cp.res.Rows {
		if i != cp.focus {
			siblings = append(siblings, x.Session)
			ignored[x.Session] = x.IgnoredHash
		}
	}
	cp.armed, cp.busy, cp.note = "", "keep", ""
	kept, letter := r.Session, ckLetter(cp.focus)
	params := map[string]any{"session": kept, "stash": true, "group": cp.res.Group, "repo_root": cp.res.RepoRoot, "fan_id": cp.res.FanID,
		"siblings": siblings, "ignored": ignored}
	go func() {
		raw, err := cl.LongVerb("keep-fan", params, ckCompareTimeout)
		var res struct {
			Removed []struct {
				Removed bool `json:"removed"`
			} `json:"removed"`
			Left int `json:"left"`
		}
		if err == nil {
			_ = json.Unmarshal(raw, &res)
		}
		a.update(func() {
			if cp := a.ck.cmp; cp != nil {
				cp.busy = ""
				if err != nil {
					cp.note = locales.CompareKeepFailed(compareErr(err))
					return
				}
				a.ck.cmp = nil
			}
			if err != nil {
				a.toast(locales.CompareKeepFailed(compareErr(err)))
				return
			}
			n := len(res.Removed) - res.Left
			text := locales.CompareKept(n, letter, kept)
			if res.Left > 0 {
				text += " " + locales.CompareKeptLeft(res.Left)
			}
			a.toast(text)
			// The kept attempt keeps the selection.
			for _, ag := range a.ck.agents {
				if ag.Host == "" && ag.Session == kept {
					a.ck.selectWhenSeen = ag.Key()
				}
			}
			a.refreshCockpit()
		})
	}()
}

// compareGo is Enter: the focused attempt's pane.
func (a *App) compareGo() {
	cp := a.ck.cmp
	r, ok := cp.focused()
	if !ok {
		return
	}
	for _, ag := range a.ck.agents {
		if ag.Host == "" && ag.Session == r.Session {
			a.ck.cmp = nil
			a.goToAgent(ag)
			return
		}
	}
	cp.note = locales.CompareNoPane(r.Session)
}

// compareView draws the comparison while it is open.
func (a *App) compareView(c *ui.Context, pal *palette) {
	cp := a.ck.cmp
	if cp == nil || !a.ck.open {
		return
	}
	n := 0
	if cp.res != nil {
		n = len(cp.res.Rows)
	}
	ui.DialogBase(c.Key("compare"), &cp.open, func(backdrop, panel ui.Element) {
		dialogMotion(backdrop, panel, pal.overlay)
		ww, wh := c.Size()
		w := min(1180, ww-2*sp4)
		floating(panel.Width(w).Height(min(680, wh-2*sp4)).Padding(sp5).Gap(sp3).Radius(rSheet).Background(pal.popover), pal).Label(locales.CompareTitle())
		a.compareKeys(c, panel, cp, n)
		a.compareHeader(c, pal, cp)
		switch {
		case cp.res == nil && cp.err != "":
			ui.Text(c, cp.err).FontSize(fsBody).TextColor(pal.danger).MaxLines(3)
			ui.Spacer(c)
		case cp.res == nil:
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		default:
			a.compareVerdict(c, pal, cp)
			// The attempts side by side; they scroll sideways only when
			// they do not fit at 260 each. The row holds the keys, so
			// Enter is the view's, not a button's.
			colW := (w - 2*sp5 - float32(max(n-1, 0))*sp3 - 4) / float32(max(n, 1))
			cols := func() {
				for i, r := range cp.res.Rows {
					a.compareAttempt(c, pal, cp, i, r, max(colW, 260))
				}
			}
			if colW >= 260 {
				ui.Row(c.Key("cols")).Grow(1).MinHeight(0).Gap(sp3).AlignItems(ui.Stretch).Padding(2).
					Focusable().FocusRing(false).AutoFocus().Label(locales.CompareTitle()).Children(cols)
			} else {
				ui.ScrollHorizontal(c.Key("cols")).Grow(1).MinHeight(0).Focusable().FocusRing(false).AutoFocus().Label(locales.CompareTitle()).Children(func() {
					ui.Row(c).Gap(sp3).AlignItems(ui.Stretch).Padding(2).Children(cols)
				})
			}
		}
		a.compareFoot(c, pal, cp)
	})
	if !cp.open && a.ck.cmp == cp {
		a.ck.cmp = nil
		a.ck.fleetFocus.Focus()
	}
}

// compareKeys are the comparison's keys. The arrows, Home, End and Tab
// move between the attempts. Ctrl+Alt+J asks to start a judge and
// Ctrl+Alt+K asks to keep: Enter does what was asked, and Escape or any
// other key drops it. A printable key never acts (cockpit_guard.go).
func (a *App) compareKeys(c *ui.Context, panel ui.Element, cp *ckCompare, n int) {
	step := func(d int) {
		if f := min(max(cp.focus+d, 0), n-1); f != cp.focus && f >= 0 {
			cp.focus, cp.armed, cp.askJudge = f, "", false
		}
	}
	if _, ok := typedText(panel); ok {
		// Compare has no field: the text drops what was asked, and runs
		// nothing.
		cp.armed, cp.askJudge = "", false
		a.toast(locales.CompareKeysHere())
		c.Invalidate()
		return
	}
	key := func(k ui.Key) bool { return panel.Shortcut(0, k) }
	if d, to, ok := listMove(panel); ok || key(ui.KeyLeft) || key(ui.KeyRight) {
		switch {
		case key(ui.KeyLeft):
			step(-1)
		case key(ui.KeyRight):
			step(1)
		case to < 0:
			step(-n)
		case to > 0:
			step(n)
		default:
			step(d)
		}
		c.Invalidate()
		return
	}
	switch {
	case chordHit(panel, chJudge):
		if cp.busy == "" && cp.res != nil {
			cp.armed, cp.askJudge = "", true
		}
	case chordHit(panel, chKeep):
		a.compareKeep(false)
	case key(ui.KeyEnter) && cp.askJudge:
		cp.askJudge = false
		a.compareJudge()
	case key(ui.KeyEnter) && cp.armed != "":
		a.compareKeep(true)
	case key(ui.KeyEnter):
		a.compareGo()
	default:
		return
	}
	c.Invalidate()
}

// ckKeepable reports whether an attempt can be kept: it has stopped, with
// changes to keep.
func ckKeepable(r ckFanAttempt) bool {
	if r.Gone || r.State == "working" {
		return false
	}
	return len(r.FileList) > 0 || r.Files != nil && *r.Files > 0
}

// compareHeader is the title, the fan and its base, and close.
func (a *App) compareHeader(c *ui.Context, pal *palette, cp *ckCompare) {
	ui.Row(c).Gap(sp3).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, locales.CompareTitle()).FontSize(fsTitle).FontWeight(wSemibold).TextColor(pal.text).SingleLine().Shrink(0)
		if r := cp.res; r != nil {
			what := r.Group
			if r.Base != "" {
				what = locales.CompareFromBase(r.Group, r.Base)
			}
			oneLine(c, what, fsSmall, 0).TextColor(pal.textFaint)
		}
		ui.Spacer(c)
		if cp.loading {
			ui.Spinner(c)
		}
		iconButton(c, "cmp-close", closeIcon, locales.CompareBack()+" (Esc)", pal, func() { cp.open = false })
	})
	if r := cp.res; r != nil && r.Prompt != "" {
		ui.Text(c, flat(r.Prompt)).FontSize(fsBody).TextColor(pal.textMuted).MaxLines(2).Shrink(0)
	}
}

// compareVerdict is the judge's verdict, when there is one.
func (a *App) compareVerdict(c *ui.Context, pal *palette, cp *ckCompare) {
	v := cp.res.Verdict
	if v == nil {
		return
	}
	pick := v.Pick
	for i, r := range cp.res.Rows {
		if r.Session == v.Pick {
			pick = ckLetter(i) + ", " + r.Branch
		}
	}
	ui.Column(c).Shrink(0).Gap(4).Padding(sp3).Radius(rCard).Background(pal.accent.Alpha(0.1)).InsetShadow(0, 0, 0, 1, pal.accent.Alpha(0.35)).Children(func() {
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.CompareVerdictPick(pick)).FontSize(fsBody).FontWeight(wSemibold).TextColor(pal.text).SingleLine()
			if by := strings.TrimSpace(v.Judge + " " + v.By); by != "" {
				oneLine(c, by, fsSmall, 0).TextColor(pal.textFaint)
			}
		})
		if v.Summary != "" {
			ui.Text(c, v.Summary).FontSize(fsBody).TextColor(pal.textMuted).MaxLines(3)
		}
		for _, r := range v.Reasons {
			ui.Text(c, "· "+flat(r)).FontSize(fsSmall).TextColor(pal.textMuted).MaxLines(2)
		}
	})
}

// compareAttempt is one attempt's column.
func (a *App) compareAttempt(c *ui.Context, pal *palette, cp *ckCompare, i int, r ckFanAttempt, w float32) {
	focused := i == cp.focus
	picked := cp.res.Verdict != nil && cp.res.Verdict.Pick == r.Session
	col := ui.Column(c.Key("att-" + r.Session)).Width(w).Shrink(0).Gap(sp2).Padding(sp3).Radius(rCard).Background(pal.card).Cursor(ui.CursorPointer)
	switch {
	case focused:
		col.InsetShadow(0, 0, 0, 2, pal.accent)
	default:
		col.InsetShadow(0, 0, 0, 1, pal.divider)
	}
	if col.Clicked() {
		if cp.focus != i {
			cp.armed = ""
		}
		cp.focus = i
		c.Invalidate()
	}
	if col.DoubleClicked() {
		a.compareGo()
	}
	col.Children(func() {
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(20, 20).Radius(rChip).Background(pal.accent.Alpha(0.18)).Center().Shrink(0).Children(func() {
				ui.Text(c, ckLetter(i)).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.accent)
			})
			oneLine(c, r.Branch, fsBody, wSemibold).TextColor(pal.text)
			ui.Spacer(c)
			if picked {
				chip(c, pal, pal.accent.Alpha(0.15), pal.accent, func() {
					ui.Text(c, locales.CompareJudgePick()).FontSize(fsSmall).FontWeight(wMedium).SingleLine()
				}).Shrink(0)
			}
		})
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, harnessIcon(r.Harness)).Size(12, 12).TextColor(pal.textFaint).Shrink(0)
			oneLine(c, cmp.Or(r.Agent, harnessName(r.Harness)), fsSmall, 0).TextColor(pal.textMuted)
			ui.Spacer(c)
			word, colr := compareState(pal, r)
			chip(c, pal, colr.Alpha(0.15), colr, func() {
				ui.Text(c, word).FontSize(fsSmall).FontWeight(wMedium).SingleLine()
			}).Shrink(0)
		})
		// The diff's size, and what verify said.
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
			if r.Files != nil {
				ui.Text(c, locales.CompareFiles(*r.Files)).FontSize(fsSmall).TextColor(pal.textMuted).FontFeatures("tnum").SingleLine()
			}
			if r.Added != nil {
				ui.Text(c, fmt.Sprintf("+%d", *r.Added)).FontSize(fsSmall).TextColor(pal.done).FontFeatures("tnum").SingleLine()
			}
			if r.Removed != nil {
				ui.Text(c, fmt.Sprintf("−%d", *r.Removed)).FontSize(fsSmall).TextColor(pal.danger).FontFeatures("tnum").SingleLine()
			}
			ui.Spacer(c)
			if v := r.Verify; v != nil && v.State != "" {
				colr := pal.textMuted
				switch v.State {
				case "pass", "passed":
					colr = pal.done
				case "fail", "failed":
					colr = pal.danger
				}
				oneLine(c, locales.CompareVerify(v.State), fsSmall, wMedium).TextColor(colr).Tooltip(v.Command)
			}
		})
		if r.Note != "" {
			ui.Text(c, flat(r.Note)).FontSize(fsSmall).TextColor(pal.textMuted).MaxLines(3)
		}
		ui.Box(c).Height(1).Background(pal.divider)
		ui.Scroll(c.Key("files")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				if len(r.FileList) == 0 {
					ui.Text(c, locales.CompareNoChanges()).FontSize(fsSmall).TextColor(pal.textFaint)
				}
				for _, f := range r.FileList {
					ui.Row(c).Gap(sp2).AlignItems(ui.Center).Height(22).Children(func() {
						pathLine(c, "p-"+f.Path, f.Path, fsSmall, pal.text)
						ui.Text(c, fmt.Sprintf("+%d", f.Added)).FontSize(fsSmall).TextColor(pal.done).FontFeatures("tnum").SingleLine().Shrink(0)
						ui.Text(c, fmt.Sprintf("−%d", f.Removed)).FontSize(fsSmall).TextColor(pal.danger).FontFeatures("tnum").SingleLine().Shrink(0)
					})
				}
			})
		})
	})
}

// compareState is an attempt's state in a word and a colour.
func compareState(pal *palette, r ckFanAttempt) (string, ui.Color) {
	switch {
	case r.Gone:
		return locales.CompareGone(), pal.textMuted
	case r.State == "working":
		return locales.StatusWorking(), pal.work
	case r.State == "needs_input":
		return locales.StatusNeedsYou(), pal.attention
	case r.State == "errored":
		return locales.StatusFailed(), pal.danger
	case r.State == "done":
		return locales.CompareDone(), pal.done
	}
	return locales.CockpitIdle(), pal.textMuted
}

// compareFoot is the note or the keep's warning, the hints and the buttons.
func (a *App) compareFoot(c *ui.Context, pal *palette, cp *ckCompare) {
	r, ok := cp.focused()
	switch {
	case cp.askJudge:
		ui.Text(c, locales.CompareAskJudge()).FontSize(fsSmall).TextColor(pal.attention).MaxLines(2).Shrink(0)
	case cp.armed != "" && ok && cp.res != nil:
		var others []string
		ignored := 0
		for i, x := range cp.res.Rows {
			if i != cp.focus {
				others = append(others, ckLetter(i))
				ignored += x.IgnoredCount
			}
		}
		warn := locales.CompareKeepWarn(ckLetter(cp.focus), strings.Join(others, ", "))
		if ignored > 0 {
			warn += " " + locales.CompareKeepIgnored(ignored)
		}
		if f := ckIgnoredFailed(cp.res.Rows, cp.focus); f != "" {
			warn = locales.CompareIgnoredFailed(f)
		}
		ui.Text(c, warn).FontSize(fsSmall).TextColor(pal.attention).MaxLines(3).Shrink(0)
	case cp.note != "":
		ui.Text(c, cp.note).FontSize(fsSmall).TextColor(pal.textMuted).MaxLines(2).Shrink(0)
	case cp.err != "" && cp.res != nil:
		ui.Text(c, cp.err).FontSize(fsSmall).TextColor(pal.danger).MaxLines(2).Shrink(0)
	}
	ui.Row(c).Gap(sp3).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Row(c).Gap(sp3).AlignItems(ui.Center).Grow(1).MinWidth(0).Clip().Children(func() {
			keyHint(c, pal, "← →", locales.CompareHintPick())
			keyHint(c, pal, "↵", locales.CockpitHintOpen())
			keyHint(c, pal, "Esc", locales.CockpitHintBack())
		})
		if button(c, "cmp-read", locales.CompareReadAgain(), "", false, pal) {
			cp.armed, cp.askJudge = "", false
			a.compareFetch()
		}
		if button(c, "cmp-judge", busyLabel(cp.busy == "judge", locales.CompareJudge()), chordName(chJudge), false, pal) {
			a.compareJudge()
		}
		switch {
		case ok && !ckKeepable(r):
			// Nothing to keep yet: the button shows, off, and says why.
			label := locales.CompareKeep(ckLetter(cp.focus))
			b := ui.ButtonBase(c.Key("cmp-keep")).Height(hControl).Padding(0, sp3).Gap(6).Radius(rControl).AlignItems(ui.Center).Shrink(0).
				Label(label).Disabled(true).Tooltip(locales.CompareNothingToKeep(ckLetter(cp.focus)))
			face(b, pal, pal.control).TextColor(pal.textFaint)
			b.Children(func() {
				ui.Text(c, label).FontSize(fsBody).FontWeight(wMedium).SingleLine()
				ui.Text(c, chordName(chKeep)).FontSize(fsSmall).SingleLine()
			})
		case ok:
			label := locales.CompareKeep(ckLetter(cp.focus))
			if cp.armed == r.Session {
				label = locales.CompareKeepAgain(ckLetter(cp.focus))
			}
			if kindButton(c, "cmp-keep", busyLabel(cp.busy == "keep", label), chordName(chKeep), btnDanger, hControl, rControl, pal, false) {
				a.compareKeep(cp.armed == r.Session)
			}
		}
		if button(c, "cmp-open", locales.CockpitOpenPane(), "↵", true, pal) {
			a.compareGo()
		}
	})
}

func busyLabel(busy bool, label string) string {
	if busy {
		return label + "…"
	}
	return label
}
