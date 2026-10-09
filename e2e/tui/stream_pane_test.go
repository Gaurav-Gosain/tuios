package tuie2e

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/charmbracelet/x/ansi"
)

// The phone's path into a daemon, end to end: a real daemon, the real
// `tuios stdio-proxy --as phone` a phone runs over ssh, and this test playing
// the phone on the proxy's stdin and stdout with the federation framing.
// Every stream it opens says {"human":true,"from":"phone"}, so it lands on
// the link-human socket the way a phone's does.
//
// It covers stream-pane (snapshot, live output numbered by seq, input, resume
// from a seq, a fresh snapshot when the ring no longer holds it), the
// per-pane size lease (only that pane changes, a desktop resize is clamped,
// the pane goes back on close), and attach-presence (its nonce answers a held
// approval from the real hook, and stops answering once its connection
// closes).
//
// The artifact is a transcript under artifactDir: every reply line, every
// frame header with its seq, the screen the snapshot painted on a fresh
// emulator, and the daemon's capture of the same pane.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with the presence clause removed from matchHumanNonceClient
//     (internal/session/human_sender.go), reply-approval with the presence
//     nonce answers not_human and the test fails at "the presence nonce did
//     not answer the held approval".
//   - with PTY.Resize recording the asked size but not applying
//     leasedSizeLocked, the desktop resize grows the leased pane and the test
//     fails at "a resize under the lease".
//   - with SetLease's release left out of paneStream.close, the pane stays at
//     the leased size and the test fails at "the pane did not go back".
//   - with the decModes call left out of snapshotVT, the snapshot sets no
//     input mode and the test fails at "the snapshot did not set DEC mode".
//   - with the alternate screen block left out of snapshotVT, the test fails
//     at "the snapshot of a pane on the alternate screen".
//   - with the recoverGap call left out of paneStream.stream, the stream goes
//     silent after the phone falls behind and the test fails at "read a pane
//     frame: stream read timed out".

// streamSession is the session every stream in the test reads.
const streamSession = "phone"

