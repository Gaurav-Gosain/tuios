package session

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

// A fan's sessions reach each other, so joining a fan is never implicit.
// Security boundary test: each case is a way a caller could put a session
// in a fan it is not part of.

// fanJoinRig places each connection in the pane it was first dialed for, so
// calls on several connections can run at once.
type fanJoinRig struct {
	t     *testing.T
	d     *Daemon
	sp    string
	repo  string
	mu    sync.Mutex
	cur   string
	conns map[*connState]string
}

func newFanJoinRig(t *testing.T) *fanJoinRig {
	if runtime.GOOS == "windows" {
		t.Skip("the agents here are POSIX programs")
	}
	d, sp, repo := worktreeFixture(t)
	r := &fanJoinRig{t: t, d: d, sp: sp, repo: repo, conns: map[*connState]string{}}
	d.setApprovalPeer(func(cs *connState) (bool, string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		w, ok := r.conns[cs]
		if !ok {
			w = r.cur
			r.conns[cs] = w
		}
		return w != "", w
	})
	return r
}

// as dials a connection placed in window, "" for the person.
func (r *fanJoinRig) as(window string) *verbConn {
	r.mu.Lock()
	r.cur = window
	r.mu.Unlock()
	c := dialVerb(r.t, r.sp)
	callP(c, r.t, "pane-grants", map[string]any{})
	return c
}

func (r *fanJoinRig) win(name string) string {
	return r.d.manager.GetSession(name).GetState().Windows[0].ID
}

func (r *fanJoinRig) fan(c *verbConn, name, join string) map[string]any {
	p := map[string]any{"repo": r.repo, "count": 1, "agent": "sleep 60", "prompt": "x", "ready_timeout": 300}
	if name != "" {
		p["name"] = name
	}
	if join != "" {
		p["join"] = join
	}
	return callP(c, r.t, "fan", p)
}

func fanMade(res map[string]any) []string {
	out := []string{}
	rr, _ := res["result"].(map[string]any)
	ss, _ := rr["sessions"].([]any)
	for _, s := range ss {
		out = append(out, s.(map[string]any)["session"].(string))
	}
	return out
}

func TestFanRefusesJoiningAnotherGroup(t *testing.T) {
	r := newFanJoinRig(t)
	admin := r.as("")
	lead := makeSessionWithWindow(t, r.d, "lead")
	other := makeSessionWithWindow(t, r.d, "other")
	mine := fakeFan(t, r.d, admin, r.repo, "fan/retry", 2, "lead")
	setStrict(r.d, "read", "fan")
	group := func() int { return len(r.d.fanSiblings(r.d.manager.GetSession(mine[0]).Worktree())) }

	// The fan's name with no join is refused for everyone, the person too.
	for who, w := range map[string]string{"another session's pane": r.win(other.Name()), "the fan's lead": r.win(lead.Name()), "the person": ""} {
		res := r.fan(r.as(w), "fan/retry", "")
		if code := errCode(t, res); code != ErrVerbFanExists {
			t.Fatalf("%s fanned the taken name with no join: %v", who, res)
		}
	}
	// join from a pane that does not reach the fan is refused.
	wantForbidden(t, "join from another session's pane", r.fan(r.as(r.win(other.Name())), "", mine[0]))
	if n := group(); n != 2 {
		t.Fatalf("the group grew to %d sessions", n)
	}
	// join from the lead, a pane of the fan and the person adds attempts.
	for who, w := range map[string]string{"the fan's lead": r.win(lead.Name()), "a pane of the fan": r.win(mine[1]), "the person": ""} {
		if res := r.fan(r.as(w), "", mine[0]); res["error"] != nil {
			t.Fatalf("%s could not join on purpose: %v", who, res["error"])
		}
	}
	if n := group(); n != 5 {
		t.Fatalf("the group has %d sessions, want 5", n)
	}

	// A name taken first by an outsider is not joined by the person's fan.
	out := r.as(r.win(other.Name()))
	squat := fanMade(r.fan(out, "fan/add-retry-client", ""))
	if len(squat) != 1 {
		t.Fatal("the outsider could not start a fan of its own")
	}
	if res := r.fan(r.as(""), "fan/add-retry-client", ""); errCode(t, res) != ErrVerbFanExists {
		t.Fatalf("the person's fan joined the outsider's: %v", res)
	}
	// The name fan makes from the prompt moves on to a free one.
	first := fanMade(callP(r.as(""), t, "fan", map[string]any{"repo": r.repo, "count": 1, "agent": "sleep 60", "prompt": "same task", "ready_timeout": 300}))
	second := fanMade(callP(r.as(""), t, "fan", map[string]any{"repo": r.repo, "count": 1, "agent": "sleep 60", "prompt": "same task", "ready_timeout": 300}))
	if len(first) != 1 || len(second) != 1 || r.d.sessionInScope(first[0], second[0]) {
		t.Fatalf("two fans from one prompt are one fan: %v %v", first, second)
	}

	// A name outside A-Z, a-z, 0-9 and / . _ - is refused: characters that
	// do not print, blank letters, and letters that look like Latin ones.
	for _, n := range []string{
		"fan/retry\u200b", "fan/re\u202etry", "fan/ret\u00adry", // zero-width, RLO, soft hyphen
		"fan/retry\u3164", "fan/retry\u2800", "fan/retry\uffa0", // Hangul filler, braille blank, halfwidth filler
		"fan/retry\u20dd", "fan/retry-\u0430", "fan/\uff52etry", // enclosing circle, Cyrillic a, full-width r
		"fan/re\u00a0try", "fan/re try",
	} {
		res := r.fan(r.as(""), n, "")
		if code := errCode(t, res); code != ErrVerbInvalidParams {
			t.Fatalf("the name %q was taken: %s", n, code)
		}
	}
	if made := fanMade(r.fan(r.as(""), "fan/Retry_2.x-y", "")); len(made) != 1 {
		t.Fatal("a plain name was refused")
	}

	// A refusal lists the fan's sessions only to a caller that reaches them.
	secret := fanMade(r.fan(r.as(""), "fan/secret", ""))
	if len(secret) != 1 {
		t.Fatal("the person could not start a fan")
	}
	hint := func(res map[string]any) []any {
		e, _ := res["error"].(map[string]any)
		h, _ := e["hint"].(map[string]any)
		a, _ := h["available"].([]any)
		return a
	}
	if res := r.fan(out, "fan/secret", ""); errCode(t, res) != ErrVerbFanExists || len(hint(res)) != 0 {
		t.Fatalf("the fan_exists refusal listed the fan's sessions to an outsider: %v", res)
	}
	res := callP(out, t, "fan", map[string]any{"repo": r.repo, "name": "fan/secret", "join": squat[0], "count": 1, "agent": "sleep 60", "prompt": "x", "ready_timeout": 300})
	if errCode(t, res) != ErrVerbInvalidParams || len(hint(res)) != 0 {
		t.Fatalf("the join refusal listed the fan's sessions to an outsider: %v", res)
	}
	if res := r.fan(r.as(""), "fan/secret", ""); len(hint(res)) != 1 {
		t.Fatalf("the fan_exists refusal did not list the fan's session to the person: %v", res)
	}
}

