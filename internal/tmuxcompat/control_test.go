package tmuxcompat

import (
	"bufio"
	"errors"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeStream is an event stream a test pushes events into.
type fakeStream struct {
	ch     chan []byte
	params map[string]any
}

func (f *fakeStream) Next() ([]byte, error) {
	ev, ok := <-f.ch
	if !ok {
		return nil, io.EOF
	}
	return ev, nil
}

func (f *fakeStream) Close() error { return nil }

// controlClient is a shim running in control mode, with its standard input
// and output wired to the test.
type controlClient struct {
	t       *testing.T
	stdin   *io.PipeWriter
	lines   chan string
	done    chan int
	streams chan *fakeStream
}

// startControl runs the shim with args in control mode.
// With events false the daemon gives no event stream.
func startControl(t *testing.T, h *harness, events bool, args ...string) *controlClient {
	t.Helper()
	oldPoll, oldSettle, oldOut := controlPoll, controlSettle, controlOutputEvery
	controlPoll, controlSettle, controlOutputEvery = 40*time.Millisecond, time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { controlPoll, controlSettle, controlOutputEvery = oldPoll, oldSettle, oldOut })
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &controlClient{t: t, stdin: inW, lines: make(chan string, 256), done: make(chan int, 1), streams: make(chan *fakeStream, 4)}
	h.shim.Stdin = inR
	h.shim.Stdout = outW
	h.shim.Subscribe = func(params map[string]any) (EventStream, error) {
		if !events {
			return nil, errors.New("unknown verb subscribe")
		}
		st := &fakeStream{ch: make(chan []byte, 16), params: params}
		c.streams <- st
		return st, nil
	}
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			c.lines <- sc.Text()
		}
		close(c.lines)
	}()
	go func() {
		c.done <- h.shim.Run(args)
		_ = outW.Close()
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return c
}

// next returns the next line of output, failing after a second.
func (c *controlClient) next() string {
	c.t.Helper()
	select {
	case l, ok := <-c.lines:
		if !ok {
			c.t.Fatal("the control client ended its output")
		}
		return l
	case <-time.After(2 * time.Second):
		c.t.Fatal("no control-mode output within 2s")
	}
	return ""
}

// until reads lines until one matches re, and returns it.
func (c *controlClient) until(re string) string {
	c.t.Helper()
	rx := regexp.MustCompile(re)
	for {
		if l := c.next(); rx.MatchString(l) {
			return l
		}
	}
}

// block reads one %begin block and returns its body and closing word.
func (c *controlClient) block(flags string) ([]string, string) {
	c.t.Helper()
	begin := c.until(`^%begin `)
	f := strings.Fields(begin)
	if len(f) != 4 || f[3] != flags {
		c.t.Fatalf("%q: want %%begin TIME NUMBER %s", begin, flags)
	}
	var body []string
	for {
		l := c.next()
		if l == "%end "+f[1]+" "+f[2]+" "+f[3] {
			return body, "%end"
		}
		if l == "%error "+f[1]+" "+f[2]+" "+f[3] {
			return body, "%error"
		}
		body = append(body, l)
	}
}

func (c *controlClient) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.stdin, line+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

// stream returns the n-th event stream the client opened.
func (c *controlClient) stream() *fakeStream {
	c.t.Helper()
	select {
	case st := <-c.streams:
		return st
	case <-time.After(2 * time.Second):
		c.t.Fatal("the control client opened no event stream")
	}
	return nil
}

