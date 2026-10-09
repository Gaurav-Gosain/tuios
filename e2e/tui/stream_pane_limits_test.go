package tuie2e

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// What stream-pane refuses, and what it bounds. The happy path is in
// stream_pane_test.go; these hold the verb to the limits around it: the link
// policy on every frame, a pane's grants on every frame and for as long as the
// stream lasts, the memory one client frame may hold, how fast a lease may
// resize a pane, and the end of a stream when its pane goes.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "stream-pane limits"):
//   - with maxPaneInputFrame back at 1 MiB, the daemon holds a megabyte for
//     each stream that announced one, and TestStreamPaneClientFrameMemory
//     fails at "the daemon grew".
//   - with PTY.SetLease resizing on every call, a burst of lease frames
//     resizes the pane once for each, and TestStreamPaneLeaseChurn fails at
//     "resized the pane".
//   - with the access recheck left out of paneStream.stream, a pane whose
//     read grant is taken away keeps its stream, and
//     TestStreamPaneFromAPane fails at "the stream outlived the read grant".

// limitsConfig gives the phone every capability and the reader only list.
const limitsConfig = "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n\n" +
	"[hosts.phone]\nallow = [\"list\", \"mail\", \"open\", \"write\", \"respond\"]\n\n" +
	"[hosts.reader]\nallow = [\"list\"]\n"

// limitsDaemon starts a daemon with limitsConfig and the session the
// stream_pane helpers read, with two panes. It returns base and the two
// window ids.
func limitsDaemon(t *testing.T) (string, *phoneLink, *linkStream, string, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(limitsConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new", streamSession, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	link := startPhoneLink(t, base)
	ctl := link.open(t, true)
	if _, verr := ctl.call(t, "new-window", map[string]any{"session": streamSession}); verr != nil {
		t.Fatalf("new-window: %v", verr)
	}
	res, verr := ctl.call(t, "list-windows", map[string]any{"session": streamSession})
	if verr != nil {
		t.Fatalf("list-windows: %v", verr)
	}
	wins := res["windows"].([]any)
	if len(wins) != 2 {
		t.Fatalf("want 2 windows, got %d", len(wins))
	}
	return base, link, ctl, wins[0].(map[string]any)["window_id"].(string), wins[1].(map[string]any)["window_id"].(string)
}

// frameWithin reads one pane frame, or reports a timeout.
func (s *linkStream) frameWithin(wait time.Duration) (byte, []byte, error) {
	s.timeout = wait
	defer func() { s.timeout = 0 }()
	return s.tryFrame()
}

// nextOf reads frames until one of type typ and returns its payload.
func (s *linkStream) nextOf(t *testing.T, typ byte, wait time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		got, payload, err := s.frameWithin(time.Until(deadline))
		if err != nil {
			t.Fatalf("waiting for a %c frame: %v", typ, err)
		}
		if got == typ {
			return payload
		}
	}
	t.Fatalf("no %c frame within %s", typ, wait)
	return nil
}

// frameCode is the code of an E frame.
func frameCode(t *testing.T, payload []byte) string {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(payload, &e); err != nil {
		t.Fatalf("an E frame is not JSON: %q", payload)
	}
	return e.Code
}

