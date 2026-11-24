//go:build spec

package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// The differential test of pane grants and verb scopes against their Lean
// model (spec/grants). It makes random states and calls from a seed, puts
// each state on a test daemon, runs each call through the checks a JSON verb
// passes (checkGrants, then checkScope, then the checks of the start verbs
// and of set-pane-grants) and through the model's executable, and fails on
// the first call where the two differ, with the seed, the state and the call.
//
//	(cd spec/grants && lake build)
//	go test -tags spec -run TestGrantsMatchLeanModel ./internal/session/
//
// The seam is this file alone: it writes the state into the daemon's own
// records (sessions, windows, the grant table and the config) and places the
// caller with setApprovalPeer, so nothing in the shipped binary changes for
// it. Every random state has the same four sessions of three panes each, so
// the daemon starts its shells once.

var (
	grantCases = flag.Int("grantspec.n", 50000, "number of random states, each with grantCalls calls")
	grantBin   = flag.String("grantspec.bin", "", "the Lean model's executable; empty is spec/grants/.lake/build/bin/grantspec")
	// grantApplyOnly puts each state on the daemon and calls nothing, for
	// spec/grants/go-files.sh: what this run covers is the setup, not the
	// checks.
	grantApplyOnly = flag.Bool("grantspec.apply-only", false, "put each state on the daemon and run no call")
)

const (
	grantSessions = 4
	grantPanes    = 3
	grantCalls    = 8
)

// The model's JSON. Every field is always written.
type (
	gsWorktree struct {
		LaunchedFrom string `json:"launched_from"`
		Group        string `json:"group"`
		Managed      bool   `json:"managed"`
		RepoRoot     string `json:"repo_root"`
		FanID        string `json:"fan_id"`
		JudgeSession string `json:"judge_session"`
		JudgeWindow  string `json:"judge_window"`
	}
	gsSession struct {
		Name     string      `json:"name"`
		ID       string      `json:"id"`
		Focused  string      `json:"focused"`
		Worktree *gsWorktree `json:"worktree"`
	}
	gsLink struct {
		Root  string `json:"root"`
		Depth int    `json:"depth"`
	}
	gsPane struct {
		ID         string   `json:"id"`
		Session    string   `json:"session"`
		StartedBy  string   `json:"started_by"`
		Link       *gsLink  `json:"link"`
		Depth      int      `json:"depth"`
		Grants     []string `json:"grants"`
		NeedsInput bool     `json:"needs_input"`
		Approval   bool     `json:"approval"`
	}
	gsConfig struct {
		Strict   bool     `json:"strict"`
		Grants   []string `json:"grants"`
		MaxDepth int      `json:"max_depth"`
	}
	gsState struct {
		Sessions []gsSession `json:"sessions"`
		Panes    []gsPane    `json:"panes"`
		Config   gsConfig    `json:"config"`
	}
	gsRestriction struct {
		Own      bool `json:"own"`
		ReadOnly bool `json:"read_only"`
	}
	gsCaller struct {
		Pane        string         `json:"pane"`
		Restriction *gsRestriction `json:"restriction"`
	}
	gsTarget struct {
		Session      string   `json:"session"`
		Window       string   `json:"window"`
		From         string   `json:"from"`
		Select       bool     `json:"select"`
		AllSessions  bool     `json:"all_sessions"`
		AllowBlocked bool     `json:"allow_blocked"`
		Grants       []string `json:"grants"`
	}
	gsCall struct {
		Caller gsCaller `json:"caller"`
		Verb   string   `json:"verb"`
		Target gsTarget `json:"target"`
	}
	gsCase struct {
		State gsState  `json:"state"`
		Calls []gsCall `json:"calls"`
	}
	gsAnswer struct {
		Decision string `json:"decision"`
		Child    any    `json:"child"`
	}
	gsReply struct {
		Answers []gsAnswer `json:"answers"`
		Error   string     `json:"error"`
	}
)