// TestPhoneStreamsAPaneAndAnswersTheInbox is the whole path above.
func TestPhoneStreamsAPaneAndAnswersTheInbox(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	cfg := "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n\n" +
		"[hosts.phone]\nallow = [\"list\", \"mail\", \"open\", \"write\", \"respond\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new", streamSession, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	tr := newSPTranscript(t)
	defer tr.save()

	link := startPhoneLink(t, base)
	ctl := link.open(t, true)
	call := func(c *linkStream, verb string, params any) map[string]any {
		t.Helper()
		res, verr := c.call(t, verb, params)
		if verr != nil {
			t.Fatalf("%s: %v", verb, verr)
		}
		return res
	}

	// Two panes, so the lease can be seen to leave the other alone.
	call(ctl, "new-window", map[string]any{"session": streamSession})
	wins := call(ctl, "list-windows", map[string]any{"session": streamSession})["windows"].([]any)
	if len(wins) != 2 {
		t.Fatalf("want 2 windows, got %d", len(wins))
	}
	w1 := wins[0].(map[string]any)["window_id"].(string)
	w2 := wins[1].(map[string]any)["window_id"].(string)
	sizeBefore := spSessionSize(t, ctl)
	spType(t, ctl, w2, "echo SZ-$(stty size | tr ' ' x)\n")
	w2Size := spSize(t, ctl, w2, "SZ-")

	// History, then colour and the input modes on the screen.
	spType(t, ctl, w1, "seq 1 60; printf '\\033[?1h\\033[?2004h\\033[?1000h\\033[?1006h\\033=\\033[31mRED\\033[0m \\033[38;2;1;2;3mTRUE\\033[0m\\n'\n")
	spWait(t, ctl, w1, "RED TRUE")

	// --- Snapshot.
	ps := link.open(t, true)
	reply := ps.streamPane(t, tr, map[string]any{"session": streamSession, "window": w1})
	if reply["mode"] != "snapshot" || reply["window"] != w1 {
		t.Fatalf("first stream-pane: want a snapshot of %s, got %v", w1, reply)
	}
	boot := reply["boot_id"].(string)
	cols, rows := int(reply["cols"].(float64)), int(reply["rows"].(float64))
	typ, payload := ps.frame(t, tr)
	if typ != 'S' {
		t.Fatalf("the first frame is %q, want S", typ)
	}
	seq := int64(binary.BigEndian.Uint64(payload[0:8]))
	if seq != int64(reply["seq"].(float64)) || int(binary.BigEndian.Uint16(payload[8:10])) != cols || int(binary.BigEndian.Uint16(payload[10:12])) != rows {
		t.Fatalf("S frame header (seq %d, %dx%d) disagrees with the reply %v", seq, binary.BigEndian.Uint16(payload[8:10]), binary.BigEndian.Uint16(payload[10:12]), reply)
	}
	emu := vt.NewEmulator(cols, rows)
	_, _ = emu.Write(payload[12:])
	tr.add("snapshot screen", spScreen(emu))
	want := spLines(t, ctl, w1)
	tr.add("daemon capture at the snapshot", strings.Join(want, "\n"))
	if got := spScreen(emu); got != strings.Join(want, "\n") {
		t.Fatalf("the snapshot painted a different screen than the daemon holds\n--- snapshot\n%s\n--- daemon\n%s", got, strings.Join(want, "\n"))
	}
	if emu.ScrollbackLen() == 0 || !strings.Contains(spHistory(emu), "\n1\n") {
		t.Fatalf("the snapshot carried no history above the screen:\n%s", spHistory(emu))
	}
	modes := emu.GetModes()
	for _, m := range []int{1, 2004, 1000, 1006, 66} {
		if !modes[m] {
			t.Errorf("the snapshot did not set DEC mode %d; modes %v", m, modes)
		}
	}
	checkSnapColours(t, emu)

	// --- Live output, numbered by seq with no hole and no overlap.
	spType(t, ctl, w1, "echo LIVE-$((40+2))\n")
	seq = ps.readUntil(t, tr, emu, seq, "\nLIVE-42")

	// --- Input typed from the phone.
	ps.send(t, 'I', []byte("echo TYPED-$((6*7))\r"))
	seq = ps.readUntil(t, tr, emu, seq, "\nTYPED-42")
	spWait(t, ctl, w1, "TYPED-42")
	settleAndCompare(t, ps, tr, emu, &seq, ctl, w1, "after live output and input")

	// --- Resume from the seq reached.
	ps.close()
	spType(t, ctl, w1, "echo AFTER-$((1+1))\n")
	spWait(t, ctl, w1, "AFTER-2")
	ps = link.open(t, true)
	reply = ps.streamPane(t, tr, map[string]any{"session": streamSession, "window": w1, "from_seq": seq, "boot_id": boot})
	if reply["mode"] != "resume" || int64(reply["seq"].(float64)) != seq {
		t.Fatalf("stream-pane from seq %d: want resume at that seq, got %v", seq, reply)
	}
	typ, payload = ps.frame(t, tr)
	if typ != 'O' || int64(binary.BigEndian.Uint64(payload[0:8]))-int64(len(payload)-8) != seq {
		t.Fatalf("the first resumed frame is %q starting at %d, want O starting at %d", typ, int64(binary.BigEndian.Uint64(payload[0:8]))-int64(len(payload)-8), seq)
	}
	_, _ = emu.Write(payload[8:])
	seq = int64(binary.BigEndian.Uint64(payload[0:8]))
	settleAndCompare(t, ps, tr, emu, &seq, ctl, w1, "after the resume")

	// A boot id from another daemon start is not resumed.
	other := link.open(t, true)
	if r := other.streamPane(t, tr, map[string]any{"session": streamSession, "window": w1, "from_seq": seq, "boot_id": "another-boot"}); r["mode"] != "snapshot" {
		t.Fatalf("a foreign boot_id resumed: %v", r)
	}
	other.close()

	// --- The lease: only this pane changes, a desktop resize is clamped,
	// and the pane goes back when the stream closes.
	ps.send(t, 'L', []byte{0, 40, 0, 10})
	seq = ps.readResize(t, tr, emu, seq, 40, 10)
	ps.send(t, 'I', []byte("echo SZ-$(stty size | tr ' ' x)\r"))
	seq = ps.readUntil(t, tr, emu, seq, "\nSZ-10x40")
	spType(t, ctl, w2, "clear; echo SZ2-$(stty size | tr ' ' x)\n")
	if got := spSize(t, ctl, w2, "SZ2-"); got != w2Size {
		t.Fatalf("the lease on one pane changed another pane from %s to %s", w2Size, got)
	}
	if after := spSessionSize(t, ctl); after != sizeBefore {
		t.Fatalf("the lease changed the session size from %s to %s", sizeBefore, after)
	}
	call(ctl, "resize", map[string]any{"session": streamSession, "window": w1, "width": 100, "height": 30})
	ps.send(t, 'I', []byte("echo RS-$(stty size | tr ' ' x)\r"))
	seq = ps.readUntil(t, tr, emu, seq, "\nRS-")
	if !strings.Contains(spAll(emu), "RS-10x40") {
		t.Fatalf("a resize under the lease changed the leased pane:\n%s", spScreen(emu))
	}
	ps.close()
	spType(t, ctl, w1, "echo BACK-$(stty size | tr ' ' x)\n")
	if !spShows(t, ctl, w1, "BACK-30x100") {
		t.Fatalf("the pane did not go back to the size asked for when the stream closed:\n%s", strings.Join(spLines(t, ctl, w1), "\n"))
	}

	// --- A ring that no longer holds the seq gives a fresh snapshot.
	spType(t, ctl, w1, "seq 1 20000; echo FLOOD-$((1+2))\n")
	spWait(t, ctl, w1, "FLOOD-3")
	ps = link.open(t, true)
	if r := ps.streamPane(t, tr, map[string]any{"session": streamSession, "window": w1, "from_seq": seq, "boot_id": boot}); r["mode"] != "snapshot" {
		t.Fatalf("a seq the ring rolled past resumed: %v", r)
	}
	if typ, _ := ps.frame(t, tr); typ != 'S' {
		t.Fatalf("after a rolled seq the first frame is %q, want S", typ)
	}
	ps.close()

	// --- A phone that falls behind by more than its queue holds gets a
	// fresh snapshot, never a hole. The link stops reading while the pane
	// prints more than the daemon queues for one stream (8 MiB) and its
	// 64 KiB ring, so the daemon cannot resume the stream from the ring.
	ps = link.open(t, true)
	reply = ps.streamPane(t, tr, map[string]any{"session": streamSession, "window": w1})
	typ, payload = ps.frame(t, tr)
	if typ != 'S' {
		t.Fatalf("the first frame is %q, want S", typ)
	}
	seq = int64(binary.BigEndian.Uint64(payload[0:8]))
	emu = vt.NewEmulator(int(reply["cols"].(float64)), int(reply["rows"].(float64)))
	_, _ = emu.Write(payload[12:])
	link.pause()
	if out, err := tuiosCLI(t, base, "send-text", "-s", streamSession, "-w", w1, "seq 1 1500000; echo GAP-$((3+4))\n"); err != nil {
		link.resume()
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", streamSession, "-w", w1)
		if strings.Contains(out, "\nGAP-7") {
			break
		}
		if time.Now().After(deadline) {
			link.resume()
			t.Fatalf("the flood never finished:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	link.resume()
	snaps := 0
	for !strings.Contains(spAll(emu), "\nGAP-7") {
		typ, payload := ps.frame(t, tr)
		if typ == 'S' {
			snaps++
			if at := int64(binary.BigEndian.Uint64(payload[0:8])); at <= seq {
				t.Fatalf("the new snapshot is at %d, not past the stream's %d", at, seq)
			}
			seq = int64(binary.BigEndian.Uint64(payload[0:8]))
			emu = vt.NewEmulator(int(binary.BigEndian.Uint16(payload[8:10])), int(binary.BigEndian.Uint16(payload[10:12])))
			_, _ = emu.Write(payload[12:])
			continue
		}
		seq = spApply(t, emu, seq, typ, payload)
	}
	if snaps == 0 {
		t.Fatalf("the phone fell behind and was never sent a new snapshot")
	}
	settleAndCompare(t, ps, tr, emu, &seq, ctl, w1, "after falling behind")
	ps.close()

	// --- A program on the alternate screen: the snapshot switches to it,
	// with the shell's screen kept underneath.
	spType(t, ctl, w1, "clear; echo UNDER-$((2+2)); printf '\\033[?1049h\\033[2J\\033[3;5HALT%sSCREEN' -; sleep 600\n")
	spWait(t, ctl, w1, "ALT-SCREEN")
	ps = link.open(t, true)
	reply = ps.streamPane(t, tr, map[string]any{"session": streamSession, "window": w1})
	typ, payload = ps.frame(t, tr)
	if typ != 'S' {
		t.Fatalf("the first frame is %q, want S", typ)
	}
	alt := vt.NewEmulator(int(reply["cols"].(float64)), int(reply["rows"].(float64)))
	_, _ = alt.Write(payload[12:])
	tr.add("alternate screen snapshot", spScreen(alt))
	if !alt.IsAltScreen() || spEmuLine(alt, 2) != "    ALT-SCREEN" {
		t.Fatalf("the snapshot of a pane on the alternate screen: alt %v, screen\n%s", alt.IsAltScreen(), spScreen(alt))
	}
	_, _ = alt.Write([]byte("\x1b[?1049l"))
	if !strings.Contains(spScreen(alt), "UNDER-4") {
		t.Fatalf("leaving the alternate screen did not bring back the shell's screen:\n%s", spScreen(alt))
	}
	ps.close()
	// Ctrl+C ends the sleep, and the program's screen with it.
	spType(t, ctl, w1, "\x03")

	// --- attach-presence.
	plain := link.open(t, false)
	if _, verr := plain.call(t, "attach-presence", nil); verr == nil || verr.Code != "forbidden" {
		t.Fatalf("attach-presence on a stream the hub did not vouch for: want forbidden, got %v", verr)
	}
	plain.close()
	pres := call(ctl, "attach-presence", nil)
	tr.add("attach-presence reply", fmt.Sprint(pres))
	nonce := pres["human_nonce"].(string)
	if pres["type"] != "presence" || nonce == "" || pres["client_id"] == "" {
		t.Fatalf("attach-presence reply: %v", pres)
	}
	if after := spSessionSize(t, ctl); after != sizeBefore {
		t.Fatalf("the presence changed the session size from %s to %s", sizeBefore, after)
	}

	hook := startApprovalHook(t, base, w2)
	id := spApproval(t, ctl, w2)
	if _, verr := ctl.call(t, "reply-approval", map[string]any{"request_id": id, "decision": "once", "human_nonce": "0000"}); verr == nil || verr.Code != "not_human" {
		t.Fatalf("a made-up nonce: want not_human, got %v", verr)
	}
	res, verr := ctl.call(t, "reply-approval", map[string]any{"request_id": id, "decision": "once", "human_nonce": nonce})
	if verr != nil || res["applied"] != true || res["answered_by"] != pres["client_id"] {
		t.Fatalf("the presence nonce did not answer the held approval: %v %v", res, verr)
	}
	tr.add("reply-approval with the presence nonce", fmt.Sprint(res))
	out := hook.wait(t)
	if !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("the hook printed %q, want the allow decision", out)
	}

	// The presence ends with its connection.
	ctl.close()
	hook = startApprovalHook(t, base, w2)
	ctl = link.open(t, true)
	id = spApproval(t, ctl, w2)
	if _, verr := ctl.call(t, "reply-approval", map[string]any{"request_id": id, "decision": "deny", "human_nonce": nonce}); verr == nil || verr.Code != "not_human" {
		t.Fatalf("the nonce of a closed presence: want not_human, got %v", verr)
	}
	pres = call(ctl, "attach-presence", map[string]any{"session": streamSession})
	if res, verr := ctl.call(t, "reply-approval", map[string]any{"request_id": id, "decision": "deny", "human_nonce": pres["human_nonce"]}); verr != nil || res["applied"] != true {
		t.Fatalf("a presence for the session did not answer: %v %v", res, verr)
	}
	if out := hook.wait(t); !strings.Contains(out, `"behavior":"deny"`) {
		t.Fatalf("the hook printed %q, want the deny decision", out)
	}
}

// checkSnapColours finds RED and TRUE on the emulator and checks a palette
// colour stayed a palette index and a truecolour stayed truecolour.
func checkSnapColours(t *testing.T, emu *vt.Emulator) {
	t.Helper()
	found := 0
	for y := range emu.Height() {
		line := spEmuLine(emu, y)
		if x := strings.Index(line, "RED TRUE"); x >= 0 {
			found++
			if fg := emu.CellAt(x, y).Style.Fg; fg != ansi.BasicColor(1) {
				t.Errorf("RED is painted %#v, want palette colour 1", fg)
			}
			if fg := emu.CellAt(x+4, y).Style.Fg; !spSameRGB(fg, color.RGBA{1, 2, 3, 255}) {
				t.Errorf("TRUE is painted %#v, want truecolour 1,2,3", fg)
			}
		}
	}
	if found == 0 {
		t.Errorf("RED TRUE is not on the snapshot screen:\n%s", spScreen(emu))
	}
}

func spSameRGB(c color.Color, want color.RGBA) bool {
	if c == nil {
		return false
	}
	if _, ok := c.(ansi.BasicColor); ok {
		return false
	}
	if _, ok := c.(ansi.IndexedColor); ok {
		return false
	}
	r, g, b, _ := c.RGBA()
	return uint8(r>>8) == want.R && uint8(g>>8) == want.G && uint8(b>>8) == want.B
}

// settleAndCompare reads output until the pane is quiet, then holds the
// emulator fed by the frames to the daemon's capture of the pane.
func settleAndCompare(t *testing.T, ps *linkStream, tr *spTranscript, emu *vt.Emulator, seq *int64, ctl *linkStream, window, when string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		*seq = ps.drain(t, tr, emu, *seq, 300*time.Millisecond)
		want := strings.Join(spLines(t, ctl, window), "\n")
		got := spScreen(emu)
		if got == want {
			tr.add("stream screen "+when, got)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s the streamed screen differs from the daemon's\n--- stream\n%s\n--- daemon\n%s", when, got, want)
		}
	}
}

// --- The phone's end of the link.

// phoneLink is a stdio-proxy child and the frames on its stdio.
type phoneLink struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	mu     sync.Mutex
	next   uint32
	smu    sync.Mutex
	stream map[uint32]*linkStream
	gate   sync.Mutex
}

