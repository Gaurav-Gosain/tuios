package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func TestGrantsAdminImpliesAllButRespond(t *testing.T) {
	admin := GrantAdmin
	for _, g := range []Grants{GrantRead, GrantWrite, GrantFan, GrantAdmin} {
		if !admin.Has(g) {
			t.Errorf("admin does not hold %s", g)
		}
	}
	if admin.Has(GrantRespond) {
		t.Error("admin holds respond; only the person gives respond")
	}
	if !admin.Covers(GrantRead | GrantWrite | GrantFan) {
		t.Error("admin cannot give read, write and fan")
	}
	if admin.Covers(GrantRespond) {
		t.Error("admin can give respond")
	}
	if (GrantRead | GrantWrite).Covers(GrantAdmin) {
		t.Error("read and write can give admin")
	}
	if got := (GrantRead | GrantFan).String(); got != "read,fan" {
		t.Errorf("String = %q, want read,fan", got)
	}
	if got := Grants(0).String(); got != "none" {
		t.Errorf("empty String = %q, want none", got)
	}
}

func TestParseGrantsParam(t *testing.T) {
	g, verr := parseGrantsParam([]string{"Write", "read", "read"})
	if verr != nil || g != GrantRead|GrantWrite {
		t.Errorf("parse = %v, %v; want read,write", g, verr)
	}
	if g, verr := parseGrantsParam([]string{"none"}); verr != nil || g != 0 {
		t.Errorf("none = %v, %v; want the empty set", g, verr)
	}
	if _, verr := parseGrantsParam([]string{"read", "root"}); verr == nil || verr.Code != ErrVerbInvalidParams {
		t.Errorf("an unknown grant was accepted: %v", verr)
	}
	if _, verr := parseGrantsParam([]string{}); verr == nil {
		t.Error("an empty list was accepted; none must be said")
	}
}

func TestPermissionsConfigFailsTowardStrict(t *testing.T) {
	cases := []struct {
		cfg    config.PermissionsConfig
		strict bool
		grants []string
	}{
		{config.PermissionsConfig{}, false, config.DefaultStrictGrants},
		{config.PermissionsConfig{Mode: "open"}, false, config.DefaultStrictGrants},
		{config.PermissionsConfig{Mode: "strict"}, true, config.DefaultStrictGrants},
		{config.PermissionsConfig{Mode: "stirct"}, true, config.DefaultStrictGrants},
		{config.PermissionsConfig{Mode: "strict", Grants: []string{"read", "bogus"}}, true, []string{"read"}},
		{config.PermissionsConfig{Mode: "strict", Grants: []string{}}, true, []string{}},
	}
	for _, c := range cases {
		r := c.cfg.Resolve()
		if r.Strict != c.strict || !slices.Equal(r.Grants, c.grants) {
			t.Errorf("%+v resolved to %+v, want strict=%v grants=%v", c.cfg, r, c.strict, c.grants)
		}
	}
	res := config.ValidateConfig(&config.UserConfig{Agents: config.AgentsConfig{Permissions: config.PermissionsConfig{Mode: "stirct", Grants: []string{"root"}}}})
	var fields []string
	for _, w := range res.Warnings {
		if w.Field == "agents.permissions" {
			fields = append(fields, w.Key)
		}
	}
	if !slices.Contains(fields, "mode") || !slices.Contains(fields, "grants") {
		t.Errorf("warnings for agents.permissions = %v, want mode and grants", fields)
	}
}

