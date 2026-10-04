package tuie2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The [notify] push notifications, against a real daemon and a local HTTP
// server that stands in for ntfy, Pushover and a webhook.

// pushRequest is one request the fake provider received.
type pushRequest struct {
	Provider string            `json:"provider"`
	Path     string            `json:"path"`
	Header   map[string]string `json:"header"`
	Body     string            `json:"body"`
}

// fakePush records every request it gets, by provider.
type fakePush struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []pushRequest
}

func newFakePush(t *testing.T) *fakePush {
	t.Helper()
	f := &fakePush{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		provider := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)[0]
		h := map[string]string{}
		for _, k := range []string{"Title", "Priority", "Click", "Tags", "Authorization", "Content-Type"} {
			if v := r.Header.Get(k); v != "" {
				h[k] = v
			}
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, pushRequest{Provider: provider, Path: r.URL.Path, Header: h, Body: string(body)})
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// of returns the requests one provider got.
func (f *fakePush) of(provider string) []pushRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []pushRequest
	for _, r := range f.reqs {
		if r.Provider == provider {
			out = append(out, r)
		}
	}
	return out
}

// total is how many requests arrived.
func (f *fakePush) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

// waitCounts waits until each provider has got want requests.
func (f *fakePush) waitCounts(t *testing.T, want int, what string) {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for {
		if len(f.of("ntfy")) >= want && len(f.of("pushover")) >= want && len(f.of("hook")) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: want %d request per provider, got ntfy %d, pushover %d, webhook %d",
				what, want, len(f.of("ntfy")), len(f.of("pushover")), len(f.of("hook")))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// save writes what the fake provider received as the test's artifact.
func (f *fakePush) save(t *testing.T, name string) {
	t.Helper()
	f.mu.Lock()
	data, _ := json.MarshalIndent(f.reqs, "", "  ")
	f.mu.Unlock()
	if err := os.WriteFile(filepath.Join(artifactDir(t), name), data, 0o644); err != nil {
		t.Errorf("save %s: %v", name, err)
	}
}

// The secrets the config hands the daemon, one per way a secret is read.
const (
	pushFileToken = "tk-file-6f1d2c"         // ntfy token_file
	pushEnvToken  = "pushover-env-8a7b3e"    // Pushover token_env
	pushUserKey   = "pushover-user-key-41c9" // Pushover user, inline
	pushHookToken = "hook-inline-0d5e77"     // webhook token, inline
	pushTopic     = "tuios-topic-secret-c3"  // the ntfy topic name
	pushEnvName   = "TUIOS_E2E_PUSH_TOKEN"
)

// writeNotifyConfig writes a config.toml whose [notify] table sends to f with
// quiet_active_seconds set to quiet and cooldown_seconds to cooldown.
func writeNotifyConfig(t *testing.T, base string, f *fakePush, quiet, cooldown int) {
	t.Helper()
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	tokenFile := filepath.Join(dir, "ntfy-token")
	if err := os.WriteFile(tokenFile, []byte(pushFileToken+"\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	body := `[notify]
web_url = "https://term.example.test/"
quiet_active_seconds = ` + itoa(quiet) + `
cooldown_seconds = ` + itoa(cooldown) + `

[notify.ntfy]
url = "` + f.srv.URL + `/ntfy/` + pushTopic + `"
token_file = "` + tokenFile + `"

[notify.pushover]
url = "` + f.srv.URL + `/pushover/1/messages.json"
user = "` + pushUserKey + `"
token_env = "` + pushEnvName + `"

[notify.webhook]
url = "` + f.srv.URL + `/hook"
token = "` + pushHookToken + `"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestNotifyPushesAnApprovalNobodyIsLookingAt raises an approval in a session
// with no client attached. Each provider gets exactly one notification, with
// the title, the Inbox line, the link to the item on tuios-web and its own
// credential. The same state reported again, and the item's line changing,
// send nothing more. Neither the daemon log nor tuios notify test shows a
// secret, and notify test reaches every provider.
//
// Negative controls: with d.notify.start() cut from Daemon.Start nothing is
// received and the first wait fails; with the n.sent check cut from note the
// line change sends a second notification and the de-dupe check fails.
func TestNotifyPushesAnApprovalNobodyIsLookingAt(t *testing.T) {
	t.Setenv(pushEnvName, pushEnvToken)
	t.Setenv("TUIOS_LOG_LEVEL", "trace")
	fake := newFakePush(t)
	base := t.TempDir()
	killDaemon(t, base)
	// No cooldown, so the per-item de-dupe is the only thing that can hold
	// back a second send for the same pane.
	writeNotifyConfig(t, base, fake, 120, 0)

	if out, err := tuiosCLI(t, base, "new", "e2e-agent", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-agent", "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: make test"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	fake.waitCounts(t, 1, "after the approval")

	ntfy := fake.of("ntfy")[0]
	if ntfy.Path != "/ntfy/"+pushTopic {
		t.Errorf("ntfy got %s, want the topic path", ntfy.Path)
	}
	if !strings.HasPrefix(ntfy.Header["Title"], "Approval needed") {
		t.Errorf("ntfy title %q, want it to start with Approval needed", ntfy.Header["Title"])
	}
	if !strings.Contains(ntfy.Body, "approve Bash: make test") || !strings.Contains(ntfy.Body, "Session e2e-agent.") {
		t.Errorf("ntfy body %q, want the Inbox line and the session", ntfy.Body)
	}
	if ntfy.Header["Authorization"] != "Bearer "+pushFileToken {
		t.Errorf("ntfy did not get the token from token_file")
	}
	if ntfy.Header["Priority"] != "4" {
		t.Errorf("ntfy priority %q, want 4 for an item that waits on you", ntfy.Header["Priority"])
	}
	click, err := url.Parse(ntfy.Header["Click"])
	if err != nil || click.Host != "term.example.test" || click.Path != "/inbox" || click.Query().Get("item") == "" {
		t.Fatalf("ntfy Click %q, want the tuios-web Inbox link for the item", ntfy.Header["Click"])
	}
	item := click.Query().Get("item")

	form, err := url.ParseQuery(fake.of("pushover")[0].Body)
	if err != nil {
		t.Fatalf("pushover body: %v", err)
	}
	if form.Get("token") != pushEnvToken || form.Get("user") != pushUserKey {
		t.Errorf("pushover did not get the token from token_env and the inline user key")
	}
	if !strings.HasPrefix(form.Get("title"), "Approval needed") || !strings.Contains(form.Get("message"), "approve Bash: make test") ||
		form.Get("url") != click.String() || form.Get("priority") != "1" {
		t.Errorf("pushover form %v is not the approval", form)
	}

	hook := fake.of("hook")[0]
	var payload struct {
		Event   string `json:"event"`
		Kind    string `json:"kind"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		Link    string `json:"link"`
		ItemID  string `json:"item_id"`
		Session string `json:"session"`
		Urgent  bool   `json:"urgent"`
		Test    bool   `json:"test"`
	}
	if err := json.Unmarshal([]byte(hook.Body), &payload); err != nil {
		t.Fatalf("webhook body: %v\n%s", err, hook.Body)
	}
	if payload.Event != "tuios.inbox" || payload.Kind != "approval" || payload.ItemID != item ||
		payload.Session != "e2e-agent" || !payload.Urgent || payload.Test || payload.Link != click.String() {
		t.Errorf("webhook payload %+v is not the approval", payload)
	}
	if hook.Header["Authorization"] != "Bearer "+pushHookToken {
		t.Errorf("the webhook did not get the inline token")
	}

	// The same state again, three times, and then the line changing: the
	// item is the same one, so nothing more is sent.
	for range 3 {
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-agent", "needs_input",
			"--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: make test"); err != nil {
			t.Fatalf("repeat set-agent-state: %v\n%s", err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-agent", "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: make lint"); err != nil {
		t.Fatalf("change the line: %v\n%s", err, out)
	}
	// The positive half: the item did change, so an update event went out.
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention")
		if strings.Contains(out, "approve Bash: make lint") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Inbox line never changed:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Absence cannot be waited for. A second send would be queued at once,
	// so a second is plenty for it to land.
	time.Sleep(time.Second)
	if n := fake.total(); n != 3 {
		t.Fatalf("after repeated reports and a changed line the providers got %d requests, want the first 3 only", n)
	}

	// tuios notify test reaches each provider, and says so per provider.
	out, err := tuiosCLI(t, base, "notify", "test")
	if err != nil {
		t.Fatalf("notify test: %v\n%s", err, out)
	}
	host := strings.TrimPrefix(fake.srv.URL, "http://")
	for _, name := range []string{"ntfy", "pushover", "webhook"} {
		if !strings.Contains(out, name+" ("+host+"): sent.") {
			t.Errorf("notify test does not report %s as sent:\n%s", name, out)
		}
	}
	fake.waitCounts(t, 2, "after notify test")
	fake.save(t, "notify-received.json")

	secrets := []string{pushFileToken, pushEnvToken, pushUserKey, pushHookToken, pushTopic}
	logData, err := os.ReadFile(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log"))
	if err != nil {
		t.Fatalf("read the daemon log: %v", err)
	}
	// The positive half: the log is the right one, and it says what was sent.
	if !strings.Contains(string(logData), "[NOTIFY] ntfy ("+host+") sent item "+item) {
		t.Errorf("the daemon log does not record the ntfy send")
	}
	for _, s := range secrets {
		if strings.Contains(string(logData), s) {
			t.Errorf("the daemon log holds a secret (%d bytes in)", strings.Index(string(logData), s))
		}
		if strings.Contains(out, s) {
			t.Errorf("notify test printed a secret")
		}
	}
}

// TestNotifyHoldsWhileAPersonTypesAtAClient attaches a client and types into
// its pane. An approval raised in another session then sends nothing: the
// person is at the desk and the Inbox already tells them. Once the client is
// gone, an approval in a third session is sent at once, and the held one
// still waits for its quiet time to pass.
//
// Negative control: with the quiet check cut from considerLocked the first
// approval is sent at once and the first count fails.
func TestNotifyHoldsWhileAPersonTypesAtAClient(t *testing.T) {
	t.Setenv(pushEnvName, pushEnvToken)
	fake := newFakePush(t)
	base := t.TempDir()
	killDaemon(t, base)
	writeNotifyConfig(t, base, fake, 300, 60)

	for _, name := range []string{"e2e-home", "e2e-agent", "e2e-other"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	// An attached client starts in terminal mode, so this goes to the shell.
	if err := term.SendKeys("echo typed-at-the-desk", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "typed-at-the-desk") >= 2
	}, shellTimeout); err != nil {
		t.Fatalf("the shell never ran the line: %v\n%s", err, term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-agent", "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: held at the desk"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention")
		if strings.Contains(out, "held at the desk") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the approval never reached the Inbox:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(time.Second)
	if n := fake.total(); n != 0 {
		t.Fatalf("with a person typing at a client the providers got %d requests, want 0", n)
	}
	saveArtifact(t, term, artifactDir(t), "notify-quiet-client")

	// The person leaves. The client goes, and the daemon forgets it.
	_ = term.Close()
	deadline = time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-clients", "--json")
		if !strings.Contains(out, `"e2e-home"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client never left:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-other", "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: nobody here"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	fake.waitCounts(t, 1, "after the person left")
	time.Sleep(time.Second)
	fake.save(t, "notify-received.json")
	if n := fake.total(); n != 3 {
		t.Fatalf("the providers got %d requests, want 3: one each for the second approval", n)
	}
	for _, r := range fake.of("ntfy") {
		if !strings.Contains(r.Body, "nobody here") {
			t.Errorf("ntfy got %q, want only the approval raised after the person left", r.Body)
		}
	}
}

// TestNotifySendsAHeldItemWhenThePersonStaysAway is the other half of the
// quiet rule: an approval held because the person typed a moment ago is sent
// once they have been away for quiet_active_seconds, with the client still
// attached and the item still open.
//
// Negative control: with the n.armLocked call cut from considerLocked the
// held item is never looked at again and the wait fails.
func TestNotifySendsAHeldItemWhenThePersonStaysAway(t *testing.T) {
	const quiet = 3 * time.Second
	t.Setenv(pushEnvName, pushEnvToken)
	fake := newFakePush(t)
	base := t.TempDir()
	killDaemon(t, base)
	writeNotifyConfig(t, base, fake, int(quiet/time.Second), 60)

	for _, name := range []string{"e2e-home", "e2e-agent"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys("echo typed-then-left", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "typed-then-left") >= 2
	}, shellTimeout); err != nil {
		t.Fatalf("the shell never ran the line: %v\n%s", err, term.Snapshot())
	}
	// The last key went before the shell echoed it, so this is a little
	// after the daemon's own record of it.
	typed := time.Now()
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-agent", "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: wait for me"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	time.Sleep(500 * time.Millisecond)
	if n := fake.total(); n != 0 && time.Since(typed) < quiet-time.Second {
		t.Fatalf("the approval was sent %s after the person typed, want it held for %s", time.Since(typed), quiet)
	}
	fake.waitCounts(t, 1, "after the person stayed away")
	if waited := time.Since(typed); waited < quiet-time.Second {
		t.Fatalf("the held approval was sent %s after the last key, before the quiet time", waited)
	}
	if !strings.Contains(fake.of("ntfy")[0].Body, "wait for me") {
		t.Errorf("ntfy got %q, want the held approval", fake.of("ntfy")[0].Body)
	}
	fake.save(t, "notify-received.json")
	alive(t, term, "after the held notification")
}