// TestControlModeTheWayCollieWatches attaches a control client the way
// Collie's watch does (tmux -C attach-session -t $N -f
// ignore-size,read-only), then changes the session under it and checks each
// change comes out as the notification tmux sends.
func TestControlModeTheWayCollieWatches(t *testing.T) {
	h := newHarness(t)
	c := startControl(t, h, true, "-S", SocketPath(h.shim.Dir), "-C", "attach-session", "-t", "$0", "-f", "ignore-size,read-only")

	if body, end := c.block("0"); end != "%end" || len(body) != 0 {
		t.Fatalf("attach-session block = %q %s", body, end)
	}
	if l := c.next(); l != "%session-changed $0 work" {
		t.Fatalf("after attach: %q, want %%session-changed $0 work", l)
	}
	life, out := c.stream(), c.stream()
	if !reflect.DeepEqual(out.params["types"], []string{"output"}) || out.params["session"] != "work" || life.params["session"] != "work" {
		t.Errorf("subscriptions = %v and %v", life.params, out.params)
	}

	// A command on stdin is framed with flags 1.
	c.send("list-windows -F '#{window_id} #{window_name}'")
	if body, end := c.block("1"); end != "%end" || !reflect.DeepEqual(body, []string{"@1 claude"}) {
		t.Errorf("list-windows block = %q %s", body, end)
	}
	// The client is read-only, so a command that changes something fails.
	c.send("kill-pane -t " + PaneID("leader-0001"))
	if body, end := c.block("1"); end != "%error" || !reflect.DeepEqual(body, []string{"client is read-only"}) {
		t.Errorf("kill-pane block = %q %s", body, end)
	}
	if h.fake.called("close-window") {
		t.Error("a read-only client closed a pane")
	}

	// Output in a pane of the session.
	out.ch <- []byte(`{"type":"output","session":"work","window":"leader-0001","bytes":12}`)
	if l := c.until(`^%output `); l != "%output "+PaneID("leader-0001")+" " {
		t.Errorf("output: %q", l)
	}

	// A pane opens on workspace 2: a new tmux window.
	h.fake.with(func() {
		h.fake.windows = append(h.fake.windows, &fakeWindow{id: "side-0002", name: "logs", ws: 2, w: 80, h: 24})
	})
	life.ch <- []byte(`{"type":"window-created","session":"work","window":"side-0002"}`)
	if l := c.until(`^%window-add`); l != "%window-add @2" {
		t.Errorf("window-add: %q", l)
	}
	// A second pane on workspace 1: its layout changes.
	h.fake.with(func() {
		h.fake.windows = append(h.fake.windows, &fakeWindow{id: "mate-0003", name: "mate", ws: 1, x: 60, w: 60, h: 40})
	})
	life.ch <- []byte(`{"type":"window-created","session":"work","window":"mate-0003"}`)
	l := c.until(`^%layout-change`)
	if !regexp.MustCompile(`^%layout-change @1 [0-9a-f]{4},120x40,0,0\{120x40,0,0,\d+,60x40,60,0,\d+\} \S+ \*$`).MatchString(l) {
		t.Errorf("layout-change: %q", l)
	}
	// Focus moves to the new pane, and the session shows workspace 2.
	h.fake.with(func() { h.fake.focused = "mate-0003" })
	life.ch <- []byte(`{"type":"window-focused","session":"work","window":"mate-0003"}`)
	if l := c.until(`^%window-pane-changed`); l != "%window-pane-changed @1 "+PaneID("mate-0003") {
		t.Errorf("window-pane-changed: %q", l)
	}
	h.fake.with(func() { h.fake.current = 2 })
	life.ch <- []byte(`{"type":"workspace-switched","session":"work","workspace":2}`)
	if l := c.until(`^%session-window-changed`); l != "%session-window-changed $0 @2" {
		t.Errorf("session-window-changed: %q", l)
	}
	// A workspace rename raises no event. The poll finds it.
	h.fake.with(func() { h.fake.wsNames[2] = "server" })
	if l := c.until(`^%window-renamed`); l != "%window-renamed @2 server" {
		t.Errorf("window-renamed: %q", l)
	}
	// The workspace empties: the tmux window closes.
	h.fake.with(func() {
		h.fake.windows = h.fake.windows[:1]
		h.fake.windows = append(h.fake.windows, &fakeWindow{id: "mate-0003", ws: 1, x: 60, w: 60, h: 40})
	})
	life.ch <- []byte(`{"type":"window-closed","session":"work","window":"side-0002"}`)
	if l := c.until(`^%window-close`); l != "%window-close @2" {
		t.Errorf("window-close: %q", l)
	}

	// Standard input closes: the client exits.
	_ = c.stdin.Close()
	c.until(`^%exit$`)
	if code := <-c.done; code != 0 {
		t.Errorf("exit status %d", code)
	}
}