// A fan's sessions save its fan ID, and a fan's sessions are the ones that
// share it. A fan made later under the same name, also while the first was
// saved and not running (disable_auto_restore), stays a fan of its own: the
// two never reach each other, in any order of restore and across restarts.
// Security boundary test.

// fanRestoreRig adds saving, restoring and a restart to fanJoinRig.
type fanRestoreRig struct {
	*fanJoinRig
	other *Session
}

func newFanRestoreRig(t *testing.T) *fanRestoreRig {
	r := newFanJoinRig(t)
	other := makeSessionWithWindow(t, r.d, "other")
	setStrict(r.d, "read", "fan")
	return &fanRestoreRig{r, other}
}

// save keeps the sessions' saved states and ends them.
func (r *fanRestoreRig) save(names []string) []*SessionState {
	r.t.Helper()
	var out []*SessionState
	for _, n := range names {
		// Through JSON, as the state file holds it.
		raw, err := json.Marshal(r.d.manager.GetSession(n).ResurrectionState())
		if err != nil {
			r.t.Fatal(err)
		}
		var st SessionState
		if err := json.Unmarshal(raw, &st); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, &st)
		if res := callP(r.as(""), r.t, "kill-session", map[string]any{"session": n}); res["error"] != nil {
			r.t.Fatalf("kill-session %s: %v", n, res["error"])
		}
	}
	return out
}

