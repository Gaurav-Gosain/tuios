package session

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The judge of a fan: an agent judge-fan starts to read the attempts and
// pick one, and set-fan-verdict, how it reports.
//
// The judge's reach is held to what it needs. Each attempt's record names
// the judge by its session's ID and its pane's window ID, never by a name
// another session could take. Only that pane reaches the attempts, and only
// with verbs that read (checkGrants, judgeReaches), plus set-fan-verdict. It
// cannot type into an attempt's pane, run a command in an attempt's session
// or start an agent there. A verdict is recorded only by that pane or by a
// call from outside every pane, and it says which of the two recorded it.

// Bounds of a verdict, so a judge's report stays small on every attempt.
const (
	fanVerdictText    = 400
	fanVerdictReasons = 6
	fanVerdictNotes   = 4
)

// judgeReadVerbs are the reads a judge makes of the attempts it judges: the
// comparison, the diffs and checkpoints, the attempts' screens and
// scrollback, their windows and their agents' state. Nothing else of an
// attempt's session is in its reach: not its command history, mail,
// environment, stash or approvals.
var judgeReadVerbs = map[string]bool{
	"compare-fan":      true,
	"review-diff":      true,
	"list-checkpoints": true,
	"checkpoint-diff":  true,
	"capture-pane":     true,
	"list-windows":     true,
	"get-agent-state":  true,
}

// fanJudgeGrants are what the judge's pane holds: read alone. Through tuios
// it reads, and records its verdict through judgeReaches. It cannot start a
// fan, an agent or a command anywhere.
var fanJudgeGrants = []string{"read"}

// paneViewOrNil is the pane the grant check found for the connection, nil
// for none.
func (cs *connState) paneViewOrNil() *paneAuth {
	if cs == nil {
		return nil
	}
	return cs.paneView.Load()
}

// judgeReaches reports whether pa is the live judge pane of the fan the
// session named target belongs to.
func (d *Daemon) judgeReaches(pa *paneAuth, target string) bool {
	if pa == nil || pa.sessionID == "" || pa.window == "" {
		return false
	}
	t, _ := d.manager.ResolveSession(target)
	if t == nil {
		return false
	}
	wt := t.Worktree()
	if wt == nil || wt.JudgeSession == "" || wt.JudgeSession != pa.sessionID || wt.JudgeWindow != pa.window {
		return false
	}
	judge := d.manager.GetSessionByID(wt.JudgeSession)
	if judge == nil {
		return false
	}
	for _, w := range judge.GetState().Windows {
		if w.ID == wt.JudgeWindow {
			return true
		}
	}
	return false
}

// forgetFanJudge clears the judge record of every attempt the session with
// ID id judged. It runs when that session ends.
func (d *Daemon) forgetFanJudge(id string) {
	for _, s := range d.manager.AllSessions() {
		if wt := s.Worktree(); wt == nil || wt.JudgeSession != id {
			continue
		}
		_ = s.mutateState(func(st *SessionState) error {
			if st.Worktree != nil && st.Worktree.JudgeSession == id {
				st.Worktree.JudgeSession, st.Worktree.JudgeWindow = "", ""
			}
			return nil
		})
	}
}

// setFanVerdictParams are what set-fan-verdict takes.
type setFanVerdictParams struct {
	Session string              `json:"session"`
	Pick    string              `json:"pick"`
	Summary string              `json:"summary"`
	Reasons []string            `json:"reasons"`
	Notes   map[string][]string `json:"notes"`
}