// startPhoneLink runs `tuios stdio-proxy --as phone` against the daemon under
// base, the command a phone runs over ssh.
func startPhoneLink(t *testing.T, base string) *phoneLink {
	t.Helper()
	cmd := exec.Command(tuiosBin, "stdio-proxy", "--as", "phone")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stdio-proxy: %v", err)
	}
	t.Cleanup(func() {
		_ = in.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	br := bufio.NewReader(out)
	line, err := br.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "TUIOS-LINK 1" {
		t.Fatalf("stdio-proxy preamble: %q %v", line, err)
	}
	l := &phoneLink{cmd: cmd, in: in, next: 1, stream: map[uint32]*linkStream{}}
	go l.readLoop(br)
	return l
}

// pause stops reading the proxy's output until resume, as a phone on a
// stalled network does. The proxy, and then the daemon, block on their
// writes.
func (l *phoneLink) pause()  { l.gate.Lock() }
func (l *phoneLink) resume() { l.gate.Unlock() }

// readLoop hands each data frame to its stream.
func (l *phoneLink) readLoop(br *bufio.Reader) {
	var head [9]byte
	for {
		l.gate.Lock()
		//nolint:staticcheck // an empty critical section: wait out a pause
		l.gate.Unlock()
		if _, err := io.ReadFull(br, head[:]); err != nil {
			l.smu.Lock()
			for _, s := range l.stream {
				s.end()
			}
			l.smu.Unlock()
			return
		}
		id := binary.BigEndian.Uint32(head[1:5])
		body := make([]byte, binary.BigEndian.Uint32(head[5:9]))
		if _, err := io.ReadFull(br, body); err != nil {
			return
		}
		l.smu.Lock()
		s := l.stream[id]
		l.smu.Unlock()
		if s == nil {
			continue
		}
		switch head[0] {
		case 2:
			s.deliver(body)
		case 3:
			s.end()
		}
	}
}

func (l *phoneLink) writeFrame(typ byte, id uint32, payload []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var head [9]byte
	head[0] = typ
	binary.BigEndian.PutUint32(head[1:5], id)
	binary.BigEndian.PutUint32(head[5:9], uint32(len(payload)))
	if _, err := l.in.Write(head[:]); err != nil {
		return err
	}
	_, err := l.in.Write(payload)
	return err
}

// open opens a stream, vouched for as the person when human is set.
func (l *phoneLink) open(t *testing.T, human bool) *linkStream {
	t.Helper()
	l.smu.Lock()
	id := l.next
	l.next += 2
	s := &linkStream{l: l, id: id, data: make(chan []byte, 4096), done: make(chan struct{})}
	l.stream[id] = s
	l.smu.Unlock()
	open := `{"from":"phone"}`
	if human {
		open = `{"human":true,"from":"phone"}`
	}
	if err := l.writeFrame(1, id, []byte(open)); err != nil {
		t.Fatalf("open stream %d: %v", id, err)
	}
	s.br = bufio.NewReader(s)
	return s
}

// linkStream is one stream: a verb connection, or a pane stream after its
// reply.
type linkStream struct {
	l       *phoneLink
	id      uint32
	data    chan []byte
	done    chan struct{}
	endOnce sync.Once
	rest    []byte
	br      *bufio.Reader
	timeout time.Duration
	nextID  int
}

func (s *linkStream) deliver(b []byte) {
	select {
	case s.data <- b:
	case <-s.done:
	}
}

func (s *linkStream) end() { s.endOnce.Do(func() { close(s.done) }) }

var errSPTimeout = errors.New("stream read timed out")

// Read blocks for at most the stream's timeout, uiTimeout when unset.
func (s *linkStream) Read(p []byte) (int, error) {
	if len(s.rest) == 0 {
		wait := s.timeout
		if wait == 0 {
			wait = uiTimeout
		}
		select {
		case b := <-s.data:
			s.rest = b
		case <-s.done:
			select {
			case b := <-s.data:
				s.rest = b
			default:
				return 0, io.EOF
			}
		case <-time.After(wait):
			return 0, errSPTimeout
		}
	}
	n := copy(p, s.rest)
	s.rest = s.rest[n:]
	return n, nil
}

func (s *linkStream) write(t *testing.T, b []byte) {
	t.Helper()
	for len(b) > 0 {
		n := min(len(b), 1<<20)
		if err := s.l.writeFrame(2, s.id, b[:n]); err != nil {
			t.Fatalf("write stream %d: %v", s.id, err)
		}
		b = b[n:]
	}
}

func (s *linkStream) close() {
	_ = s.l.writeFrame(3, s.id, nil)
	s.end()
}

// spVerbErr is a verb's error envelope.
type spVerbErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *spVerbErr) String() string { return e.Code + ": " + e.Message }