// TestStreamPaneHeldToTheLinkPolicy: a peer allowed only list may stream a
// pane, and every input and lease frame it sends is refused. A presence
// nonce is good only on the kind of stream it was made on.
func TestStreamPaneHeldToTheLinkPolicy(t *testing.T) {
	base, link, ctl, w1, w2 := limitsDaemon(t)
	reader := startLinkAs(t, base, "reader")

	rs := reader.open(t, true)
	if _, verr := rs.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w1, "lease_cols": 40, "lease_rows": 10}); verr == nil || verr.Code != "forbidden" {
		t.Fatalf("a lease from a peer without write: want forbidden, got %v", verr)
	}
	rs.close()

	rs = reader.open(t, true)
	if _, verr := rs.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w1}); verr != nil {
		t.Fatalf("stream-pane with list: %v", verr)
	}
	rs.nextOf(t, 'S', uiTimeout)
	rs.send(t, 'I', []byte("echo READER-$((3*3))\r"))
	if code := frameCode(t, rs.nextOf(t, 'E', uiTimeout)); code != "forbidden" {
		t.Fatalf("an input frame from a peer without write: want forbidden, got %s", code)
	}
	rs.send(t, 'L', []byte{0, 30, 0, 8})
	if code := frameCode(t, rs.nextOf(t, 'E', uiTimeout)); code != "forbidden" {
		t.Fatalf("a lease frame from a peer without write: want forbidden, got %s", code)
	}
	spType(t, ctl, w1, "echo SZ-$(stty size | tr ' ' x)\n")
	if got := spSize(t, ctl, w1, "SZ-"); got == "8x30" {
		t.Fatalf("a refused lease frame resized the pane to %s", got)
	}
	if strings.Contains(strings.Join(spLines(t, ctl, w1), "\n"), "READER-9") {
		t.Fatalf("a refused input frame reached the pane")
	}
	rs.close()

	// A presence for a peer with list only: the nonce is made, and the
	// verbs that use it still need their own capability.
	rc := reader.open(t, true)
	if _, verr := rc.call(t, "attach-presence", nil); verr != nil {
		t.Fatalf("attach-presence with list: %v", verr)
	}
	if _, verr := rc.call(t, "reply-approval", map[string]any{"request_id": "x", "decision": "once", "human_nonce": "00"}); verr == nil || verr.Code != "forbidden" {
		t.Fatalf("reply-approval from a peer without respond: want forbidden, got %v", verr)
	}
	rc.close()

	// The nonce of a presence on a vouched stream does not answer from a
	// stream the hub did not vouch for, on the same link and process.
	pres, verr := ctl.call(t, "attach-presence", nil)
	if verr != nil {
		t.Fatalf("attach-presence: %v", verr)
	}
	hook := startApprovalHook(t, base, w2)
	id := spApproval(t, ctl, w2)
	plain := link.open(t, false)
	if _, verr := plain.call(t, "reply-approval", map[string]any{"request_id": id, "decision": "once", "human_nonce": pres["human_nonce"]}); verr == nil || verr.Code != "not_human" {
		t.Fatalf("a presence nonce on a stream the hub did not vouch for: want not_human, got %v", verr)
	}
	plain.close()
	if _, verr := ctl.call(t, "reply-approval", map[string]any{"request_id": id, "decision": "deny", "human_nonce": pres["human_nonce"]}); verr != nil {
		t.Fatalf("the presence nonce on its own stream: %v", verr)
	}
	hook.wait(t)
}

// TestStreamPaneClientFrameMemory: what one client frame may make the daemon
// hold. Each stream announces an input frame of 1 MiB and sends all of it but
// the last byte, so a daemon that takes such a frame holds a megabyte per
// stream while it waits for the rest. One that refuses it holds nothing.
func TestStreamPaneClientFrameMemory(t *testing.T) {
	base, link, ctl, w1, _ := limitsDaemon(t)
	const streams = 96
	const announced = 1 << 20
	var opened []*linkStream
	defer func() {
		for _, s := range opened {
			s.close()
		}
	}()
	for range streams {
		s := link.open(t, true)
		opened = append(opened, s)
		if _, verr := s.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w1}); verr != nil {
			t.Fatalf("stream-pane: %v", verr)
		}
	}
	before := daemonRSS(t, base)
	body := make([]byte, 5+announced-1)
	body[0] = 'I'
	binary.BigEndian.PutUint32(body[1:5], announced)
	for i := 5; i < len(body); i++ {
		body[i] = 'a'
	}
	for _, s := range opened {
		s.write(t, body)
	}
	// Every byte has gone through the proxy once a verb on the control
	// stream answers after them.
	time.Sleep(2 * time.Second)
	if _, verr := ctl.call(t, "session-info", map[string]any{"session": streamSession}); verr != nil {
		t.Fatalf("session-info: %v", verr)
	}
	after := daemonRSS(t, base)
	grew := (after - before) / 1024
	t.Logf("daemon RSS %d KiB -> %d KiB (+%d MiB) with %d streams that each announced %d bytes", before, after, grew, streams, announced)
	if grew > streams/3 {
		t.Fatalf("the daemon grew by %d MiB for %d streams that each announced a 1 MiB frame and never finished it", grew, streams)
	}

	// The bound is on one frame, not on input: a frame at the limit is
	// typed, and a longer paste goes as more frames.
	s := link.open(t, true)
	if _, verr := s.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w1}); verr != nil {
		t.Fatalf("stream-pane: %v", verr)
	}
	s.nextOf(t, 'S', uiTimeout)
	// A program reads the frame in raw mode and counts it. A shell line
	// would not do: a shell without line editing (dash on Linux CI) reads
	// in canonical mode, where the terminal keeps only 4095 bytes of a line.
	spType(t, ctl, w1, "stty raw -echo; head -c 65536 | wc -c | sed 's/^ */LIMIT-/'; stty sane\n")
	time.Sleep(500 * time.Millisecond)
	s.send(t, 'I', []byte(strings.Repeat("x", 64<<10)))
	spWait(t, ctl, w1, "LIMIT-65536")
	s.close()
}