// verbSetFanVerdict records a judge's pick on every attempt of a fan, each
// with its own notes. Only the fan's judge pane, or a call from outside
// every pane, records one: an attempt cannot name itself the winner.
func (d *Daemon) verbSetFanVerdict(cs *connState, params json.RawMessage) (any, *verbError) {
	var p setFanVerdictParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Summary) == "" {
		return nil, invalidParam("summary", "summary is required: the verdict in a sentence or two")
	}
	if cs != nil && cs.viaLink {
		return nil, newVerbError(ErrVerbForbidden, "a verdict is recorded on the machine of the fan, not over a link")
	}
	_, info, verr := d.fanTarget(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	by, harness := "you", ""
	if pa := d.paneAuthority(cs); pa != nil {
		if !d.judgeReaches(pa, p.Session) {
			return nil, hintedVerbError(ErrVerbForbidden, "only the judge tuios started for this fan records its verdict", &VerbHint{
				Verb:   "judge-fan",
				Detail: "Nothing was recorded. A pane of an attempt, or any other pane, cannot record a verdict. The person starts a judge with j in the agents view.",
			})
		}
		by = "judge"
		if judge := d.manager.GetSessionByID(info.JudgeSession); judge != nil {
			for _, w := range judge.GetState().Windows {
				if w.ID == info.JudgeWindow {
					harness = string(w.AgentHarness)
				}
			}
		}
	}
	siblings := d.fanSiblings(info)
	names := make([]string, 0, len(siblings))
	for _, s := range siblings {
		names = append(names, s.Name())
	}
	if !slices.Contains(names, p.Pick) {
		return nil, hintedVerbError(ErrVerbInvalidParams, "pick "+p.Pick+" is not an attempt of this fan", &VerbHint{
			Param: "pick", Available: names,
			Detail: "Nothing was recorded. Name the session of the attempt the judge recommends.",
		})
	}
	for name := range p.Notes {
		if !slices.Contains(names, name) {
			return nil, hintedVerbError(ErrVerbInvalidParams, "notes name "+name+", which is not an attempt of this fan", &VerbHint{
				Param: "notes", Available: names,
			})
		}
	}
	clip := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) > fanVerdictText {
			s = strings.ToValidUTF8(s[:fanVerdictText], "")
		}
		return s
	}
	clipAll := func(in []string, n int) []string {
		var out []string
		for _, s := range in {
			if s = clip(s); s != "" && len(out) < n {
				out = append(out, s)
			}
		}
		return out
	}
	at := time.Now().UnixNano()
	for _, s := range siblings {
		v := &FanVerdict{Pick: p.Pick, Summary: clip(p.Summary), Reasons: clipAll(p.Reasons, fanVerdictReasons),
			Notes: clipAll(p.Notes[s.Name()], fanVerdictNotes), By: by, Judge: harness, At: at}
		_ = s.mutateState(func(st *SessionState) error {
			if st.Worktree != nil {
				st.Worktree.Verdict = v
			}
			return nil
		})
	}
	return map[string]any{"type": "fan_verdict", "group": info.Group, "pick": p.Pick, "by": by, "attempts": names}, nil
}

// judgeFanParams are what judge-fan takes.
type judgeFanParams struct {
	Session      string            `json:"session"`
	Agent        string            `json:"agent"`
	Env          map[string]string `json:"env"`
	ReadyTimeout int               `json:"ready_timeout"`
}