// The verbs of the random calls: the verbs the model knows (verbs in
// spec/grants/GrantSpec/Model.lean), at least one of each kind and every verb
// a check names.
var gsVerbs = []string{"hello", "pane-grants", "set-pane-grants", "resolve-pane", "list-sessions", "list-agents",
	"capture-pane", "get-agent-state", "read-agent-messages", "compare-fan", "wait-for",
	"set-agent-state", "ask-human", "request-approval", "send-agent-message", "send-text",
	"send-keys", "run", "respond", "paste-buffer", "start-agent", "set-fan-verdict", "fan",
	"list-buffers", "set-buffer", "add-task", "claim-task", "remove-task", "close-window",
	"kill-session", "set-option", "new-window", "transfer-start", "transfer-list"}

// gsUniverse is the test daemon and the real names of the model's symbols:
// pane pN is window pane[N], session sN is named sN with ID id[N].
type gsUniverse struct {
	d     *Daemon
	sess  []*Session
	pane  []string
	ids   map[string]string // model symbol -> real ID, for panes and session IDs
	names map[string]string // real ID -> model symbol
}

const (
	gsUnknownPane = "px"
	gsUnknownID   = "ix"
)

func newGrantUniverse(t *testing.T) *gsUniverse {
	d, _ := startTestDaemon(t)
	u := &gsUniverse{d: d, ids: map[string]string{}, names: map[string]string{}}
	for i := range grantSessions {
		sess := makeSessionWithWindow(t, d, fmt.Sprintf("s%d", i))
		for range grantPanes - 1 {
			if _, err := sess.AddDaemonWindow("w", nil); err != nil {
				t.Fatal(err)
			}
		}
		// A save re-detects the session's worktree from its shell's
		// directory and replaces the record a case wrote, after the case
		// was applied. The model holds the record as written.
		stopSessionSaver(sess)
		u.sess = append(u.sess, sess)
		u.ids[fmt.Sprintf("i%d", i)] = sess.ID
		for _, w := range sess.GetState().Windows {
			sym := fmt.Sprintf("p%d", len(u.pane))
			u.pane = append(u.pane, w.ID)
			u.ids[sym] = w.ID
			u.names[w.ID] = sym
			if !d.manager.grants.set(w.ID, nil) {
				t.Fatalf("window %s has no entry in the grant table", w.ID)
			}
		}
	}
	u.ids[gsUnknownPane] = "00000000-0000-4000-8000-000000000000"
	u.ids[gsUnknownID] = "no-such-session-id"
	return u
}

// real is the real name of a model symbol; "" stays "".
func (u *gsUniverse) real(sym string) string {
	if sym == "" {
		return ""
	}
	if r, ok := u.ids[sym]; ok {
		return r
	}
	return sym
}

func gsGrantBits(names []string) *Grants {
	if names == nil {
		return nil
	}
	g := grantsFromNames(names)
	return &g
}