// call sends one verb and reads its reply line.
func (s *linkStream) call(t *testing.T, verb string, params any) (map[string]any, *spVerbErr) {
	t.Helper()
	s.nextID++
	req := map[string]any{"id": s.nextID, "verb": verb}
	if params != nil {
		req["params"] = params
	}
	line, _ := json.Marshal(req)
	s.write(t, append(line, '\n'))
	s.timeout = 30 * time.Second
	resp, err := s.br.ReadString('\n')
	s.timeout = 0
	if err != nil {
		t.Fatalf("%s: read reply: %v", verb, err)
	}
	var env struct {
		Result map[string]any `json:"result"`
		Error  *spVerbErr     `json:"error"`
	}
	if err := json.Unmarshal([]byte(resp), &env); err != nil {
		t.Fatalf("%s: decode %q: %v", verb, resp, err)
	}
	return env.Result, env.Error
}

// streamPane sends stream-pane and returns its result.
func (s *linkStream) streamPane(t *testing.T, tr *spTranscript, params map[string]any) map[string]any {
	t.Helper()
	res, verr := s.call(t, "stream-pane", params)
	if verr != nil {
		t.Fatalf("stream-pane %v: %v", params, verr)
	}
	b, _ := json.Marshal(map[string]any{"result": res})
	tr.add("stream-pane "+fmt.Sprint(params["from_seq"]), string(b))
	return res
}