// TestControlModeWritableAndCC runs a control client that may write, wrapped
// in -CC's DCS, and detaches it with detach-client.
func TestControlModeWritableAndCC(t *testing.T) {
	h := newHarness(t)
	c := startControl(t, h, true, "-CC", "attach")
	first := c.next()
	if !strings.HasPrefix(first, "\x1bP1000p%begin ") {
		t.Fatalf("first line %q, want the DCS and then %%begin", first)
	}
	c.until(`^%end `)
	if l := c.next(); l != "%session-changed $0 work" {
		t.Fatalf("after attach: %q", l)
	}
	c.stream()
	c.stream()
	c.send(`send-keys -t ` + PaneID("leader-0001") + ` "echo hi" Enter ; display-message -p "#{session_name}"`)
	if body, end := c.block("1"); end != "%end" || len(body) != 0 {
		t.Errorf("send-keys block = %q %s", body, end)
	}
	if body, end := c.block("1"); end != "%end" || !reflect.DeepEqual(body, []string{"work"}) {
		t.Errorf("display-message block = %q %s", body, end)
	}
	if st := h.fake.last("send-text"); st["text"] != "echo hi\r" {
		t.Errorf("send-text = %v", st)
	}
	c.send("detach-client")
	c.block("1")
	if l := c.next(); l != "%exit" {
		t.Errorf("after detach-client: %q", l)
	}
	if l, ok := <-c.lines; ok && l != "\x1b\\" {
		t.Errorf("after %%exit: %q, want the DCS end", l)
	}
	<-c.done
}

// TestControlModeSessionEnds exits the client when its session ends, and
// fails an attach to a session that is not there.
func TestControlModeSessionEnds(t *testing.T) {
	h := newHarness(t)
	c := startControl(t, h, true, "-C", "attach-session", "-t", "nope")
	if body, end := c.block("0"); end != "%error" || !reflect.DeepEqual(body, []string{"can't find session: nope"}) {
		t.Errorf("attach to nope = %q %s", body, end)
	}
	c.until(`^%exit$`)
	if code := <-c.done; code != 1 {
		t.Errorf("a failed attach exited %d, want 1", code)
	}

	h = newHarness(t)
	m := newMultiFake(t, "work", "api")
	h.shim.Caller = m
	h.shim.AllSessions = true
	h.shim.Session, h.shim.Window, h.shim.TmuxPane = "", "", ""
	c = startControl(t, h, true, "-C", "attach-session", "-t", "api")
	c.block("0")
	c.until(`^%session-changed \$\d+ api$`)
	life := c.stream()
	if _, ok := life.params["session"]; ok {
		t.Errorf("serving every session, the lifecycle stream is filtered: %v", life.params)
	}
	c.stream()
	// A window opens in the other session: unlinked, in tmux's words.
	work := m.session("work")
	work.with(func() {
		work.windows = append(work.windows, &fakeWindow{id: "w2-0001", ws: 3, w: 10, h: 10})
	})
	life.ch <- []byte(`{"type":"window-created","session":"work","window":"w2-0001"}`)
	c.until(`^%unlinked-window-add @\d+$`)
	// The api session ends.
	m.mu.Lock()
	m.sessions = m.sessions[:1]
	m.mu.Unlock()
	life.ch <- []byte(`{"type":"session-closed","session":"api"}`)
	c.until(`^%sessions-changed$`)
	c.until(`^%exit$`)
	<-c.done
}

// TestControlModeNoEventStream falls back to reading the session on a
// timer when the daemon gives no event stream.
func TestControlModeNoEventStream(t *testing.T) {
	h := newHarness(t)
	c := startControl(t, h, false, "-C", "attach-session")
	c.block("0")
	c.next()
	h.fake.with(func() { h.fake.wsNames[1] = "main" })
	if l := c.until(`^%window-renamed`); l != "%window-renamed @1 main" {
		t.Errorf("window-renamed: %q", l)
	}
	_ = c.stdin.Close()
	<-c.done
}

