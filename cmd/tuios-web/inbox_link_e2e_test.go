package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestNotificationLinkOpensTheInboxOnItsItem follows a push notification's
// link through the real web stack: /inbox?item=ID answers with a cookie and a
// redirect to the page, and a browser that then connects with that cookie
// gets the Inbox open on the item. A browser with no cookie gets tuios as
// usual, which is the positive half: the Inbox text is not on screen by any
// other road.
//
// Negative control: with daemonOpts.OpenInboxItem cut from
// createTUIOSHandler, the browser with the cookie shows no Inbox and the
// first wait fails.
func TestNotificationLinkOpensTheInboxOnItsItem(t *testing.T) {
	d := startWebDaemon(t)

	// An approval in another session, the item the link names.
	verbs, err := session.DialVerbClient()
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	defer func() { _ = verbs.Close() }()
	if _, err := verbs.Call("new-session", map[string]any{"name": "linkagent"}); err != nil {
		t.Fatalf("create the agent's session: %v", err)
	}
	if _, err := verbs.Call("set-agent-state", map[string]any{
		"session": "linkagent", "state": "needs_input", "kind": "approval",
		"harness": "claude-code", "message": "approve Bash: open from the phone",
	}); err != nil {
		t.Fatalf("raise the approval: %v", err)
	}
	raw, err := verbs.Call("list-attention", nil)
	if err != nil {
		t.Fatalf("list-attention: %v", err)
	}
	var listing struct {
		Items []session.AttentionItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil || len(listing.Items) != 1 {
		t.Fatalf("want one Inbox item, got %s (%v)", raw, err)
	}
	item := listing.Items[0].ID

	port := serveWeb(t, "linkweb")

	// The link: a cookie naming the item, and a redirect to the page. The
	// server starts in the background, so the first tries can be refused.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var resp *http.Response
	for deadline := time.Now().Add(10 * time.Second); ; {
		resp, err = client.Get("http://127.0.0.1:" + port + "/inbox?item=" + item)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("open the link: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "./" {
		t.Fatalf("/inbox answered %d to %q, want 303 to ./", resp.StatusCode, resp.Header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == inboxCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != item || !cookie.HttpOnly || cookie.MaxAge != inboxCookieAge {
		t.Fatalf("/inbox set %v, want an HttpOnly cookie naming item %s", resp.Cookies(), item)
	}
	// A value that is not an item id sets nothing.
	bad, err := client.Get("http://127.0.0.1:" + port + "/inbox?item=1%3B%20Path%3D%2F")
	if err != nil {
		t.Fatalf("open a bad link: %v", err)
	}
	_ = bad.Body.Close()
	if len(bad.Cookies()) != 0 {
		t.Fatalf("/inbox set %v for an id that is not a number", bad.Cookies())
	}

	withLink := dialWebRun(t, d, port, "linkweb", http.Header{"Cookie": {cookie.String()}})
	if !waitForText(withLink, "open from the phone", 15*time.Second) || !waitForText(withLink, "Approvals 1", time.Second) {
		t.Fatalf("the browser with the link's cookie never showed the Inbox on the item:\n%s", printable(withLink.text()))
	}
	saveWebArtifact(t, "inbox-link-with-cookie.txt", withLink.text())

	plain := dialWebRun(t, d, port, "linkweb", nil)
	time.Sleep(time.Second)
	if strings.Contains(printable(plain.text()), "Approvals 1") {
		t.Fatalf("a browser with no cookie opened the Inbox:\n%s", printable(plain.text()))
	}
}

// waitForText waits for s in what the browser has been sent.
func waitForText(r *webExitRun, s string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(printable(r.text()), s) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// saveWebArtifact keeps what the browser was sent, in $TUIOS_E2E_FRAMES when
// it is set.
func saveWebArtifact(t *testing.T, name, text string) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_FRAMES")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("artifact dir: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(printable(text)), 0o644); err != nil {
		t.Logf("save %s: %v", name, err)
	}
}