// frame reads one pane frame.
func (s *linkStream) frame(t *testing.T, tr *spTranscript) (byte, []byte) {
	t.Helper()
	typ, payload, err := s.tryFrame()
	if err != nil {
		t.Fatalf("read a pane frame: %v", err)
	}
	tr.frame(typ, payload)
	return typ, payload
}

func (s *linkStream) tryFrame() (byte, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(s.br, head[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, binary.BigEndian.Uint32(head[1:5]))
	if _, err := io.ReadFull(s.br, payload); err != nil {
		return 0, nil, err
	}
	return head[0], payload, nil
}

// send writes one client frame.
func (s *linkStream) send(t *testing.T, typ byte, payload []byte) {
	t.Helper()
	b := make([]byte, 5, 5+len(payload))
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:5], uint32(len(payload)))
	s.write(t, append(b, payload...))
}

// spApply feeds one frame to the emulator and checks it continues the stream
// at seq. It returns the stream position after the frame.
func spApply(t *testing.T, emu *vt.Emulator, seq int64, typ byte, payload []byte) int64 {
	t.Helper()
	switch typ {
	case 'O':
		end := int64(binary.BigEndian.Uint64(payload[0:8]))
		if start := end - int64(len(payload)-8); start != seq {
			t.Fatalf("an O frame starts at %d, but the stream was at %d", start, seq)
		}
		_, _ = emu.Write(payload[8:])
		return end
	case 'R':
		at := int64(binary.BigEndian.Uint64(payload[0:8]))
		if at != seq {
			t.Fatalf("an R frame is at %d, but the stream was at %d", at, seq)
		}
		emu.Resize(int(binary.BigEndian.Uint16(payload[8:10])), int(binary.BigEndian.Uint16(payload[10:12])))
		return seq
	case 'E':
		t.Fatalf("the daemon refused a frame: %s", payload)
	case 'X':
		t.Fatalf("the pane stream ended: %s", payload)
	default:
		t.Fatalf("unexpected frame %q", typ)
	}
	return seq
}

