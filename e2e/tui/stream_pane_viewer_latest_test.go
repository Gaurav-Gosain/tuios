package tuie2e

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestStreamPaneViewerTakesLatest: under window_size latest, a desktop app
// that streams the panes with a yielding lease is a contender for the
// latest client. A TUI client attached to the session sizes the pane until
// the app has input, an I frame, or its window gains focus, an A frame.
// Then the app's lease sizes the pane, over the client. Input at the TUI
// client takes it back. Each side keeps it for the second-long hold after
// its own last activity, so TUI input right after the viewer's does not
// resize the pane at once. The transcript goes to stream-viewer-latest.txt.
//
// How this could pass wrongly, written down first:
//   - The pane could take the lease's size for another reason (the client
//     gone). The TUI client stays attached all through, and the pane is back
//     at its size after the client's input.
//   - The hand-back could come from the hold running out, not the TUI's
//     input. After the A frame the pane holds the lease's size for longer
//     than the hold before the TUI client types.
//   - TUI input could take latest back at once, past the viewer's hold. The
//     size is read within the hold after the viewer's input, and must still
//     be the lease's. (The other way round the test cannot order the two:
//     a key at the TUI client reaches the daemon through the client, later
//     than a frame the test writes to the daemon itself.)
func TestStreamPaneViewerTakesLatest(t *testing.T) {
	base, _, ctl, w1, _ := limitsDaemon(t)
	var log strings.Builder
	term := attachIn(t, base, streamSession, startOpts{})
	enableTiling(t, term)
	n := 0
	size := func() string {
		n++
		mark := fmt.Sprintf("Z%d-", n)
		spType(t, ctl, w1, "clear; echo "+mark+"$(stty size | tr ' ' x)\n")
		return spSize(t, ctl, w1, mark)
	}
	until := func(what string, ok func(string) bool) string {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for {
			got := size()
			if ok(got) {
				return got
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the pane is %s", what, got)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	tuiInput := func() {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('u')); err != nil {
			t.Fatalf("type in the TUI client: %v", err)
		}
	}
	tuiInput()
	tui := size()
	fmt.Fprintf(&log, "TUI client attached, typed: %s\n", tui)
	if tui == "6x30" {
		t.Fatalf("the TUI client's pane is already 6x30; pick another lease size")
	}

	conn, err := net.Dial("unix", hubSocket(base))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]any{"id": 1, "verb": "stream-pane", "params": map[string]any{
		"session": streamSession, "window": w1, "yield": true, "lease_cols": 30, "lease_rows": 6}})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	reply, err := br.ReadString('\n')
	if err != nil || !strings.Contains(reply, `"pane_stream"`) {
		t.Fatalf("stream-pane: %v %s", err, reply)
	}
	if !strings.Contains(reply, `"activity":true`) {
		t.Fatalf("the stream-pane reply does not offer A frames: %s", reply)
	}
	errs := make(chan string, 16)
	go func() {
		var head [5]byte
		for {
			if _, err := io.ReadFull(br, head[:]); err != nil {
				return
			}
			payload := make([]byte, binary.BigEndian.Uint32(head[1:]))
			if _, err := io.ReadFull(br, payload); err != nil {
				return
			}
			if head[0] == 'E' {
				errs <- string(payload)
			}
		}
	}()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for {
			select {
			case e := <-errs:
				t.Logf("an E frame the stream got: %s", e)
			default:
				return
			}
		}
	})
	send := func(typ byte, payload []byte) {
		t.Helper()
		b := binary.BigEndian.AppendUint32([]byte{typ}, uint32(len(payload)))
		if _, err := conn.Write(append(b, payload...)); err != nil {
			t.Fatalf("send %c: %v", typ, err)
		}
	}

	// The lease alone yields to the client.
	time.Sleep(1500 * time.Millisecond)
	if got := size(); got != tui {
		t.Fatalf("a yielding lease with no input sized the pane: %s, want the client's %s", got, tui)
	}
	log.WriteString("yield lease, no viewer input: the client's size\n")

	// The viewer types: its lease sizes the pane.
	send('I', []byte("#\n"))
	got := until("after the viewer's I frame", func(s string) bool { return s == "6x30" })
	fmt.Fprintf(&log, "viewer I frame: %s\n", got)

	// The TUI client types: the pane goes back to its size.
	tuiInput()
	got = until("after the TUI client's input", func(s string) bool { return s == tui })
	fmt.Fprintf(&log, "TUI input: %s\n", got)

	// The viewer's window gains focus, past the TUI client's hold.
	time.Sleep(1500 * time.Millisecond)
	send('A', nil)
	got = until("after the viewer's A frame", func(s string) bool { return s == "6x30" })
	fmt.Fprintf(&log, "viewer A frame: %s\n", got)

	// The viewer keeps it with no one typing, longer than the hold.
	time.Sleep(2 * time.Second)
	if got := size(); got != "6x30" {
		t.Fatalf("the viewer lost latest with no input anywhere: %s", got)
	}
	log.WriteString("kept 2s with no input: 6x30\n")

	// The viewer types and the TUI client types right after it, within the
	// viewer's hold: no change yet, then the client's size once it ends.
	send('I', []byte("#\n"))
	tuiInput()
	if got := size(); got != "6x30" {
		t.Fatalf("TUI input within the viewer's hold resized the pane at once: %s", got)
	}
	got = until("after the TUI client's input in the hold", func(s string) bool { return s == tui })
	fmt.Fprintf(&log, "TUI input within the viewer's hold: 6x30 first, then %s\n", got)

	select {
	case e := <-errs:
		t.Fatalf("the stream got an E frame: %s", e)
	default:
	}
	alive(t, term, "after the viewer took latest and gave it back")
	saveFrame(t, term, "stream-viewer-latest")
	writeReviewArtifact(t, "stream-viewer-latest.txt", log.String())
}