// TestPermissionsReachTheDaemonAndFollowTheFile: the table is read at start
// by every starter, and a change to the file that narrows reaches panes on
// the default. One that widens waits for a restart
// (TestAPermissionReloadOnlyTightens).
func TestPermissionsReachTheDaemonAndFollowTheFile(t *testing.T) {
	uc := &config.UserConfig{Agents: config.AgentsConfig{Permissions: config.PermissionsConfig{Mode: "strict", Grants: []string{"read"}}}}
	cfg := DaemonConfigFromUser(uc)
	if !cfg.Permissions.Strict || !slices.Equal(cfg.Permissions.Grants, []string{"read"}) {
		t.Fatalf("DaemonConfigFromUser carried %+v, want strict read", cfg.Permissions)
	}
	d, _, a1, _, _ := scopeFixture(t)
	d.onConfigReload(uc, nil)
	if g, explicit := d.manager.grants.effective(a1); g != GrantRead || explicit {
		t.Errorf("after the reload a pane on the default holds %v (explicit %v), want read", g, explicit)
	}
	d.onConfigReload(&config.UserConfig{}, nil)
	if g, _ := d.manager.grants.effective(a1); g != GrantRead {
		t.Errorf("after the table was removed a pane holds %v, want read until a restart", g)
	}
}

func setStrict(d *Daemon, grants ...string) {
	if grants == nil {
		grants = config.DefaultStrictGrants
	}
	d.manager.SetPanePermissions(config.ResolvedPermissions{Strict: true, Grants: grants})
}

func TestFanGrantReachesTheFanGroup(t *testing.T) {
	d, sp, a1, _, b1 := scopeFixture(t)
	mustSetWorktree(t, d, "b", &WorktreeInfo{LaunchedFrom: "a"})
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })

	setStrict(d, "read", "write")
	c := dialVerb(t, sp)
	// read reaches the group, write does not.
	result(t, callP(c, t, "capture-pane", map[string]any{"session": "b", "window": b1}))
	resp := callP(c, t, "send-text", map[string]any{"session": "b", "window": b1, "text": "x"})
	wantForbidden(t, "write into the group without fan", resp)
	if msg := resp["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "fan grant") {
		t.Errorf("refusal %q does not name the fan grant", msg)
	}

	setStrict(d, "read", "fan")
	result(t, callP(c, t, "send-text", map[string]any{"session": "b", "window": b1, "text": "x"}))
	// fan alone does not write into the pane's own session.
	wantForbidden(t, "own session with fan and no write", callP(c, t, "send-text", map[string]any{"session": "a", "window": a1, "text": "x"}))
}

func TestExplicitGrantsHoldEvenUnderOpen(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	set := result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a1, "grants": []string{"read"}}))
	if set["explicit"] != true || set["previous_explicit"] != false {
		t.Errorf("set-pane-grants = %v", set)
	}
	if st := d.manager.GetSession("a").GetState(); !slices.Equal(st.Windows[0].Grants, []string{"read"}) {
		t.Errorf("the window's recorded grants = %v, want [read]", st.Windows[0].Grants)
	}
	rows := result(t, callP(person, t, "list-windows", map[string]any{"session": "a"}))["windows"].([]any)
	for _, r := range rows {
		row := r.(map[string]any)
		g, has := row["grants"]
		switch {
		case row["window_id"] == a1 && (!has || len(g.([]any)) != 1 || g.([]any)[0] != "read"):
			t.Errorf("list-windows row of a1 = %v, want grants [read]", row)
		case row["window_id"] != a1 && has:
			t.Errorf("list-windows row of a pane on the default carries grants %v", g)
		}
	}

	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	wantForbidden(t, "send-text from a read pane", callP(c, t, "send-text", map[string]any{"window": a2, "text": "x"}))
	wantForbidden(t, "list-sessions from a read pane", callP(c, t, "list-sessions", nil))

	// Another pane still holds the open default.
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a2 })
	other := dialVerb(t, sp)
	result(t, callP(other, t, "send-text", map[string]any{"session": "b", "window": b1, "text": "x"}))
}