// readUntil applies frames until the emulator shows marker.
func (s *linkStream) readUntil(t *testing.T, tr *spTranscript, emu *vt.Emulator, seq int64, marker string) int64 {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for !strings.Contains(spAll(emu), marker) {
		if time.Now().After(deadline) {
			t.Fatalf("%q never arrived on the stream:\n%s", marker, spScreen(emu))
		}
		typ, payload := s.frame(t, tr)
		seq = spApply(t, emu, seq, typ, payload)
	}
	return seq
}

// readResize applies frames until an R frame of cols by rows.
func (s *linkStream) readResize(t *testing.T, tr *spTranscript, emu *vt.Emulator, seq int64, cols, rows int) int64 {
	t.Helper()
	for {
		typ, payload := s.frame(t, tr)
		seq = spApply(t, emu, seq, typ, payload)
		if typ == 'R' && int(binary.BigEndian.Uint16(payload[8:10])) == cols && int(binary.BigEndian.Uint16(payload[10:12])) == rows {
			return seq
		}
	}
}

// drain applies frames until none arrives for quiet.
func (s *linkStream) drain(t *testing.T, tr *spTranscript, emu *vt.Emulator, seq int64, quiet time.Duration) int64 {
	t.Helper()
	s.timeout = quiet
	defer func() { s.timeout = 0 }()
	for {
		typ, payload, err := s.tryFrame()
		if errors.Is(err, errSPTimeout) {
			return seq
		}
		if err != nil {
			t.Fatalf("read a pane frame: %v", err)
		}
		tr.frame(typ, payload)
		seq = spApply(t, emu, seq, typ, payload)
	}
}

