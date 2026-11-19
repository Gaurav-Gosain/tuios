package session

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// attachPromptTUI attaches a TUI client that shows prompts, the way the
// tuios TUI does, and returns it with the prompt pushes it reads.
func attachPromptTUI(t *testing.T, socketPath, session string, keys bool) (*TUIClient, <-chan PromptPayload) {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := NewTUIClient()
	c.conn = conn
	// The upgrade dialog's mark is what says the client takes the person's
	// keys. gui-bridge does not set it.
	c.UpgradeDialog = keys
	pushes := make(chan PromptPayload, 16)
	c.OnPrompt(func(p PromptPayload) { pushes <- p })
	if err := c.handshake("test", 80, 24, nil); err != nil {
		_ = conn.Close()
		t.Fatalf("handshake: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.AttachSession(session, false, 80, 24); err != nil {
		t.Fatalf("attach: %v", err)
	}
	// Pushes are demuxed by the read loop, which the attach does not start.
	c.StartReadLoop()
	return c, pushes
}

// promptAsync starts a prompt call on its own connection and returns a
// channel with its response.
func promptAsync(t *testing.T, sp string, params map[string]any) <-chan map[string]any {
	t.Helper()
	c := dialVerb(t, sp)
	raw, _ := json.Marshal(params)
	c.send(t, fmt.Sprintf(`{"id":1,"verb":"prompt","params":%s}`, raw))
	out := make(chan map[string]any, 1)
	go func() {
		_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		line, err := c.r.ReadBytes('\n')
		if err != nil {
			out <- map[string]any{"read_error": err.Error()}
			return
		}
		var resp map[string]any
		_ = json.Unmarshal(line, &resp)
		out <- resp
	}()
	return out
}

// nextPush waits for a client's next prompt push.
func nextPush(t *testing.T, pushes <-chan PromptPayload, what string) PromptPayload {
	t.Helper()
	select {
	case p := <-pushes:
		return p
	case <-time.After(5 * time.Second):
		t.Fatalf("no prompt push reached the client: %s", what)
	}
	return PromptPayload{}
}

// promptDone waits for a prompt call's result.
func promptDone(t *testing.T, done <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case resp := <-done:
		return result(t, resp)
	case <-time.After(10 * time.Second):
		t.Fatal("the prompt call did not return")
	}
	return nil
}

func answerPromptCall(t *testing.T, c *verbConn, params map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(params)
	return c.call(t, fmt.Sprintf(`{"id":1,"verb":"answer-prompt","params":%s}`, raw))
}

var deployItems = []map[string]any{
	{"id": "stg", "title": "Staging"},
	{"id": "prod", "title": "Production", "subtitle": "needs review", "icon": "!"},
}