// verbJudgeFan starts an agent that judges the attempts of a fan, in a new
// session of its own in the repository's main checkout. The attempts name
// the judge's session and pane only once start-agent made both, and the
// first prompt is typed only after that, so the judge's first call reaches
// them. Only the person, or a pane with admin, starts a judge: an attempt
// cannot.
func (d *Daemon) verbJudgeFan(cs *connState, params json.RawMessage) (any, *verbError) {
	var p judgeFanParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if pa := d.paneAuthority(cs); pa != nil && !pa.grants.Has(GrantAdmin) {
		return nil, newVerbError(ErrVerbForbidden, "starting a judge needs the admin grant: the person starts one with j in the agents view")
	}
	_, info, verr := d.fanTarget(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	siblings := d.fanSiblings(info)
	if len(siblings) < 2 {
		return nil, newVerbError(ErrVerbInvalidParams, "the fan has one attempt left, so there is nothing to judge")
	}
	for _, s := range siblings {
		if !d.callerReachesSession(cs, s.Name()) {
			return nil, newVerbError(ErrVerbForbidden, "the caller cannot reach every attempt of the fan, so it cannot start a judge for them")
		}
	}
	agent := strings.TrimSpace(p.Agent)
	if agent == "" {
		if wt := siblings[0].Worktree(); wt != nil {
			agent = wt.Agent
		}
	}
	if agent == "" {
		return nil, invalidParam("agent", "agent is required: no attempt names the agent it ran")
	}
	// A session of its own, new: a free name, which start-agent must create.
	name := ""
	base := fanJudgeSession(info)
	for i := 1; i <= 9 && name == ""; i++ {
		n := base
		if i > 1 {
			n += "-" + strconv.Itoa(i)
		}
		if d.manager.GetSession(n) == nil {
			name = n
		}
	}
	if name == "" {
		return nil, hintedVerbError(ErrVerbInvalidParams, "every session name for the judge of this fan is taken", &VerbHint{
			Detail: "Nothing was started. Close the judge sessions named " + base + " and try again.",
		})
	}
	raw, _ := json.Marshal(map[string]any{
		"session": name, "agent": agent, "name": "judge", "cwd": info.RepoRoot,
		"env": p.Env, "grants": fanJudgeGrants, "ready_timeout": p.ReadyTimeout,
	})
	res, verr := d.verbStartAgent(cs, raw)
	if verr != nil {
		return nil, verr
	}
	out, _ := res.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	sessionID, _ := out["session_id"].(string)
	window, _ := out["window_id"].(string)
	created, _ := out["created_session"].(bool)
	judge := d.manager.GetSessionByID(sessionID)
	if !created || judge == nil || window == "" {
		// Another caller made a session of that name first. Its panes get
		// nothing, and the agent just started there is closed.
		if judge != nil && window != "" {
			closeRaw, _ := json.Marshal(map[string]any{"session": judge.Name(), "window": window})
			_, _ = d.verbCloseWindow(cs, closeRaw)
		}
		return nil, newVerbError(ErrVerbInternal, "the judge's session was made by another caller first, so no judge was started")
	}
	for _, s := range siblings {
		_ = s.mutateState(func(st *SessionState) error {
			if st.Worktree != nil {
				st.Worktree.JudgeSession, st.Worktree.JudgeWindow = sessionID, window
			}
			return nil
		})
	}
	harness, _ := out["agent"].(string)
	timeout := time.Duration(p.ReadyTimeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	go d.deliverFanPrompt(judge, window, harness, fanJudgePrompt(info, siblings), timeout)
	out["type"] = "fan_judge"
	out["group"] = info.Group
	out["judge_session"] = judge.Name()
	return out, nil
}

// fanJudgeSession is the name the judge of a fan runs under, unless taken.
func fanJudgeSession(info *WorktreeInfo) string {
	stem := strings.NewReplacer("/", "-", " ", "-", ":", "-").Replace(info.Group)
	return info.Repo + "-" + stem + "-judge"
}

// fanJudgePrompt is the judge's first prompt: the task, the attempts by
// letter, and how to read them and report.
func fanJudgePrompt(info *WorktreeInfo, siblings []*Session) string {
	var b strings.Builder
	b.WriteString("You judge a fan of ")
	b.WriteString(strconv.Itoa(len(siblings)))
	b.WriteString(" attempts at one task. Do not change any file.")
	if task := strings.TrimSpace(info.Prompt); task != "" {
		b.WriteString(" The task was: ")
		b.WriteString(strings.Join(strings.Fields(task), " "))
	}
	b.WriteString(" The attempts are the sessions")
	for i, s := range siblings {
		sep := ","
		if i == 0 {
			sep = ""
		}
		b.WriteString(sep)
		b.WriteString(" ")
		b.WriteString(string(rune('A' + i%26)))
		b.WriteString(" ")
		b.WriteString(s.Name())
	}
	first := siblings[0].Name()
	b.WriteString(". Read them with: tuios fan compare ")
	b.WriteString(first)
	b.WriteString(" --json, which gives each attempt's folder, and git -C FOLDER diff in each. tuios lets you read them and nothing more.")
	b.WriteString(" Then report once with: tuios fan verdict ")
	b.WriteString(first)
	b.WriteString(" --pick SESSION --summary \"the verdict in one or two sentences\" --reason \"one reason\" --note \"SESSION=a note on that attempt\". Repeat --reason and --note as needed.")
	return b.String()
}
