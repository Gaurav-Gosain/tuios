package session

import (
	"fmt"
	"runtime"
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

	// A name with a character that does not print is refused.
	for _, n := range []string{"fan/retry\u200b", "fan/re\u202etry", "fan/ret\u00adry"} {
		if code := errCode(t, r.fan(r.as(""), n, "")); code != ErrVerbInvalidParams {
			t.Fatalf("the name %q was taken: %s", n, code)
		}
	}
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