// TestStreamPaneLeaseChurn: a client that sends lease frames as fast as it
// can resizes the pane at a bounded rate, and the pane ends at the last
// lease it sent.
func TestStreamPaneLeaseChurn(t *testing.T) {
	_, link, ctl, w1, _ := limitsDaemon(t)

	watch := link.open(t, true)
	if _, verr := watch.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w1}); verr != nil {
		t.Fatalf("stream-pane: %v", verr)
	}
	watch.nextOf(t, 'S', uiTimeout)
	leaser := link.open(t, true)
	if _, verr := leaser.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w1}); verr != nil {
		t.Fatalf("stream-pane: %v", verr)
	}
	leaser.nextOf(t, 'S', uiTimeout)

	const burst = 300
	var frames []byte
	for i := range burst {
		cols := byte(40 + i%2)
		frames = append(frames, 'L', 0, 0, 0, 4, 0, cols, 0, 10)
	}
	frames = append(frames, 'L', 0, 0, 0, 4, 0, 45, 0, 12)
	start := time.Now()
	leaser.write(t, frames)

	resizes := 0
	last := ""
	for {
		typ, payload, err := watch.frameWithin(1500 * time.Millisecond)
		if errors.Is(err, errSPTimeout) {
			break
		}
		if err != nil {
			t.Fatalf("read the watching stream: %v", err)
		}
		if typ == 'R' {
			resizes++
			last = fmt.Sprintf("%dx%d", binary.BigEndian.Uint16(payload[8:10]), binary.BigEndian.Uint16(payload[10:12]))
		}
	}
	took := time.Since(start)
	t.Logf("%d lease frames resized the pane %d times in %s; last size %s", burst+1, resizes, took.Round(time.Millisecond), last)
	if limit := int(took/(100*time.Millisecond)) + 3; resizes > limit {
		t.Fatalf("%d lease frames resized the pane %d times in %s, more than %d", burst+1, resizes, took.Round(time.Millisecond), limit)
	}
	if last != "45x12" {
		t.Fatalf("after the burst the pane is %s, want the last lease 45x12", last)
	}
	spType(t, ctl, w1, "echo SZ-$(stty size | tr ' ' x)\n")
	if got := spSize(t, ctl, w1, "SZ-"); got != "12x45" {
		t.Fatalf("the shell sees %s, want 12x45", got)
	}
	leaser.close()
	watch.close()
}

// TestStreamPaneEndsWithItsWindow: a stream ends with an X frame when its
// window closes or its session is killed, lease or not.
func TestStreamPaneEndsWithItsWindow(t *testing.T) {
	base, link, _, w1, w2 := limitsDaemon(t)

	s2 := link.open(t, true)
	if _, verr := s2.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w2, "lease_cols": 30, "lease_rows": 8}); verr != nil {
		t.Fatalf("stream-pane: %v", verr)
	}
	s2.nextOf(t, 'S', uiTimeout)
	s1 := link.open(t, true)
	if _, verr := s1.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w1}); verr != nil {
		t.Fatalf("stream-pane: %v", verr)
	}
	s1.nextOf(t, 'S', uiTimeout)

	if out, err := tuiosCLI(t, base, "close-window", "-s", streamSession, w2); err != nil {
		t.Fatalf("close-window: %v\n%s", err, out)
	}
	t.Logf("closed window: X %q", s2.nextOf(t, 'X', uiTimeout))
	if _, _, err := s2.frameWithin(uiTimeout); err == nil || errors.Is(err, errSPTimeout) {
		t.Fatalf("the stream of a closed window stayed open: %v", err)
	}

	if out, err := tuiosCLI(t, base, "kill-session", streamSession); err != nil {
		t.Fatalf("kill-session: %v\n%s", err, out)
	}
	t.Logf("killed session: X %q", s1.nextOf(t, 'X', uiTimeout))
	if _, _, err := s1.frameWithin(uiTimeout); err == nil || errors.Is(err, errSPTimeout) {
		t.Fatalf("the stream of a killed session stayed open: %v", err)
	}
}

// paneClient is a stream-pane client that runs inside a pane, as an agent
// would. It logs what it got, one line each, to the file named by its
// second argument.
const paneClient = `import json, os, socket, struct, sys
win, path = sys.argv[1], sys.argv[2]
log = open(path, "w", buffering=1)
s = socket.socket(socket.AF_UNIX)
s.connect(os.environ["TUIOS_SOCKET"])
r = s.makefile("rb")
def call(req):
    s.sendall((json.dumps(req) + "\n").encode())
    return r.readline().decode().strip()
log.write("PRESENCE " + call({"id": 1, "verb": "attach-presence"}) + "\n")
log.write("REPLY " + call({"id": 2, "verb": "stream-pane", "params": {"window": win}}) + "\n")
def frame(t, p):
    s.sendall(t + struct.pack(">I", len(p)) + p)
frame(b"I", b"echo FROMPANE-$((5*5))\r")
frame(b"L", struct.pack(">HH", 30, 8))
seen = 0
while True:
    h = r.read(5)
    if len(h) < 5:
        log.write("EOF\n")
        break
    n = struct.unpack(">I", h[1:5])[0]
    p = r.read(n)
    t = chr(h[0])
    if t in "EX":
        log.write(t + " " + p.decode(errors="replace") + "\n")
    elif seen < 20:
        seen += 1
        log.write(t + "\n")
`