// TestPromptPickRoundTrip is the whole round trip: the prompt reaches every
// client that shows prompts, nothing without the nonce of an attached client
// answers it, the answer must be one the prompt offers, the first answer
// wins, and the close reaches the other client.
func TestPromptPickRoundTrip(t *testing.T) {
	d, sp := startTestDaemon(t)
	twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	tui, pushes := attachPromptTUI(t, sp, "work", true)
	_, otherPushes := attachPromptTUI(t, sp, "work", true)

	done := promptAsync(t, sp, map[string]any{"kind": "pick", "title": "Deploy to?", "items": deployItems, "timeout": 20000})
	p := nextPush(t, pushes, "open")
	if p.Prompt == nil || p.Prompt.Title != "Deploy to?" || len(p.Prompt.Items) != 2 || p.Prompt.Items[1].Subtitle != "needs review" || p.Prompt.From != "a command" {
		t.Fatalf("push = %+v, want the prompt with its two rows, from a command", p.Prompt)
	}
	if o := nextPush(t, otherPushes, "open on the second client"); o.Prompt == nil || o.Prompt.ID != p.Prompt.ID {
		t.Fatalf("the second client got %+v, want the same prompt", o)
	}
	id := p.Prompt.ID

	if code := errCode(t, answerPromptCall(t, c, map[string]any{"request_id": id, "ids": []string{"stg"}})); code != ErrVerbNotHuman {
		t.Fatalf("answer without a nonce: code %q, want %q", code, ErrVerbNotHuman)
	}
	nonce := tui.HumanNonce()
	for _, bad := range []map[string]any{
		{"ids": []string{"dev"}},
		{"ids": []string{"stg", "prod"}},
		{"ids": []string{}},
		{"other": "qa"},
		{"value": "stg"},
		{"cancel": true, "ids": []string{"stg"}},
	} {
		bad["request_id"], bad["human_nonce"] = id, nonce
		if code := errCode(t, answerPromptCall(t, c, bad)); code != ErrVerbInvalidParams {
			t.Fatalf("answer %v: code %q, want %q", bad, code, ErrVerbInvalidParams)
		}
	}
	select {
	case resp := <-done:
		t.Fatalf("the prompt returned before anyone answered: %v", resp)
	default:
	}

	res := result(t, answerPromptCall(t, c, map[string]any{"request_id": id, "human_nonce": nonce, "ids": []string{"prod"}}))
	if res["applied"] != true || res["status"] != PromptAnswered {
		t.Fatalf("answer-prompt = %v, want applied", res)
	}
	got := promptDone(t, done)
	ids, _ := got["ids"].([]any)
	if got["status"] != PromptAnswered || len(ids) != 1 || ids[0] != "prod" || got["answered_by"] == "" || got["request_id"] != id {
		t.Fatalf("prompt = %v, want answered prod", got)
	}
	if cl := nextPush(t, otherPushes, "close"); cl.Prompt != nil || cl.Closed != id || cl.Reason != PromptAnswered {
		t.Fatalf("the second client got %+v, want the close with reason answered", cl)
	}
	// The first answer stands: the prompt is gone.
	if code := errCode(t, answerPromptCall(t, c, map[string]any{"request_id": id, "human_nonce": nonce, "ids": []string{"stg"}})); code != ErrVerbInvalidParams {
		t.Fatalf("a second answer: code %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestPromptInputAndOther: an input returns the typed line, and a pick with
// allow_other and multiple returns the rows and the typed text.
func TestPromptInputAndOther(t *testing.T) {
	d, sp := startTestDaemon(t)
	twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	tui, pushes := attachPromptTUI(t, sp, "work", true)
	nonce := tui.HumanNonce()

	done := promptAsync(t, sp, map[string]any{"kind": "input", "title": "Branch name?", "placeholder": "feature/...", "default": "fix/", "timeout": 20000})
	p := nextPush(t, pushes, "input open")
	if p.Prompt == nil || p.Prompt.Kind != PromptInput || p.Prompt.Default != "fix/" || p.Prompt.Placeholder != "feature/..." {
		t.Fatalf("push = %+v, want the input with its default and placeholder", p.Prompt)
	}
	if code := errCode(t, answerPromptCall(t, c, map[string]any{"request_id": p.Prompt.ID, "human_nonce": nonce, "value": "a\nb"})); code != ErrVerbInvalidParams {
		t.Fatalf("a value of two lines: code %q, want %q", code, ErrVerbInvalidParams)
	}
	result(t, answerPromptCall(t, c, map[string]any{"request_id": p.Prompt.ID, "human_nonce": nonce, "value": "fix/prompt"}))
	if got := promptDone(t, done); got["status"] != PromptAnswered || got["value"] != "fix/prompt" {
		t.Fatalf("prompt = %v, want answered fix/prompt", got)
	}
	nextPush(t, pushes, "input close")

	done = promptAsync(t, sp, map[string]any{"kind": "pick", "title": "Which?", "items": deployItems, "multiple": true, "allow_other": true, "timeout": 20000})
	p = nextPush(t, pushes, "pick open")
	result(t, answerPromptCall(t, c, map[string]any{"request_id": p.Prompt.ID, "human_nonce": nonce, "ids": []string{"stg", "prod"}, "other": "qa"}))
	got := promptDone(t, done)
	ids, _ := got["ids"].([]any)
	if got["status"] != PromptAnswered || len(ids) != 2 || got["other"] != "qa" {
		t.Fatalf("prompt = %v, want stg, prod and other qa", got)
	}
}

// TestPromptEndsWithAReason: no client, a client that takes no keys, a
// second prompt while one is open, a cancel and the wait running out each end
// with their own status.
func TestPromptEndsWithAReason(t *testing.T) {
	d, sp := startTestDaemon(t)
	twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	input := map[string]any{"kind": "input", "title": "Name?", "timeout": 20000}

	if got := result(t, c.call(t, `{"id":1,"verb":"prompt","params":{"kind":"input","title":"Name?"}}`)); got["status"] != PromptNoClient || got["request_id"] != nil {
		t.Fatalf("with no client: %v, want no_client and nothing shown", got)
	}
	_, bridgePushes := attachPromptTUI(t, sp, "work", false)
	if got := result(t, c.call(t, `{"id":1,"verb":"prompt","params":{"kind":"input","title":"Name?"}}`)); got["status"] != PromptNoClient {
		t.Fatalf("with only a client that takes no keys: %v, want no_client", got)
	}
	select {
	case p := <-bridgePushes:
		t.Fatalf("a client that takes no keys was sent %+v", p)
	default:
	}

	tui, pushes := attachPromptTUI(t, sp, "work", true)
	done := promptAsync(t, sp, input)
	p := nextPush(t, pushes, "open")
	if got := result(t, c.call(t, `{"id":1,"verb":"prompt","params":{"kind":"input","title":"Other?"}}`)); got["status"] != PromptBusy {
		t.Fatalf("a second prompt: %v, want busy", got)
	}
	result(t, answerPromptCall(t, c, map[string]any{"request_id": p.Prompt.ID, "human_nonce": tui.HumanNonce(), "cancel": true}))
	if got := promptDone(t, done); got["status"] != PromptCancelled {
		t.Fatalf("after a cancel: %v, want cancelled", got)
	}
	if cl := nextPush(t, pushes, "close after cancel"); cl.Closed != p.Prompt.ID || cl.Reason != PromptCancelled {
		t.Fatalf("close push %+v, want reason cancelled", cl)
	}

	start := time.Now()
	got := result(t, c.call(t, `{"id":1,"verb":"prompt","params":{"kind":"input","title":"Name?","timeout":300}}`))
	if got["status"] != PromptTimeout || time.Since(start) > 5*time.Second {
		t.Fatalf("after the wait: %v, want timeout", got)
	}
	nextPush(t, pushes, "open before the timeout")
	if cl := nextPush(t, pushes, "close after the timeout"); cl.Reason != PromptTimeout {
		t.Fatalf("close push %+v, want reason timeout", cl)
	}
	// The next prompt is not busy: the one that timed out is gone.
	done = promptAsync(t, sp, input)
	p = nextPush(t, pushes, "open after the timeout")
	result(t, answerPromptCall(t, c, map[string]any{"request_id": p.Prompt.ID, "human_nonce": tui.HumanNonce(), "value": "x"}))
	if got := promptDone(t, done); got["status"] != PromptAnswered {
		t.Fatalf("a prompt after the timeout: %v, want answered", got)
	}
}

// TestPromptOpenReachesAnAttachingClientOnce: a prompt that opens while a
// client attaches reaches that client once, before its close. The attach
// handler sends the open prompt to the client it admits, and the push of a
// prompt that opened after the admission reached the client as well, so it
// read two opens and took the second one for the close.
func TestPromptOpenReachesAnAttachingClientOnce(t *testing.T) {
	d, sp := startTestDaemon(t)
	twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)

	// The attach handler waits after its reply, with the client admitted to
	// the pushes, until the prompt below has reached the client.
	release := make(chan struct{})
	var once sync.Once
	hook := func() {
		once.Do(func() {
			select {
			case <-release:
			case <-time.After(10 * time.Second):
			}
		})
	}
	attachReplied.Store(&hook)
	t.Cleanup(func() { attachReplied.Store(nil) })
	var released sync.Once
	letGo := func() { released.Do(func() { close(release) }) }
	t.Cleanup(letGo)

	tui, pushes := attachPromptTUI(t, sp, "work", true)
	done := promptAsync(t, sp, map[string]any{"kind": "input", "title": "Name?", "timeout": 20000})
	p := nextPush(t, pushes, "open")
	if p.Prompt == nil {
		t.Fatalf("push %+v, want the open prompt", p)
	}
	// The attach goes on and sends the client what is open now. The daemon
	// reads this client's next request once the attach handler returns.
	letGo()
	if _, err := tui.RefreshSessionList(); err != nil {
		t.Fatalf("session list: %v", err)
	}
	result(t, answerPromptCall(t, c, map[string]any{"request_id": p.Prompt.ID, "human_nonce": tui.HumanNonce(), "cancel": true}))
	if got := promptDone(t, done); got["status"] != PromptCancelled {
		t.Fatalf("after a cancel: %v, want cancelled", got)
	}
	if cl := nextPush(t, pushes, "close"); cl.Prompt != nil || cl.Closed != p.Prompt.ID || cl.Reason != PromptCancelled {
		t.Fatalf("push %+v, want the close with reason cancelled", cl)
	}
	// Nothing follows the close: the next push opens the next prompt.
	done = promptAsync(t, sp, map[string]any{"kind": "input", "title": "Again?", "timeout": 20000})
	o := nextPush(t, pushes, "open of the next prompt")
	if o.Prompt == nil || o.Prompt.Title != "Again?" {
		t.Fatalf("push %+v, want the open of the next prompt", o)
	}
	result(t, answerPromptCall(t, c, map[string]any{"request_id": o.Prompt.ID, "human_nonce": tui.HumanNonce(), "value": "x"}))
	if got := promptDone(t, done); got["status"] != PromptAnswered {
		t.Fatalf("the next prompt: %v, want answered", got)
	}
}

// TestPromptChecksWhatItAsks refuses a prompt the palette could not show as
// asked, before anything is shown.
func TestPromptChecksWhatItAsks(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	for _, bad := range []string{
		`{"kind":"form","title":"x"}`,
		`{"kind":"input"}`,
		`{"kind":"input","title":"two\nlines"}`,
		`{"kind":"input","title":"x","items":[{"id":"a","title":"A"}]}`,
		`{"kind":"pick","title":"x"}`,
		`{"kind":"pick","title":"x","default":"a","items":[{"id":"a","title":"A"}]}`,
		`{"kind":"pick","title":"x","items":[{"id":"a","title":"A"},{"id":"a","title":"B"}]}`,
		`{"kind":"pick","title":"x","items":[{"id":"a"}]}`,
		`{"kind":"pick","title":"x","items":[{"id":"","title":"A"}]}`,
		`{"kind":"input","title":"x","timeout":-1}`,
	} {
		if code := errCode(t, c.call(t, `{"id":1,"verb":"prompt","params":`+bad+`}`)); code != ErrVerbInvalidParams {
			t.Errorf("prompt %s: code %q, want %q", bad, code, ErrVerbInvalidParams)
		}
	}
}