func TestAPaneCannotWidenItself(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	setStrict(d, "read", "write")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	wantForbidden(t, "widening to admin", callP(c, t, "set-pane-grants", map[string]any{"grants": []string{"admin"}}))
	wantForbidden(t, "giving itself respond", callP(c, t, "set-pane-grants", map[string]any{"grants": []string{"read", "respond"}}))
	wantForbidden(t, "changing another pane", callP(c, t, "set-pane-grants", map[string]any{"window": a2, "grants": []string{"read"}}))
	// Narrowing itself is allowed and sticks.
	result(t, callP(c, t, "set-pane-grants", map[string]any{"grants": []string{"read"}}))
	wantForbidden(t, "send-text after narrowing", callP(c, t, "send-text", map[string]any{"window": a2, "text": "x"}))
	// And it cannot come back, not even to the default it came from.
	wantForbidden(t, "reset to the default", callP(c, t, "set-pane-grants", map[string]any{"reset": true}))
	got := result(t, callP(c, t, "pane-grants", nil))
	if g := got["grants"].([]any); len(g) != 1 || g[0] != "read" || got["explicit"] != true {
		t.Errorf("pane-grants after narrowing = %v", got)
	}

	// An admin pane may not give respond either.
	setStrict(d, "admin")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a2 })
	admin := dialVerb(t, sp)
	// b is the most recently active session; a pane's call that names none
	// still means its own.
	d.manager.GetSession("b").TouchActive()
	wantForbidden(t, "admin giving respond", callP(admin, t, "set-pane-grants", map[string]any{"window": a1, "grants": []string{"respond"}}))
	result(t, callP(admin, t, "set-pane-grants", map[string]any{"window": a1, "grants": []string{"read", "write"}}))
}

func TestLaunchGrantsNeverWiden(t *testing.T) {
	d, _, a1, _, _ := scopeFixture(t)
	cs := &connState{}
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	setStrict(d, "read", "fan")

	g, verr := d.launchGrants(cs, nil)
	if verr != nil || g == nil || *g != GrantRead|GrantFan {
		t.Errorf("a pane without admin naming nothing gives %v, %v; want its own read,fan", g, verr)
	}
	if _, verr := d.launchGrants(cs, []string{"write"}); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("a read,fan pane gave write: %v", verr)
	}
	if g, verr := d.launchGrants(cs, []string{"read"}); verr != nil || *g != GrantRead {
		t.Errorf("a read,fan pane giving read = %v, %v", g, verr)
	}

	setStrict(d, "admin")
	cs = &connState{}
	if g, verr := d.launchGrants(cs, nil); verr != nil || g != nil {
		t.Errorf("an admin pane naming nothing gives %v, %v; want the default (nil)", g, verr)
	}
	if _, verr := d.launchGrants(cs, []string{"respond"}); verr == nil {
		t.Error("an admin pane gave respond")
	}

	// The person gives anything; a link may not give respond.
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	if g, verr := d.launchGrants(&connState{}, []string{"respond", "read"}); verr != nil || *g != GrantRespond|GrantRead {
		t.Errorf("the person giving respond,read = %v, %v", g, verr)
	}
	if _, verr := d.launchGrants(&connState{viaLink: true}, []string{"respond"}); verr == nil {
		t.Error("a link gave respond")
	}
}

func TestNewWindowStartsWithItsGrants(t *testing.T) {
	d, sp, _, _, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	c := dialVerb(t, sp)
	res := result(t, callP(c, t, "new-window", map[string]any{"session": "a", "focus": false, "grants": []string{"none"}}))
	id := res["window_id"].(string)
	if g, explicit := d.manager.grants.effective(id); g != 0 || !explicit {
		t.Errorf("the new pane holds %v (explicit %v), want nothing", g, explicit)
	}
	var w WindowState
	for _, x := range d.manager.GetSession("a").GetState().Windows {
		if x.ID == id {
			w = x
		}
	}
	if !slices.Equal(w.Grants, []string{"none"}) {
		t.Errorf("recorded grants = %v, want [none] so an empty set survives JSON and gob", w.Grants)
	}
	d.setApprovalPeer(func(*connState) (bool, string) { return true, id })
	p := dialVerb(t, sp)
	wantForbidden(t, "a pane started with no grants reading", callP(p, t, "list-windows", nil))
}

