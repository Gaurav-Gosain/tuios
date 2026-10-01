package tuie2e

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The small client's view of a larger session (internal/app/pane_view.go),
// for the things drawn or picked at a pane's position: capture mode, copy
// mode and its search prompt, and a program that hides its cursor. Then the
// daemon's side: the activity frame's size limit, the attach notice, and the
// clients that do not count toward the size.

// TestWindowSizeCaptureInView picks a pane with capture mode in the small
// client and checks the pane under the pointer is the one captured.
//
// NEGATIVE CONTROL: with renderCaptureMode recording the panes at their
// layout positions, the click on the right pane hits the left pane's
// rectangle, and the capture holds the left pane's L line.
func TestWindowSizeCaptureInView(t *testing.T) {
	// Both clients read the directory from the file, so whichever one the
	// capture runs in writes there.
	cfg := "[screenshot]\nformat = \"txt\"\ndirectory = \"{BASE}/shots\"\n"
	_, small, base := windowSizePair(t, "largest", cfg, nil)
	dir := shotDir(t, base)
	waitWSSize(t, base, wsBigCols, wsBigRows, "under largest")
	viewOverTheSplit(t, base, small)

	if err := small.SendKeys(tuitest.Ctrl('b'), "C"); err != nil {
		t.Fatalf("send leader+C: %v", err)
	}
	if err := small.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "cancel")
	}, uiTimeout); err != nil {
		t.Fatalf("capture mode never opened: %v\n%s", err, small.Snapshot())
	}
	row, col := findText(t, small, "RIGHTSIDE")
	leftClick(t, small, col+2, row)
	if err := small.WaitFor(func(tuitest.Screen) bool {
		return len(shotFiles(t, dir)) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("the capture wrote %v, want one file\n%s", shotFiles(t, dir), small.Snapshot())
	}
	body, err := os.ReadFile(shotFiles(t, dir)[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "RIGHTSIDE") || strings.Contains(string(body), "LLLLLLLLLL") {
		t.Fatalf("a click on the right pane captured another pane:\n%s", body)
	}
}

// TestWindowSizeCopyModeInView enters copy mode in the small client with the
// copy cursor at the right of the view. The view stays on the copy cursor,
// follows it home with 0, and the search prompt opens on the view's last
// row, inside the pane it searches.
//
// NEGATIVE CONTROLS: with paneCursor ignoring copy mode, entering copy mode
// snaps the view to the pane's left edge and RIGHTSIDE leaves the screen.
// With the search prompt placed at the pane's layout position, it is drawn
// on row 47 of a 24-row screen and never seen.
func TestWindowSizeCopyModeInView(t *testing.T) {
	_, small, base := windowSizePair(t, "largest", "", nil)
	waitWSSize(t, base, wsBigCols, wsBigRows, "under largest")
	viewOverTheSplit(t, base, small)

	if err := small.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send leader+[: %v", err)
	}
	// Copy mode starts on the shell's cursor, at the end of the L line, so
	// the view is where it was.
	time.Sleep(time.Second)
	if err := small.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "RIGHTSIDE") && strings.Contains(s.Text(), "←")
	}, uiTimeout); err != nil {
		t.Fatalf("entering copy mode moved the view off the copy cursor\n%s", small.Snapshot())
	}
	saveArtifact(t, small, artifactDir(t), "copy-mode-right")

	// 0 takes the copy cursor to the start of the line, and the view with it.
	if err := small.SendKeys("0"); err != nil {
		t.Fatal(err)
	}
	if err := small.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "RIGHTSIDE") && !strings.Contains(s.Text(), "←")
	}, uiTimeout); err != nil {
		t.Fatalf("the view did not follow the copy cursor home\n%s", small.Snapshot())
	}

	// The search prompt is drawn inside the view, on its last pane row.
	if err := small.SendKeys("/", "LLL"); err != nil {
		t.Fatal(err)
	}
	last := wsSmallRows - 3
	if err := small.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(last), "/LLL")
	}, uiTimeout); err != nil {
		t.Fatalf("the copy-mode search prompt is not on the view's last row\n%s", small.Snapshot())
	}
	saveArtifact(t, small, artifactDir(t), "copy-mode-search")
}

// TestWindowSizeHiddenCursorInView runs a program that hides its cursor and
// leaves it near the bottom of a pane taller than the small client, as an
// agent CLI does with its input box. The view shows that row.
//
// NEGATIVE CONTROL: with paneCursor giving up on a hidden cursor, the view
// shows the top of the pane and INPUTBOX is never on the small client.
func TestWindowSizeHiddenCursorInView(t *testing.T) {
	_, small, base := windowSizePair(t, "largest", "", nil)
	waitWSSize(t, base, wsBigCols, wsBigRows, "under largest")
	left, _ := sideBySide(t, base)
	if out, err := tuiosCLI(t, base, "focus-window", "-s", wsSession, left); err != nil {
		t.Fatalf("focus-window: %v\n%s", err, out)
	}
	// %s keeps the typed line from matching the marker.
	cmd := `printf '\033[2J\033[?25l\033[40;3HINPUT%sBOX' ''; sleep 600` + "\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", wsSession, "-w", left, cmd); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	if err := small.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "INPUTBOX")
	}, uiTimeout); err != nil {
		t.Fatalf("the view did not follow the hidden cursor to the input row\n%s", small.Snapshot())
	}
	saveArtifact(t, small, artifactDir(t), "hidden-cursor")
}