func TestParseCommandLine(t *testing.T) {
	for _, c := range []struct {
		in   string
		want [][]string
		err  bool
	}{
		{`list-windows -F '#{window_id} x'`, [][]string{{"list-windows", "-F", "#{window_id} x"}}, false},
		{`send-keys -t %1 "a \"b\"" Enter; kill-pane`, [][]string{{"send-keys", "-t", "%1", `a "b"`, "Enter"}, {"kill-pane"}}, false},
		{`send-keys a\;b ; ls`, [][]string{{"send-keys", "a;b"}, {"ls"}}, false},
		{`display -p '' # a comment`, [][]string{{"display", "-p", ""}}, false},
		{`   `, nil, false},
		{`bad 'quote`, nil, true},
	} {
		got, err := parseCommandLine(c.in)
		if (err != nil) != c.err || !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseCommandLine(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// TestLayoutChecksum checks the checksum against a layout tmux printed.
func TestLayoutChecksum(t *testing.T) {
	if got := layoutChecksum("159x48,0,0{79x48,0,0,79x48,80,0}"); got != 0xbb62 {
		t.Errorf("checksum = %04x, want bb62", got)
	}
}

// TestControlModeReattachFollowsTheNewSession attaches a client to one
// session, then to another from stdin. Its event streams must follow: the
// output of the new session reaches it, and a scratch terminal in it raises
// no window notification.
func TestControlModeReattachFollowsTheNewSession(t *testing.T) {
	h := newHarness(t)
	m := newMultiFake(t, "work", "api")
	h.shim.Caller = m
	h.shim.AllSessions = true
	h.shim.Session, h.shim.Window, h.shim.TmuxPane = "", "", ""
	c := startControl(t, h, true, "-C", "attach-session", "-t", "work")
	c.block("0")
	c.until(`^%session-changed \$\d+ work$`)
	c.stream()
	if out := c.stream(); out.params["session"] != "work" {
		t.Fatalf("first output stream is for %v", out.params["session"])
	}
	c.send("attach-session -t api")
	c.block("1")
	c.until(`^%session-changed \$\d+ api$`)
	life := c.stream()
	out := c.stream()
	if out.params["session"] != "api" {
		t.Fatalf("after attaching to api the output stream is for %v", out.params["session"])
	}
	out.ch <- []byte(`{"type":"output","session":"api","window":"api-main-0001","bytes":3}`)
	if l := c.until(`^%output `); l != "%output "+PaneID("api-main-0001")+" " {
		t.Errorf("output: %q", l)
	}
	api := m.session("api")
	api.with(func() {
		api.windows = append(api.windows, &fakeWindow{id: "api-scratch", ws: 1000, scratch: true, w: 10, h: 10})
		api.windows = append(api.windows, &fakeWindow{id: "api-side", ws: 2, w: 10, h: 10})
	})
	life.ch <- []byte(`{"type":"window-created","session":"api","window":"api-side"}`)
	l := c.until(`^%(window-add|unlinked-window-add)`)
	if !strings.HasSuffix(l, "002") || strings.HasPrefix(l, "%unlinked") {
		t.Errorf("window-add: %q, want the api session's workspace 2 and nothing for the scratch terminal", l)
	}
	_ = c.stdin.Close()
	<-c.done
}

// TestControlModeReadOnlyStaysReadOnly refuses attach -f !read-only from a
// read-only client, as tmux does.
func TestControlModeReadOnlyStaysReadOnly(t *testing.T) {
	h := newHarness(t)
	c := startControl(t, h, true, "-C", "attach-session", "-f", "read-only")
	c.block("0")
	c.next()
	c.send("attach-session -f !read-only")
	if body, end := c.block("1"); end != "%error" || !reflect.DeepEqual(body, []string{"client is read-only"}) {
		t.Errorf("attach -f !read-only = %q %s", body, end)
	}
	c.send("kill-pane -t " + PaneID("leader-0001"))
	if _, end := c.block("1"); end != "%error" || h.fake.called("close-window") {
		t.Error("the client became writable")
	}
	_ = c.stdin.Close()
	<-c.done
}