// TestStreamPaneFromAPane: a process inside a pane that holds only the read
// grant may stream a sibling pane. It may not type into it, lease it, or hold
// a presence, and its stream ends once the read grant is taken away.
func TestStreamPaneFromAPane(t *testing.T) {
	base, _, ctl, w1, w2 := limitsDaemon(t)
	script := filepath.Join(base, "paneclient.py")
	logPath := filepath.Join(base, "paneclient.log")
	if err := os.WriteFile(script, []byte(paneClient), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", streamSession, "-w", w1, "--grants", "read"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	spType(t, ctl, w2, "echo SZ-$(stty size | tr ' ' x)\n")
	w2Size := spSize(t, ctl, w2, "SZ-")
	spType(t, ctl, w1, fmt.Sprintf("python3 %s %s %s\n", script, w2, logPath))

	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	waitLog := func(what string, ok func(string) bool) string {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for {
			got := readLog()
			if ok(got) {
				return got
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s; the pane client logged:\n%s\npane:\n%s", what, got, strings.Join(spLines(t, ctl, w1), "\n"))
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	got := waitLog("the pane client never got two refusals", func(s string) bool {
		return strings.Count(s, "\nE ") >= 2
	})
	if !strings.Contains(got, "PRESENCE ") || !strings.Contains(got, `"forbidden"`) || strings.Contains(got, "human_nonce") {
		t.Fatalf("attach-presence from inside a pane was not refused:\n%s", got)
	}
	if !strings.Contains(got, `"type":"pane_stream"`) {
		t.Fatalf("a pane with the read grant could not stream its sibling:\n%s", got)
	}
	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasPrefix(line, "E ") && !strings.Contains(line, `"forbidden"`) {
			t.Fatalf("a frame from the pane was refused with another code: %s", line)
		}
	}
	if strings.Contains(strings.Join(spLines(t, ctl, w2), "\n"), "FROMPANE-25") {
		t.Fatalf("a pane without the write grant typed into its sibling through stream-pane")
	}
	spType(t, ctl, w2, "clear; echo SZ2-$(stty size | tr ' ' x)\n")
	if got := spSize(t, ctl, w2, "SZ2-"); got != w2Size {
		t.Fatalf("a pane without the write grant leased its sibling from %s to %s", w2Size, got)
	}

	// The grant goes, and the stream with it.
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", streamSession, "-w", w1, "--grants", "none"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	spType(t, ctl, w2, "echo AFTER-REVOKE\n")
	got = waitLog("the stream outlived the read grant", func(s string) bool {
		return strings.Contains(s, "\nX ") && strings.Contains(s, "\nEOF")
	})
	t.Logf("the pane client logged:\n%s", got)
}

// TestStreamPaneLeaseOnTheDesktop: an attached tuios client draws a pane a
// phone leased smaller, and draws it at full size again when the lease ends.
// The frames go to TUIOS_E2E_FRAMES as stream-lease-held and
// stream-lease-released.
func TestStreamPaneLeaseOnTheDesktop(t *testing.T) {
	base, link, ctl, w1, _ := limitsDaemon(t)
	term := attachIn(t, base, streamSession, startOpts{})
	// Tiled, so both panes show side by side.
	enableTiling(t, term)

	ps := link.open(t, true)
	if _, verr := ps.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w1, "lease_cols": 30, "lease_rows": 6}); verr != nil {
		t.Fatalf("stream-pane: %v", verr)
	}
	ps.nextOf(t, 'S', uiTimeout)
	spType(t, ctl, w1, "clear; echo SZ-$(stty size | tr ' ' x); seq 101 102; echo LEASED-END\n")
	if got := spSize(t, ctl, w1, "SZ-"); got != "6x30" {
		t.Fatalf("the leased pane is %s, want 6x30", got)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "LEASED-END", "SZ-6x30") }, uiTimeout); err != nil {
		t.Fatalf("the desktop never drew the leased pane: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "stream-lease-held")

	ps.close()
	spType(t, ctl, w1, "clear; echo BACK-$(stty size | tr ' ' x); echo RELEASED-END\n")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "RELEASED-END") }, uiTimeout); err != nil {
		t.Fatalf("the desktop never drew the released pane: %v\n%s", err, term.Snapshot())
	}
	if got := spSize(t, ctl, w1, "BACK-"); got == "6x30" {
		t.Fatalf("the pane stayed at the leased size after the stream closed")
	}
	saveFrame(t, term, "stream-lease-released")
	alive(t, term, "after the lease")
}