// TestWindowSizeActivityFrameLimit sends the daemon an activity frame with a
// payload far larger than the message has, and expects it refused unread.
//
// NEGATIVE CONTROL: with the MsgClientActivity case removed from
// daemonFrameLimit, the frame falls to the 16 MiB limit, the daemon reads it
// and answers nothing.
// The wire numbers of the two message types, from internal/session's iota
// block. This module does not import that package, whose dependencies it
// does not carry. Neither number can change: both are appended values of the
// iota block, which is the wire format (see ProtocolVersion).
const (
	wireMsgError          = 28
	wireMsgClientActivity = 64
)

func TestWindowSizeActivityFrameLimit(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", wsSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	sock := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	defer func() { _ = conn.Close() }()

	const payload = 64 * 1024
	frame := make([]byte, 6+payload)
	binary.BigEndian.PutUint32(frame, uint32(2+payload))
	frame[4] = wireMsgClientActivity
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write the frame: %v", err)
	}
	// The answer is one frame: length, type, codec, then the gob payload,
	// whose strings are plain bytes.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	head := make([]byte, 6)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatalf("the daemon answered nothing to a %d-byte activity frame: %v", payload, err)
	}
	body := make([]byte, binary.BigEndian.Uint32(head)-2)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatalf("read the answer: %v", err)
	}
	if head[4] != wireMsgError || !strings.Contains(string(body), "refused") {
		t.Fatalf("the daemon answered type %d %q, want a refusal", head[4], body)
	}
}

// TestWindowSizeAttachNoticeNamesThePolicyInForce attaches a third client
// while an old client holds the session at smallest under a latest config.
// The notice it prints names smallest.
//
// NEGATIVE CONTROL: with attachWindowSize reading the configured value
// (get-option) instead of session-info, the notice says latest.
func TestWindowSizeAttachNoticeNamesThePolicyInForce(t *testing.T) {
	_, _, base := windowSizePair(t, "latest", "", []string{wsLegacyEnv})
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "with an old client attached")
	var logPath string
	third := attachSmall(t, base, wsSession, startOpts{cols: 100, rows: 30, logPath: &logPath})
	_ = third
	deadline := time.Now().Add(uiTimeout)
	var raw []byte
	for time.Now().Before(deadline) {
		raw, _ = os.ReadFile(logPath)
		if strings.Contains(string(raw), "already has a client attached") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	text := string(raw)
	if !strings.Contains(text, "(window_size smallest)") || strings.Contains(text, "(window_size latest)") {
		i := strings.Index(text, "already has")
		t.Fatalf("the attach notice does not name smallest, the policy in force: %q", text[max(i-40, 0):min(i+260, len(text))])
	}
}

// TestWindowSizeViewersDoNotSize attaches a client marked as a viewer, which
// sends no input, beside one that types. Under largest the viewer does not
// count, so the session keeps the typing client's size. Under smallest it
// counts, as every client did before.
//
// NEGATIVE CONTROL: with sizingClients returning every client, the big
// viewer makes the session 200x50 under largest and the first subtest fails.
// With it leaving viewers out under smallest too, the small viewer stops
// counting and the second subtest sees 200x50.
func TestWindowSizeViewersDoNotSize(t *testing.T) {
	const viewer = "TUIOS_VIEW_ONLY=1"
	t.Run("largest", func(t *testing.T) {
		_, small, base := windowSizePair(t, "largest", "", nil, viewer)
		waitWSSize(t, base, wsSmallCols, wsSmallRows, "under largest with the big client a viewer")
		waitMark(t, small, false, "the typing client, sized by itself")
	})
	t.Run("smallest", func(t *testing.T) {
		_, _, base := windowSizePair(t, "smallest", "", []string{viewer})
		waitWSSize(t, base, wsSmallCols, wsSmallRows, "under smallest with the small client a viewer")
		if w, h, policy := wsSize(t, base); w != wsSmallCols || h != wsSmallRows {
			t.Fatalf("under %s a viewer stopped counting: the session is %dx%d", policy, w, h)
		}
	})
}

// TestWindowSizeMultiCopySaveInView opens the multi copy "Save to" prompt in
// the small client. The prompt belongs at the bottom of the focused pane,
// which is below the view, so it goes on the view's last pane row.
//
// NEGATIVE CONTROL: with multiCopySaveLayer placing the prompt at the pane's
// layout position, it is drawn below the 24-row screen and never seen.
func TestWindowSizeMultiCopySaveInView(t *testing.T) {
	_, small, base := windowSizePair(t, "largest", "", nil)
	waitWSSize(t, base, wsBigCols, wsBigRows, "under largest")
	left, right := sideBySide(t, base)
	for _, id := range []string{left, right} {
		if out, err := tuiosCLI(t, base, "focus-window", "-s", wsSession, id); err != nil {
			t.Fatalf("focus-window: %v\n%s", err, out)
		}
		if err := small.WaitFor(func(tuitest.Screen) bool {
			return focusedWindowID(t, base, wsSession) == id
		}, uiTimeout); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		togglePaletteMultifocus(t, small)
	}
	if err := small.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	if err := small.WaitForText("MULTI 2", uiTimeout); err != nil {
		t.Fatalf("no multi copy mode: %v\n%s", err, small.Snapshot())
	}
	if err := small.SendKeys("V"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := small.SendKeys("Y"); err != nil {
		t.Fatal(err)
	}
	last := wsSmallRows - 3
	if err := small.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(last), "Save to: ~/tuios-copy-")
	}, uiTimeout); err != nil {
		t.Fatalf("the Save to prompt is not on the view's last row\n%s", small.Snapshot())
	}
	saveArtifact(t, small, artifactDir(t), "multi-copy-save")
}