func TestPaneGrantsEnvAndTableFollowTheProcess(t *testing.T) {
	m := NewManager()
	sess, err := m.CreateSession("env", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Stop)
	g := GrantRead | GrantWrite
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Grants: &g}, nil)
	if err != nil {
		t.Fatal(err)
	}
	env := sess.buildEnv(win.ID, false)
	if !slices.Contains(env, "TUIOS_PANE_GRANTS=read,write") {
		t.Errorf("env has no TUIOS_PANE_GRANTS=read,write: %v", env)
	}
	if got := sess.buildEnv("unknown", false); !slices.Contains(got, "TUIOS_PANE_GRANTS=admin") {
		t.Error("a pane given nothing does not say it holds the open default")
	}

	// A new process in the window with no grants named keeps the window's.
	m.grants.add(win.ID, "second-pty", "env", nil)
	if got, explicit := m.grants.effective(win.ID); got != g || !explicit {
		t.Errorf("after a second process the window holds %v (explicit %v), want read,write", got, explicit)
	}
	// The first process exiting does not drop the second's entry.
	m.grants.remove(win.ID, win.PTYID)
	if _, ok := m.grants.lookup(win.ID); !ok {
		t.Error("the first process's exit removed the window")
	}
	m.grants.remove(win.ID, "second-pty")
	if _, ok := m.grants.lookup(win.ID); ok {
		t.Error("the window stayed after its process exited")
	}
	if m.grants.mayMatter() {
		t.Error("the table still counts an explicit pane after it left")
	}

	// A new process in the window after the last one left still holds what
	// the window's record says it was given, not the default.
	if _, err := sess.CreatePTY(win.ID, 80, 24, nil); err != nil {
		t.Fatal(err)
	}
	if got, explicit := m.grants.effective(win.ID); got != g || !explicit {
		t.Errorf("a respawned process holds %v (explicit %v), want read,write from the record", got, explicit)
	}
}

func TestPaneTokenPlacesAConnectionTheKernelCannot(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	setStrict(d, "read")
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	c := dialVerb(t, sp)
	wantForbidden(t, "a wrong token", callP(c, t, "pane-grants", map[string]any{"pane_id": a1, "pane_token": "00"}))
	got := result(t, callP(c, t, "pane-grants", map[string]any{"pane_id": a1, "pane_token": d.manager.PaneToken(a1)}))
	if got["pane"] != true || got["via"] != "token" || got["window"] != a1 {
		t.Fatalf("pane-grants with a token = %v, want the pane by token", got)
	}
	wantForbidden(t, "capture of b once placed", callP(c, t, "capture-pane", map[string]any{"session": "b", "window": b1}))
	wantForbidden(t, "send-text once placed", callP(c, t, "send-text", map[string]any{"window": a2, "text": "x"}))
	wantForbidden(t, "a second pane's token", callP(c, t, "pane-grants", map[string]any{"pane_id": a2, "pane_token": d.manager.PaneToken(a2)}))
}

