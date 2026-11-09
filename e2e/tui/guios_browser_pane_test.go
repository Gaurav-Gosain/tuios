package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	guios "github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// New browser pane on a tuios daemon (guios polish wave 1, item 5). guios
// asked for a split with a URL, and the tuios client dropped the URL, so the
// daemon started a shell. The daemon has windows a native renderer draws:
// kind web with a uri. This runs the guios client (internal/guios/client),
// with no window, against a scratch daemon: the split must make a web window
// on that page, pane.page must move its uri, and the uri must come back after
// a daemon restart.
//
// How this could pass wrongly, written down first:
//   - The web window could come from somewhere else. No TUI is attached and
//     the test opens none: only the client's split makes one.
//   - The uri could be the first page because nothing changed it. The page
//     the client reports is another one, and the test waits for it.
//   - The restart could keep the uri in memory only. The daemon is killed
//     and started again from its saved state.
//
// The artifact is the client's log and list-windows before and after.
func TestGuiosBrowserPaneIsAWebWindow(t *testing.T) {
	if os.Getenv("TUIOS_E2E_GUIOS_BROWSER_CHILD") != "" {
		guiosBrowserChild(os.Getenv("TUIOS_E2E_GUIOS_BROWSER_CHILD"))
		return
	}
	base := t.TempDir()
	const session = "e2e-guios-web"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	killDaemon(t, base)
	rows := xpanesRowsIn(t, base, session)
	if len(rows) != 1 {
		t.Fatalf("the session has %d panes, want 1", len(rows))
	}
	const first, second = "https://example.com/first", "https://example.com/second"

	logPath := filepath.Join(artifactDir(t), "guios-browser-client.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create %s: %v", logPath, err)
	}
	defer logFile.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestGuiosBrowserPaneIsAWebWindow$", "-test.v")
	child.Dir = workDirIn(t, base)
	child.Env = append(os.Environ(), "TUIOS_E2E_GUIOS_BROWSER_CHILD="+session+" "+rows[0].ID+" "+first+" "+second)
	for _, key := range xdgKeys {
		child.Env = append(child.Env, key+"="+xdgDir(base, key))
	}
	child.Stdout, child.Stderr = logFile, logFile
	if err := child.Start(); err != nil {
		t.Fatalf("start the guios client: %v", err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}

	type webRow struct {
		ID   string `json:"window_id"`
		Kind string `json:"kind"`
		URI  string `json:"uri"`
	}
	webWindows := func() []webRow {
		out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
		if err != nil {
			return nil
		}
		var res struct {
			Windows []webRow `json:"windows"`
		}
		_ = json.Unmarshal([]byte(out), &res)
		var web []webRow
		for _, w := range res.Windows {
			if w.Kind != "" {
				web = append(web, w)
			}
		}
		return web
	}
	waitURI := func(want, what string) webRow {
		t.Helper()
		deadline := time.Now().Add(shellTimeout)
		for {
			web := webWindows()
			if len(web) == 1 && web[0].Kind == "web" && web[0].URI == want {
				return web[0]
			}
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: %s: the session's native windows are %+v, want one of kind web on %s. The client's log:\n%s", what, web, want, readLog())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	w := waitURI(first, "the split with a URL")
	waitURI(second, "the page the client reported")
	_ = child.Process.Kill()
	_ = child.Wait()

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	back := waitURI(second, "after a daemon restart")
	if back.ID != w.ID {
		t.Logf("the web window came back as %s (was %s)", back.ID, w.ID)
	}
}

// guiosBrowserChild is the guios client: it splits the pane given with a
// URL, as New browser pane does, and reports the page moving, as the web
// view does when the person follows a link.
func guiosBrowserChild(args string) {
	fail := func(f string, a ...any) {
		fmt.Fprintf(os.Stderr, f+"\n", a...)
		os.Exit(1)
	}
	parts := strings.Fields(args)
	if len(parts) != 4 {
		fail("want session, pane, first URL and second URL, got %q", args)
	}
	session, pane, first, second := parts[0], parts[1], parts[2], parts[3]
	c, err := guios.Connect(true)
	if err != nil {
		fail("connect: %v", err)
	}
	if err := c.Call("subscribe", nil, nil); err != nil {
		fail("subscribe: %v", err)
	}
	if err := c.Call("split", proto.CreateParams{Split: pane, Direction: "right", URL: first}, nil); err != nil {
		fail("split: %v", err)
	}
	fmt.Fprintln(os.Stderr, "split asked for", first)
	var web string
	for range 100 {
		raw, err := c.Verb("list-windows", map[string]any{"session": session}, false)
		if err == nil {
			var res struct {
				Windows []struct {
					ID   string `json:"window_id"`
					Kind string `json:"kind"`
				} `json:"windows"`
			}
			_ = json.Unmarshal(raw, &res)
			for _, w := range res.Windows {
				if w.Kind == "web" {
					web = w.ID
				}
			}
		}
		if web != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if web == "" {
		fail("no web window after the split")
	}
	time.Sleep(time.Second)
	if err := c.Call("pane.page", proto.PageParams{ID: web, URL: second, Title: "Second"}, nil); err != nil {
		fail("pane.page: %v", err)
	}
	fmt.Fprintln(os.Stderr, "page reported", second)
	<-c.Done()
}