// apply writes the state into the daemon.
func (u *gsUniverse) apply(t *testing.T, s gsState) {
	d := u.d
	d.manager.SetPanePermissions(config.ResolvedPermissions{Strict: s.Config.Strict, Grants: s.Config.Grants})
	d.setAgentMaxDepth(s.Config.MaxDepth)
	byWindow := map[string]gsPane{}
	for _, p := range s.Panes {
		byWindow[u.real(p.ID)] = p
	}
	for i, ss := range s.Sessions {
		var wt *WorktreeInfo
		if w := ss.Worktree; w != nil {
			wt = &WorktreeInfo{LaunchedFromID: u.real(w.LaunchedFrom), Group: w.Group, Managed: w.Managed,
				FanID: w.FanID, JudgeSession: u.real(w.JudgeSession), JudgeWindow: u.real(w.JudgeWindow)}
			wt.RepoRoot = w.RepoRoot
		}
		err := u.sess[i].mutateState(func(st *SessionState) error {
			st.Worktree = wt
			st.FocusedWindowID = u.real(ss.Focused)
			for j := range st.Windows {
				w := &st.Windows[j]
				p := byWindow[w.ID]
				w.StartedBy, w.AgentLink = nil, nil
				if p.StartedBy != "" {
					w.StartedBy = &PaneRef{Session: ss.Name, Window: u.real(p.StartedBy)}
				}
				if l := p.Link; l != nil {
					w.AgentLink = &AgentLink{Root: PaneRef{Session: ss.Name, Window: u.real(l.Root)}, Depth: l.Depth}
				}
				w.ChainDepth = p.Depth
				w.AgentState, w.AgentKind = AgentStateIdle, ""
				if p.NeedsInput {
					w.AgentState, w.AgentKind = AgentStateNeedsInput, "question"
					if p.Approval {
						w.AgentKind = "approval"
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	u.applyGrants(t, s)
}

// applyGrants writes the grant table, which set-pane-grants changes.
func (u *gsUniverse) applyGrants(t *testing.T, s gsState) {
	for _, p := range s.Panes {
		if !u.d.manager.grants.set(u.real(p.ID), gsGrantBits(p.Grants)) {
			t.Fatalf("window %s has no entry in the grant table", p.ID)
		}
	}
}

// gsDecision names a refusal the way the model does.
func gsDecision(verr *verbError) string {
	switch {
	case verr == nil:
		return "allow"
	case verr.Code == ErrVerbForbidden:
		return "forbidden"
	case verr.Code == ErrVerbInvalidParams:
		return "invalid"
	case verr.Code == ErrVerbAgentBlocked:
		return "blocked"
	}
	return "error"
}

// params is the call's JSON params with real names.
func (u *gsUniverse) params(c gsCall) json.RawMessage {
	m := map[string]any{}
	t := c.Target
	if t.Session != "" {
		m["session"] = t.Session
	}
	if t.Window != "" {
		m["window"] = u.real(t.Window)
	}
	if t.From != "" {
		m["from"] = u.real(t.From)
	}
	if t.Select {
		m["select"] = "state:idle"
	}
	if t.AllSessions {
		m["all_sessions"] = true
	}
	if t.AllowBlocked {
		m["allow_blocked"] = true
	}
	if c.Verb == "set-pane-grants" {
		if t.Grants == nil {
			m["reset"] = true
		} else {
			m["grants"] = gsGrantParam(t.Grants)
		}
	}
	raw, _ := json.Marshal(m)
	return raw
}

// gsGrantParam is a grants parameter: none for the empty set.
func gsGrantParam(names []string) []string {
	if len(names) == 0 {
		return []string{grantNone}
	}
	return names
}

// run answers one call on the Go side.
func (u *gsUniverse) run(t *testing.T, s gsState, c gsCall) gsAnswer {
	d := u.d
	window := u.real(c.Caller.Pane)
	d.setApprovalPeer(func(*connState) (bool, string) { return window != "", window })
	// A peer pid lets callerSession place the caller as it does a real
	// process. The daemon's own pid is never read as a process in a pane.
	cs := &connState{clientID: "grantspec", peerPID: os.Getpid()}
	if r := c.Caller.Restriction; r != nil {
		sc := &connScope{own: r.Own, readOnly: r.ReadOnly}
		if window != "" {
			if sess := d.sessionHoldingWindow(window); sess != nil {
				sc.sessionID, sc.window, sc.via = sess.ID, window, "pid"
			}
		}
		cs.scope.Store(sc)
	}
	params := u.params(c)
	out, verr := d.checkGrants(cs, c.Verb, params)
	if verr == nil {
		out, verr = d.checkScope(cs, c.Verb, out)
	}
	if verr != nil {
		return gsAnswer{Decision: gsDecision(verr)}
	}
	switch c.Verb {
	case "start-agent", "fan":
		var req []string
		if c.Target.Grants != nil {
			req = gsGrantParam(c.Target.Grants)
		}
		g, verr := d.launchGrants(cs, req)
		if verr == nil {
			verr = d.depthRefusal(cs, c.Verb, nil)
		}
		if verr != nil {
			return gsAnswer{Decision: gsDecision(verr)}
		}
		if g == nil {
			return gsAnswer{Decision: "allow", Child: "default"}
		}
		return gsAnswer{Decision: "allow", Child: g.Names()}
	case "set-pane-grants":
		_, verr := d.verbSetPaneGrants(cs, out)
		u.applyGrants(t, s)
		return gsAnswer{Decision: gsDecision(verr)}
	}
	return gsAnswer{Decision: "allow"}
}

func gsPickGrants(r *rand.Rand) []string {
	out := []string{}
	for _, n := range config.PaneGrantNames {
		// admin less often, so the rules below it are reached.
		if n == config.PaneGrantAdmin && r.IntN(4) != 0 {
			continue
		}
		if r.IntN(2) == 0 {
			out = append(out, n)
		}
	}
	return out
}

func gsGen(r *rand.Rand) gsCase {
	pick := func(xs ...string) string { return xs[r.IntN(len(xs))] }
	nPanes := grantSessions * grantPanes
	pane := func() string { return fmt.Sprintf("p%d", r.IntN(nPanes)) }
	sid := func() string { return fmt.Sprintf("i%d", r.IntN(grantSessions)) }
	sname := func() string { return fmt.Sprintf("s%d", r.IntN(grantSessions)) }

	var s gsState
	s.Config = gsConfig{Strict: r.IntN(3) != 0, Grants: gsPickGrants(r)}
	s.Config.MaxDepth = []int{0, 0, 1, 2, 3, 9}[r.IntN(6)]
	for i := range grantSessions {
		ss := gsSession{Name: fmt.Sprintf("s%d", i), ID: fmt.Sprintf("i%d", i), Focused: fmt.Sprintf("p%d", i*grantPanes+r.IntN(grantPanes))}
		if r.IntN(4) != 0 {
			w := &gsWorktree{Group: pick("", "g", "g"), Managed: r.IntN(3) != 0, RepoRoot: pick("r1", "r1", "r2"), FanID: pick("", "", "f")}
			if r.IntN(3) == 0 {
				w.LaunchedFrom = pick(sid(), gsUnknownID)
			}
			if r.IntN(4) == 0 {
				w.JudgeSession, w.JudgeWindow = pick(sid(), gsUnknownID), pick(pane(), pane(), "")
			}
			ss.Worktree = w
		}
		s.Sessions = append(s.Sessions, ss)
	}
	for i := range nPanes {
		p := gsPane{ID: fmt.Sprintf("p%d", i), Session: fmt.Sprintf("s%d", i/grantPanes), Depth: r.IntN(5),
			NeedsInput: r.IntN(4) == 0, Approval: r.IntN(2) == 0}
		if r.IntN(2) == 0 {
			p.StartedBy = pick(pane(), pane(), pane(), gsUnknownPane)
		}
		if r.IntN(3) == 0 {
			p.Link = &gsLink{Root: pick(pane(), pane(), ""), Depth: r.IntN(4)}
		}
		if r.IntN(2) == 0 {
			p.Grants = gsPickGrants(r)
		}
		s.Panes = append(s.Panes, p)
	}

	calls := make([]gsCall, grantCalls)
	for i := range calls {
		c := gsCall{Verb: gsVerbs[r.IntN(len(gsVerbs))]}
		if r.IntN(5) != 0 {
			c.Caller.Pane = pane()
		}
		if r.IntN(3) == 0 {
			c.Caller.Restriction = &gsRestriction{Own: r.IntN(3) != 0, ReadOnly: r.IntN(2) == 0}
		}
		t := &c.Target
		if r.IntN(3) != 0 {
			t.Session = pick(sname(), sname(), sname(), "nope")
		}
		if r.IntN(4) != 0 {
			t.Window = pick(pane(), pane(), pane(), gsUnknownPane)
		}
		if r.IntN(4) == 0 {
			t.From = pane()
		}
		t.Select = r.IntN(8) == 0
		t.AllSessions = r.IntN(10) == 0
		t.AllowBlocked = r.IntN(3) == 0
		if r.IntN(2) == 0 {
			t.Grants = gsPickGrants(r)
		}
		// From outside every pane, set-pane-grants with no session goes to
		// the most recently active one, which the model does not know.
		if c.Verb == "set-pane-grants" && c.Caller.Pane == "" && t.Session == "" {
			t.Session = sname()
		}
		calls[i] = c
	}
	return gsCase{State: s, Calls: calls}
}

func gsEncode(c gsCase) []byte {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

func gsRead(m *leanModel) (gsReply, error) {
	line, err := m.out.ReadBytes('\n')
	if err != nil {
		_ = m.cmd.Process.Kill()
		m.wait()
		return gsReply{}, fmt.Errorf("read the Lean model: %w: %s", err, m.stderr.Bytes())
	}
	var a gsReply
	if err := json.Unmarshal(line, &a); err != nil {
		return gsReply{}, fmt.Errorf("decode the Lean model's line %q: %w", line, err)
	}
	if a.Error != "" {
		return a, fmt.Errorf("the Lean model refused a case: %s", a.Error)
	}
	return a, nil
}

func gsSame(a, b gsAnswer) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// gsModelBin is the Lean model's executable.
func gsModelBin(t *testing.T) string {
	if *grantBin != "" {
		return *grantBin
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(filepath.Dir(filepath.Dir(wd)), "spec", "grants", ".lake", "build", "bin", "grantspec")
}

// gsRegressions are cases a random run found, each with the decision both
// sides must give.
var gsRegressions = []struct {
	name string
	want string
	edit func(*gsCase)
	json string
}{
	{
		// s0 was launched from s1, so p3 in s1 reads s0 with the read grant.
		// A save that re-detected s0's directory replaced its managed record
		// with a detected one, and the Go side refused.
		name: "a pane reads the session its own session launched",
		want: "allow",
		json: gsLaunchedFromCase,
	},
	{
		// The negative control: without the launcher, s0 is out of reach.
		name: "a pane reads a session its own session did not launch",
		want: "forbidden",
		edit: func(c *gsCase) { c.State.Sessions[0].Worktree.LaunchedFrom = "" },
		json: gsLaunchedFromCase,
	},
}

// gsLaunchedFromCase is seed 1795541135002539675, case 20400, call 5.
const gsLaunchedFromCase = `{"state":{"sessions":[{"name":"s0","id":"i0","focused":"p2","worktree":{"launched_from":"i1","group":"g","managed":true,"repo_root":"r2","fan_id":"","judge_session":"ix","judge_window":"p5"}},{"name":"s1","id":"i1","focused":"p4","worktree":null},{"name":"s2","id":"i2","focused":"p7","worktree":{"launched_from":"i0","group":"","managed":true,"repo_root":"r1","fan_id":"f","judge_session":"","judge_window":""}},{"name":"s3","id":"i3","focused":"p9","worktree":{"launched_from":"","group":"g","managed":false,"repo_root":"r2","fan_id":"","judge_session":"","judge_window":""}}],"panes":[{"id":"p0","session":"s0","started_by":"p2","link":null,"depth":0,"grants":["read"],"needs_input":false,"approval":true},{"id":"p1","session":"s0","started_by":"p8","link":null,"depth":1,"grants":null,"needs_input":false,"approval":false},{"id":"p2","session":"s0","started_by":"p6","link":{"root":"p5","depth":0},"depth":3,"grants":["task"],"needs_input":false,"approval":false},{"id":"p3","session":"s1","started_by":"","link":null,"depth":3,"grants":["read","write","task"],"needs_input":false,"approval":false},{"id":"p4","session":"s1","started_by":"p11","link":{"root":"p6","depth":3},"depth":2,"grants":null,"needs_input":false,"approval":false},{"id":"p5","session":"s1","started_by":"","link":null,"depth":3,"grants":null,"needs_input":false,"approval":false},{"id":"p6","session":"s2","started_by":"px","link":null,"depth":3,"grants":null,"needs_input":true,"approval":false},{"id":"p7","session":"s2","started_by":"p5","link":null,"depth":4,"grants":null,"needs_input":false,"approval":false},{"id":"p8","session":"s2","started_by":"px","link":null,"depth":2,"grants":["respond","task"],"needs_input":false,"approval":false},{"id":"p9","session":"s3","started_by":"","link":null,"depth":1,"grants":["fan","respond","files"],"needs_input":true,"approval":true},{"id":"p10","session":"s3","started_by":"p5","link":{"root":"","depth":0},"depth":2,"grants":null,"needs_input":false,"approval":false},{"id":"p11","session":"s3","started_by":"px","link":null,"depth":3,"grants":null,"needs_input":false,"approval":true}],"config":{"strict":true,"grants":["read","respond","files","task"],"max_depth":1}},"calls":[{"caller":{"pane":"p3","restriction":null},"verb":"capture-pane","target":{"session":"s0","window":"px","from":"","select":false,"all_sessions":false,"allow_blocked":true,"grants":["files"]}}]}`

func TestGrantsLeanRegressions(t *testing.T) {
	u := newGrantUniverse(t)
	model := startLeanModel(t, gsModelBin(t))
	for _, rc := range gsRegressions {
		t.Run(rc.name, func(t *testing.T) {
			var c gsCase
			if err := json.Unmarshal([]byte(rc.json), &c); err != nil {
				t.Fatal(err)
			}
			if rc.edit != nil {
				rc.edit(&c)
			}
			if _, err := model.in.Write(gsEncode(c)); err != nil {
				t.Fatal(err)
			}
			reply, err := gsRead(model)
			if err != nil {
				t.Fatal(err)
			}
			if len(reply.Answers) != len(c.Calls) {
				t.Fatalf("the model answered %d calls of %d", len(reply.Answers), len(c.Calls))
			}
			// The order CI ran in: a save starts on a state where s0 has no
			// record, and the case is applied while it detects s0's
			// directory.
			before := c.State
			before.Sessions = slices.Clone(c.State.Sessions)
			before.Sessions[0].Worktree = nil
			u.apply(t, before)
			wd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			detect := detectWorktree
			defer func() { detectWorktree = detect }()
			detectWorktree = func(dir string) (worktree.Info, bool, bool) {
				detectWorktree = detect
				u.apply(t, c.State)
				return detect(dir)
			}
			u.sess[0].refreshWorktree(wd)
			for j, call := range c.Calls {
				got, lean := u.run(t, c.State, call), reply.Answers[j]
				if got.Decision != rc.want || lean.Decision != rc.want {
					t.Errorf("call %d: go %+v, lean %+v, want %s", j, got, lean, rc.want)
				}
			}
		})
	}
}

func TestGrantsMatchLeanModel(t *testing.T) {
	bin := gsModelBin(t)
	seed := *specSeed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	t.Logf("seed %d, %d states of %d calls (rerun with -spec.seed=%d)", seed, *grantCases, grantCalls, seed)
	start := time.Now()

	u := newGrantUniverse(t)
	model := startLeanModel(t, bin)
	caseOf := func(i int) gsCase { return gsGen(rand.New(rand.NewPCG(seed, uint64(i)))) }

	// A writer feeds the model while the test reads its answers, so the
	// pipe never waits on a round trip. The Go side runs on one daemon, one
	// case at a time.
	go func() {
		bw := bufio.NewWriterSize(model.in, 1<<20)
		for i := range *grantCases {
			if _, err := bw.Write(gsEncode(caseOf(i))); err != nil {
				return
			}
		}
		_ = bw.Flush()
	}()

	// seen counts the answers of each verb by decision, and under "pane"
	// the calls served to a pane without admin, which is where the rules
	// below admin branch.
	seen := map[string]map[string]int{}
	for i := range *grantCases {
		reply, err := gsRead(model)
		if err != nil {
			t.Fatal(err)
		}
		c := caseOf(i)
		if len(reply.Answers) != len(c.Calls) {
			t.Fatalf("case %d: the model answered %d calls of %d", i, len(reply.Answers), len(c.Calls))
		}
		u.apply(t, c.State)
		if *grantApplyOnly {
			continue
		}
		for j, call := range c.Calls {
			got := u.run(t, c.State, call)
			if seen[call.Verb] == nil {
				seen[call.Verb] = map[string]int{}
			}
			seen[call.Verb][got.Decision]++
			if g, _ := u.d.manager.grants.effective(u.real(call.Caller.Pane)); call.Caller.Pane != "" && !g.Has(GrantAdmin) && got.Decision == "allow" {
				seen[call.Verb]["pane"]++
			}
			if want := reply.Answers[j]; !gsSame(got, want) {
				_ = model.cmd.Process.Kill()
				one := gsCase{State: c.State, Calls: []gsCall{call}}
				g, _ := json.Marshal(got)
				l, _ := json.Marshal(want)
				t.Fatalf("the grant checks and the Lean model differ: seed %d, case %d, call %d.\ncase: %s\ngo:   %s\nlean: %s",
					seed, i, j, bytes.TrimSpace(gsEncode(one)), g, l)
			}
		}
	}
	t.Logf("%d states in %s", *grantCases, time.Since(start).Round(time.Millisecond))
	verbs := slices.Sorted(func(yield func(string) bool) {
		for v := range seen {
			if !yield(v) {
				return
			}
		}
	})
	for _, v := range verbs {
		c := seen[v]
		t.Logf("%-20s allow %6d (pane %5d)  forbidden %6d  invalid %5d  blocked %5d  error %5d", v, c["allow"], c["pane"], c["forbidden"], c["invalid"], c["blocked"], c["error"])
	}
}