// TestTheClientPresentsItsPaneWhereTheKernelCannot: on a platform with no
// peer pid, the CLI's connection presents the pane's token, so a call from a
// pane is held there too. Where the kernel places the caller it sends nothing.
func TestTheClientPresentsItsPaneWhereTheKernelCannot(t *testing.T) {
	d, sp, a1, _, b1 := scopeFixture(t)
	setStrict(d, "read")
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	env := map[string]string{"TUIOS_PANE_ID": a1, "TUIOS_PANE_TOKEN": d.manager.PaneToken(a1)}
	getenv := func(k string) string { return env[k] }

	kernel, err := DialVerbClientAt(sp, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = kernel.Close() }()
	if !kernel.Daemon().PaneGrants {
		t.Fatal("the daemon's hello does not say it takes pane grants")
	}
	kernel.presentPaneToken(true, getenv)
	if _, err := kernel.Call("capture-pane", map[string]any{"session": "b", "window": b1}); err != nil {
		t.Errorf("a client that presented nothing was held: %v", err)
	}

	c, err := DialVerbClientAt(sp, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	c.presentPaneToken(false, getenv)
	_, err = c.Call("capture-pane", map[string]any{"session": "b", "window": b1})
	var callErr *VerbCallError
	if !errors.As(err, &callErr) || callErr.Code != ErrVerbForbidden {
		t.Errorf("capture of b after presenting a read pane's token = %v, want forbidden", err)
	}
}

func TestBinaryProtocolNeedsAdmin(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	setStrict(d)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	conn, err := net.DialTimeout("unix", sp, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	msg, err := NewMessage(MsgList, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(conn, msg); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := ReadMessage(conn)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Type != MsgError {
		t.Fatalf("a pane without admin listed sessions over the client protocol: reply type %d", resp.Type)
	}
	var e ErrorPayload
	if err := resp.ParsePayload(&e); err != nil || e.Code != ErrCodeForbidden || !strings.Contains(e.Message, "admin") {
		t.Errorf("error = %+v, %v; want forbidden naming admin", e, err)
	}
}

func TestGrantKindCoversEveryVerb(t *testing.T) {
	for name := range verbRegistry {
		kind := grantKind(name)
		if _, ok := verbScopes[name]; !ok && kind != scopeOpen {
			t.Errorf("verb %q has no class for pane grants", name)
		}
	}
	// The verbs a pane calls about itself are open to every pane.
	for _, v := range []string{"hello", "list-verbs", "pane-grants", "resolve-pane"} {
		if grantKind(v) != scopeOpen {
			t.Errorf("%s is not open to every pane", v)
		}
	}
	if grantKind("request-approval") != scopeSelf {
		t.Error("request-approval is not a pane's report about itself")
	}
}

// The params a checked call carries on are valid JSON with the pane's own
// session filled in.
func TestCheckGrantsFillsTheOwnSession(t *testing.T) {
	d, _, a1, _, _ := scopeFixture(t)
	setStrict(d)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	out, verr := d.checkGrants(&connState{}, "list-windows", nil)
	if verr != nil {
		t.Fatal(verr)
	}
	var m map[string]string
	if err := json.Unmarshal(out, &m); err != nil || m["session"] != "a" {
		t.Errorf("params = %s, want session a", out)
	}
}

// TestGetWindowIsARead: tuios get-window used to send the client protocol's
// GetWindow, which a pane without admin may not send, so an agent holding
// read could not read one window's agent_state the way the skill tells it to.
// The get-window verb is a read: served on the pane's own session, refused
// on a session outside its reach, and the binary message stays admin only.
func TestGetWindowIsARead(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	setStrict(d, "read")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)

	got := result(t, callP(c, t, "get-window", map[string]any{"window": a2}))
	if got["window_id"] != a2 || got["type"] != "window" {
		t.Errorf("get-window = %v, want window %s", got, a2)
	}
	if _, ok := got["agent_state"]; !ok {
		t.Errorf("get-window = %v, want agent_state", got)
	}
	// No window means the focused one, as GetWindow did.
	if got := result(t, callP(c, t, "get-window", nil)); got["window_id"] != a2 {
		t.Errorf("get-window with no window = %v, want the focused %s", got["window_id"], a2)
	}
	wantForbidden(t, "get-window of b", callP(c, t, "get-window", map[string]any{"session": "b", "window": b1}))

	// The client protocol's command message stays admin only, and its
	// refusal names run-command and the read that replaces it, not attach.
	conn, err := net.DialTimeout("unix", sp, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	msg, err := NewMessage(MsgExecuteCommand, &ExecuteCommandPayload{SessionName: "a", CommandType: "GetWindow", RequestID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(conn, msg); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := ReadMessage(conn)
	if err != nil {
		t.Fatal(err)
	}
	var e ErrorPayload
	if resp.Type != MsgError || resp.ParsePayload(&e) != nil || e.Code != ErrCodeForbidden ||
		!strings.Contains(e.Message, "run-command") || !strings.Contains(e.Message, "get-window") {
		t.Errorf("GetWindow over the client protocol from a read pane: type %d, %+v; want forbidden naming run-command and get-window", resp.Type, e)
	}

	// The fields are list-windows' entry for the same window. The pane's
	// shell can still be printing, and output between the two reads moves
	// fields such as revision, so read both again until one pair agrees.
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	var diffs []string
	eventually(t, "get-window to match list-windows", 5*time.Second, func() bool {
		diffs = diffs[:0]
		rows := result(t, callP(person, t, "list-windows", map[string]any{"session": "a"}))["windows"].([]any)
		one := result(t, callP(person, t, "get-window", map[string]any{"session": "a", "window": "Second"}))
		found := false
		for _, r := range rows {
			row := r.(map[string]any)
			if row["window_id"] != a2 {
				continue
			}
			found = true
			for k, v := range row {
				if w, ok := one[k]; !ok || !jsonEqual(w, v) {
					diffs = append(diffs, fmt.Sprintf("get-window %s = %v, list-windows has %v", k, w, v))
				}
			}
		}
		if !found {
			diffs = append(diffs, fmt.Sprintf("list-windows has no entry for %v", a2))
		}
		if len(diffs) > 0 {
			t.Logf("reads differ: %s", strings.Join(diffs, "; "))
		}
		return len(diffs) == 0
	})
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// TestAttachedClientAnswersGetWindowAndNeverAPanesKeys: with a client
// attached, get-window is answered by the client, as the client protocol's
// GetWindow was, so tuios get-window keeps its fields. send-keys from a pane
// without admin is written to the pane's terminal and never routed to the
// client, where the prefix key would drive the window manager.
func TestAttachedClientAnswersGetWindowAndNeverAPanesKeys(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	tui := attachTestClient(t, "a")
	routed := make(chan *RemoteCommandPayload, 8)
	tui.OnRemoteCommand(func(p *RemoteCommandPayload) error {
		routed <- p
		if p.CommandType == "tape_command" && p.TapeCommand == "GetWindow" && len(p.TapeArgs) == 1 {
			return tui.SendCommandResultWithData(p.RequestID, true, "command executed", map[string]any{"id": p.TapeArgs[0], "cursor_x": 3})
		}
		return tui.SendCommandResult(p.RequestID, true, "ok")
	})

	setStrict(d)
	// The attached client said hello over the client protocol and is the
	// person's; the verb connection is the pane a1.
	d.setApprovalPeer(func(cs *connState) (bool, string) {
		if cs.hello != nil {
			return false, ""
		}
		return true, a1
	})
	c := dialVerb(t, sp)
	got := result(t, callP(c, t, "get-window", map[string]any{"window": "Second"}))
	if got["id"] != a2 || got["cursor_x"] != float64(3) || got["type"] != "window" {
		t.Errorf("get-window with a client attached = %v, want the client's answer for %s", got, a2)
	}
	// Drain the GetWindow the client answered.
	for len(routed) > 0 {
		<-routed
	}

	result(t, callP(c, t, "send-keys", map[string]any{"window": a2, "keys": "ctrl+b,c"}))
	select {
	case p := <-routed:
		t.Errorf("send-keys from a pane without admin was routed to the client: %+v", p)
	case <-time.After(200 * time.Millisecond):
	}

	// The person's keys with no window still go through the client. Keys for
	// a named window go to that window's terminal, whoever sends them, since
	// the client would hand them to the focused window instead.
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	result(t, callP(person, t, "send-keys", map[string]any{"session": "a", "window": a2, "keys": "Enter"}))
	select {
	case p := <-routed:
		t.Errorf("send-keys for a named window was routed to the client: %+v", p)
	case <-time.After(200 * time.Millisecond):
	}
	result(t, callP(person, t, "send-keys", map[string]any{"session": "a", "keys": "Enter"}))
	select {
	case p := <-routed:
		if p.CommandType != "send_keys" {
			t.Errorf("the person's send-keys reached the client as %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Error("the person's send-keys never reached the attached client")
	}
}