func (r *fanRestoreRig) restore(st *SessionState) *Session {
	r.t.Helper()
	s, err := r.d.restoreSession(st)
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

// restart drops what the daemon holds in memory about fans.
func (r *fanRestoreRig) restart() {
	r.d.fanReserved.mu.Lock()
	r.d.fanReserved.names = nil
	r.d.fanReserved.mu.Unlock()
}

func (r *fanRestoreRig) personFan(name string, n int) []string {
	r.t.Helper()
	made := fanMade(callP(r.as(""), r.t, "fan", map[string]any{"repo": r.repo, "name": name, "count": n, "agent": "sleep 60", "prompt": "x", "ready_timeout": 300}))
	if len(made) != n {
		r.t.Fatalf("the person could not start %s", name)
	}
	return made
}

func (r *fanRestoreRig) paneFan(name string) string {
	r.t.Helper()
	made := fanMade(r.fan(r.as(r.win(r.other.Name())), name, ""))
	if len(made) != 1 {
		r.t.Fatalf("the pane could not make a fan named %s", name)
	}
	return made[0]
}

// apart fails when a session of pane's fan reaches one of the person's, or
// when the person's do not reach each other.
func (r *fanRestoreRig) apart(pane *Session, person ...*Session) {
	r.t.Helper()
	for _, s := range person {
		if r.d.sessionInScope(pane.Name(), s.Name()) || r.d.sessionInScope(r.other.Name(), s.Name()) {
			r.t.Errorf("the pane's fan reaches the person's restored %s", s.Name())
		}
		if g := s.Worktree().Group; g == "" {
			r.t.Errorf("the person's restored %s lost its fan", s.Name())
		}
		for _, o := range person {
			if o != s && !r.d.sessionInScope(s.Name(), o.Name()) {
				r.t.Errorf("the person's restored %s does not reach %s", s.Name(), o.Name())
			}
		}
	}
	for _, s := range r.d.fanSiblings(pane.Worktree()) {
		if slices.Contains(person, s) {
			r.t.Errorf("the pane's fan lists the person's %s as an attempt", s.Name())
		}
	}
}

func TestRestoredFanStaysItsOwnFan(t *testing.T) {
	r := newFanRestoreRig(t)
	// A pane makes the name while the person's fan is saved; then the
	// person restores it, and joins it.
	states := r.save(r.personFan("fan/saved", 2))
	squat := r.d.manager.GetSession(r.paneFan("fan/saved"))
	a, b := r.restore(states[0]), r.restore(states[1])
	r.apart(squat, a, b)
	added := fanMade(r.fan(r.as(""), "", a.Name()))
	if len(added) != 1 || !r.d.sessionInScope(added[0], b.Name()) || r.d.sessionInScope(added[0], squat.Name()) {
		t.Errorf("a join of the restored fan did not join that fan alone: %v", added)
	}
}

func TestReview984RestoreAcrossRestart(t *testing.T) {
	r := newFanRestoreRig(t)
	// Run 1 ends with the person's fan saved.
	ps := r.save(r.personFan("fan/saved", 2))
	r.restart()
	// Run 2, with no restore: a pane makes the name, and its session is
	// saved when the daemon goes down.
	sq := r.save([]string{r.paneFan("fan/saved")})
	r.restart()
	// Run 3: everything comes back.
	a, b, pane := r.restore(ps[0]), r.restore(ps[1]), r.restore(sq[0])
	r.apart(pane, a, b)
}

func TestReview984RestoreSquatFirst(t *testing.T) {
	r := newFanRestoreRig(t)
	ps := r.save(r.personFan("fan/saved", 1))
	r.restart()
	sq := r.save([]string{r.paneFan("fan/saved")})
	r.restart()
	pane := r.restore(sq[0])
	r.apart(pane, r.restore(ps[0]))
}

// Two calls with one new name at the same time make one fan and refuse the
// other: the name is held from the check until the sessions exist.
func TestFanNameIsHeldWhileItIsMade(t *testing.T) {
	r := newFanJoinRig(t)
	other := makeSessionWithWindow(t, r.d, "other")
	setStrict(r.d, "read", "fan")
	ow := r.win(other.Name())
	for i := range 12 {
		stem := fmt.Sprintf("fan/race%c", 'a'+i)
		person, out := r.as(""), r.as(ow)
		var pres, ores map[string]any
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); pres = r.fan(person, stem, "") }()
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i%6) * 2 * time.Millisecond)
			ores = r.fan(out, stem, "")
		}()
		wg.Wait()
		p, o := fanMade(pres), fanMade(ores)
		if len(p)+len(o) == 0 {
			t.Fatalf("%s: neither call made a fan: %v %v", stem, pres["error"], ores["error"])
		}
		if len(p) == 1 && len(o) == 1 && r.d.sessionInScope(o[0], p[0]) {
			t.Fatalf("%s: the outsider's %s joined the person's %s", stem, o[0], p[0])
		}
	}
}

// The cockpit's new-agent form checks a fan name with FanNameRefOK in its
// update loop, where it cannot run git. Within the characters a fan name
// may have, it must answer as git check-ref-format --branch does.
func TestFanNameRefOKAnswersAsGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	for _, n := range []string{
		"fan/retry", "Fix/ABC", "feat/a.b_c", "a", "HEAD", "head", "HEAD/x", "x/HEAD", "-x", "x-", "/x", "x/",
		"fan//x", "fan/.x", ".x", "x.", "x.lock", "x.lock/y", "x..y", "x/./y", "x/../y", "a.b.c", "_", "x_/y",
		"refs/heads/x", "x.locked",
	} {
		git := exec.Command("git", "check-ref-format", "--branch", n).Run() == nil
		if got := FanNameRefOK(n); got != git {
			t.Errorf("FanNameRefOK(%q) = %v, git says %v", n, got, git)
		}
	}
}