// --- Daemon reads through the control stream.

func spType(t *testing.T, ctl *linkStream, window, text string) {
	t.Helper()
	if _, verr := ctl.call(t, "send-text", map[string]any{"session": streamSession, "window": window, "text": text}); verr != nil {
		t.Fatalf("send-text: %v", verr)
	}
}

func spLines(t *testing.T, ctl *linkStream, window string) []string {
	t.Helper()
	res, verr := ctl.call(t, "capture-pane", map[string]any{"session": streamSession, "window": window})
	if verr != nil {
		t.Fatalf("capture-pane: %v", verr)
	}
	lines := strings.Split(res["content"].(string), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func spShows(t *testing.T, ctl *linkStream, window, marker string) bool {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(spLines(t, ctl, window), "\n"), marker) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func spWait(t *testing.T, ctl *linkStream, window, marker string) {
	t.Helper()
	if !spShows(t, ctl, window, marker) {
		t.Fatalf("%q never showed in the pane:\n%s", marker, strings.Join(spLines(t, ctl, window), "\n"))
	}
}

// spSize waits for a line that starts with prefix and holds a size, and
// returns the size.
func spSize(t *testing.T, ctl *linkStream, window, prefix string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		for _, line := range spLines(t, ctl, window) {
			if rest, ok := strings.CutPrefix(line, prefix); ok && strings.Contains(rest, "x") {
				return rest
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no %s line showed in %s:\n%s", prefix, window, strings.Join(spLines(t, ctl, window), "\n"))
	return ""
}

func spSessionSize(t *testing.T, ctl *linkStream) string {
	t.Helper()
	res, verr := ctl.call(t, "session-info", map[string]any{"session": streamSession})
	if verr != nil {
		t.Fatalf("session-info: %v", verr)
	}
	return fmt.Sprintf("%vx%v", res["width"], res["height"])
}

// spApproval waits for the pane's held approval and returns its id.
func spApproval(t *testing.T, ctl *linkStream, window string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		res, verr := ctl.call(t, "list-attention", map[string]any{"session": streamSession})
		if verr != nil {
			t.Fatalf("list-attention: %v", verr)
		}
		items, _ := res["items"].([]any)
		for _, it := range items {
			m := it.(map[string]any)
			w, _ := m["window"].(string)
			if m["kind"] == "approval" && w != "" && strings.HasPrefix(window, w) && m["request_id"] != nil && m["request_id"] != "" {
				return m["request_id"].(string)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no held approval for %s showed in the Inbox", window)
	return ""
}

// approvalHook is the real Claude Code hook, waiting for the Inbox.
type approvalHook struct {
	out  bytes.Buffer
	done chan error
}

func startApprovalHook(t *testing.T, base, window string) *approvalHook {
	t.Helper()
	cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", streamSession, "--window", window)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"PermissionRequest","session_id":"e2e-phone","tool_name":"Bash","tool_input":{"command":"npm test"}}`)
	h := &approvalHook{done: make(chan error, 1)}
	cmd.Stdout = &h.out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the hook: %v", err)
	}
	go func() { h.done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return h
}

func (h *approvalHook) wait(t *testing.T) string {
	t.Helper()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("the hook never returned")
	}
	return h.out.String()
}

// --- The emulator, read back.

func spEmuLine(emu *vt.Emulator, y int) string {
	var b strings.Builder
	for x := 0; x < emu.Width(); x++ {
		c := emu.CellAt(x, y)
		if c == nil || c.Content == "" {
			if c != nil && c.Width == 0 {
				continue
			}
			b.WriteByte(' ')
			continue
		}
		b.WriteString(c.Content)
	}
	return strings.TrimRight(b.String(), " ")
}

func spScreen(emu *vt.Emulator) string {
	lines := make([]string, emu.Height())
	for y := range lines {
		lines[y] = spEmuLine(emu, y)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func spHistory(emu *vt.Emulator) string {
	var b strings.Builder
	b.WriteByte('\n')
	for i := 0; i < emu.ScrollbackLen(); i++ {
		var line strings.Builder
		for _, c := range emu.ScrollbackLine(i) {
			line.WriteString(c.Content)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// spAll is the history and the screen, for a marker that may have scrolled.
func spAll(emu *vt.Emulator) string {
	return spHistory(emu) + spScreen(emu)
}

// --- The artifact.

// spTranscript records what the phone saw, for a person to read after a run.
type spTranscript struct {
	t     *testing.T
	b     strings.Builder
	runs  int
	last  byte
	bytes int
}

func newSPTranscript(t *testing.T) *spTranscript { return &spTranscript{t: t} }

func (tr *spTranscript) add(title, body string) {
	tr.flushRun()
	fmt.Fprintf(&tr.b, "=== %s\n%s\n\n", title, body)
}

// frame records one frame header. A run of output frames is one line.
func (tr *spTranscript) frame(typ byte, payload []byte) {
	if typ == 'O' && tr.last == 'O' {
		tr.runs++
		tr.bytes += len(payload) - 8
		return
	}
	tr.flushRun()
	switch typ {
	case 'O':
		tr.last, tr.runs, tr.bytes = 'O', 1, len(payload)-8
		fmt.Fprintf(&tr.b, "frame O seq=%d", binary.BigEndian.Uint64(payload[0:8]))
		return
	case 'S', 'R':
		fmt.Fprintf(&tr.b, "frame %c seq=%d cols=%d rows=%d len=%d\n", typ, binary.BigEndian.Uint64(payload[0:8]), binary.BigEndian.Uint16(payload[8:10]), binary.BigEndian.Uint16(payload[10:12]), len(payload))
	default:
		fmt.Fprintf(&tr.b, "frame %c %q\n", typ, payload)
	}
	tr.last = typ
}

func (tr *spTranscript) flushRun() {
	if tr.last == 'O' {
		fmt.Fprintf(&tr.b, " (+%d frames, %d bytes)\n", tr.runs-1, tr.bytes)
	}
	tr.last, tr.runs, tr.bytes = 0, 0, 0
}

func (tr *spTranscript) save() {
	tr.flushRun()
	path := filepath.Join(artifactDir(tr.t), "stream-pane-transcript.txt")
	if err := os.WriteFile(path, []byte(tr.b.String()), 0o644); err != nil {
		tr.t.Errorf("save the transcript: %v", err)
		return
	}
	tr.t.Logf("transcript: %s", path)
}
