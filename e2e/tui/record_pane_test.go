package tuie2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"tuios.dev/vt"
)

// How TestRecordPaneWritesACastThatReplaysToTheScreen could pass wrongly,
// written down before the recorder:
//   - The header could name a size the first frame was not painted at. The
//     replay builds its emulator from the header alone.
//   - The first event could be the live stream only, so the cast opens on a
//     blank screen. The rows printed before the recording started are on the
//     screen at the end, and only the snapshot event can carry them.
//   - A resize could be dropped, so the rest wraps at the old width. The
//     replay resizes only on r events, and the screen it ends on is compared
//     cell row by cell row with the daemon's capture.
//   - The times could be made up. The shell sleeps a second between two
//     lines, and the events must show the gap.
//   - The marker could be written out of order, or not at all. The replay
//     stops at the marker that was set right after the capture.
//   - The recording could outlive its pane. The pane's shell exits, and
//     list-recordings must be empty without a stop.
//   - A UTF-8 character cut between two chunks could arrive as U+FFFD. The
//     pane prints a run of multibyte characters and the replay must hold them.
//
// The artifact is the .cast itself and a transcript beside it: the header,
// the event list, the replayed screen and the daemon's capture.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with the writeSnap call left out of verbRecordPane
//     (internal/session/pane_record.go), the cast starts with live output and
//     the test fails at "the replayed screen differs from the capture".
//   - with the resize branch of paneRecorder.take returning before it writes
//     the r event, the test fails at "no r event".

// recordSession is the session the recording test uses.
const recordSession = "e2e-record"

// castEvent is one event line of an asciicast v2 file.
type castEvent struct {
	t    float64
	code string
	data string
}

// TestRecordPaneWritesACastThatReplaysToTheScreen records a pane that prints
// known text, sleeps, is resized, gets a marker and ends, then replays the
// file through tuios.dev/vt and compares the screen with capture-pane.
func TestRecordPaneWritesACastThatReplaysToTheScreen(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	art := artifactDir(t)
	cast := filepath.Join(art, "pane.cast")
	_ = os.Remove(cast)
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	typeLine := func(line string) {
		t.Helper()
		cli("send-keys", "-s", recordSession, "--raw", line)
		cli("send-keys", "-s", recordSession, "Enter")
	}
	cli("new", recordSession, "--detach")

	// Before the recording: rows only the snapshot event can carry.
	// The recorded commands are functions defined here, so each takes one
	// short row and the rows printed before the start stay on the screen to
	// the end.
	typeLine(`a() { echo "A-$((20+1))"; sleep 1; echo "B-$((40+2))"; printf '\342\224\200%.0s' 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; echo; }; ` +
		`b() { echo "SZ-$(stty size | tr ' ' x)"; printf 'W%.0s' $(seq 1 70); echo; }; for i in 1 2 3; do echo "pre-$i"; done`)
	waitPaneCapture(t, base, recordSession, false, func(s string) bool { return strings.Contains(s, "\npre-3") })

	// Failure mode: a file already there is refused, and left alone.
	if err := os.WriteFile(cast, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "record-pane", "start", "-s", recordSession, "-o", cast); err == nil || !strings.Contains(out, "already exists") {
		t.Fatalf("record-pane over an existing file: want a refusal, got %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(cast); string(b) != "keep" {
		t.Fatalf("the refused start changed the file: %q", b)
	}
	cli("record-pane", "start", "-s", recordSession, "-o", cast, "--overwrite", "--title", "e2e")
	if fi, err := os.Stat(cast); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the cast file: want mode 0600, got %v %v", fi, err)
	}
	if out, err := tuiosCLI(t, base, "record-pane", "start", "-s", recordSession, "-o", cast+".2"); err == nil || !strings.Contains(out, "already being recorded") {
		t.Fatalf("a second recording of one pane: want a refusal, got %v\n%s", err, out)
	}

	// Output with a real pause in it, and multibyte characters.
	typeLine("a")
	waitPaneCapture(t, base, recordSession, false, func(s string) bool { return strings.Contains(s, "\nB-42") && strings.Contains(s, "────") })
	cli("record-pane", "mark", "-s", recordSession, "after sleep")

	// A resize, then a line longer than the new width.
	cli("call", "resize", `{"session":"`+recordSession+`","width":50,"height":12}`)
	typeLine("b")
	waitPaneCapture(t, base, recordSession, false, func(s string) bool { return strings.Contains(s, "\nSZ-12x50") })
	want := captureLines(t, base)
	cli("record-pane", "mark", "-s", recordSession, "captured")

	// The pane ends, and the recording with it.
	typeLine("exit")
	waitUntil(t, "the recording ends with its pane", shellTimeout, func() (bool, string) {
		out, err := tuiosCLI(t, base, "record-pane", "list", "--json")
		return err == nil && (strings.Contains(out, `"recordings":[]`) || strings.Contains(out, `"recordings": []`)), out
	})

	header, events := readCast(t, cast)
	tr := &strings.Builder{}
	defer func() {
		_ = os.WriteFile(filepath.Join(art, "transcript.txt"), []byte(tr.String()), 0o644)
	}()
	fmt.Fprintf(tr, "=== header\n%v\n\n=== events\n", header)
	for _, e := range events {
		fmt.Fprintf(tr, "%10.6f %s %q\n", e.t, e.code, e.data)
	}

	if header["version"] != float64(2) || header["title"] != "e2e" {
		t.Fatalf("the header: want version 2 and the title, got %v", header)
	}
	if _, ok := header["timestamp"].(float64); !ok {
		t.Fatalf("the header has no timestamp: %v", header)
	}
	cols, rows := int(header["width"].(float64)), int(header["height"].(float64))
	if len(events) == 0 || events[0].code != "o" || events[0].t != 0 {
		t.Fatalf("the first event must be the snapshot, output at 0: %v", events[:min(len(events), 1)])
	}
	last, tA, tB := 0.0, -1.0, -1.0
	sawResize := false
	for _, e := range events {
		if e.t < last {
			t.Fatalf("the times go back: %v after %v", e.t, last)
		}
		last = e.t
		if e.code == "o" && strings.Contains(e.data, "A-21") && tA < 0 {
			tA = e.t
		}
		if e.code == "o" && strings.Contains(e.data, "B-42") && tB < 0 {
			tB = e.t
		}
		if e.code == "r" && e.data == "50x12" {
			sawResize = true
		}
	}
	if tA < 0 || tB < 0 || tB-tA < 0.9 {
		t.Fatalf("the shell slept a second between A and B, the events say %v and %v", tA, tB)
	}
	if !sawResize {
		t.Fatalf("no r event for the resize to 50x12")
	}

	// Replay up to the marker set right after the capture.
	emu := vt.NewEmulator(cols, rows)
	marked := false
	for _, e := range events {
		if e.code == "m" && e.data == "captured" {
			marked = true
			break
		}
		switch e.code {
		case "o":
			_, _ = emu.Write([]byte(e.data))
		case "r":
			w, h, ok := strings.Cut(e.data, "x")
			cw, _ := strconv.Atoi(w)
			ch, _ := strconv.Atoi(h)
			if !ok || cw < 1 || ch < 1 {
				t.Fatalf("an r event that is not a size: %q", e.data)
			}
			emu.Resize(cw, ch)
		}
	}
	if !marked {
		t.Fatalf("no marker \"captured\" in the cast")
	}
	got := spScreen(emu)
	fmt.Fprintf(tr, "\n=== replayed screen (%dx%d)\n%s\n\n=== daemon capture\n%s\n", emu.Width(), emu.Height(), got, want)
	if got != want {
		t.Fatalf("the replayed screen differs from the capture\n--- replay\n%s\n--- capture\n%s", got, want)
	}
	if !strings.Contains(got, "pre-3") || !strings.Contains(got, strings.Repeat("─", 20)) {
		t.Fatalf("the replayed screen lacks the rows from before the start or the multibyte line:\n%s", got)
	}
	if !strings.Contains(spAll(emu), "\npre-1\n") {
		t.Fatalf("the snapshot carried no history:\n%s", spAll(emu))
	}
}

// captureLines is the daemon's capture of the recorded pane, as spScreen
// lays out an emulator: right-trimmed rows, no blank rows at the end.
func captureLines(t *testing.T, base string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "call", "capture-pane", `{"session":"`+recordSession+`"}`)
	if err != nil {
		t.Fatalf("capture-pane: %v\n%s", err, out)
	}
	var res struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("capture-pane: %v\n%s", err, out)
	}
	lines := strings.Split(res.Content, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// readCast parses an asciicast v2 file: every line must be JSON, the first an
// object, the rest [time, code, data] arrays.
func readCast(t *testing.T, path string) (map[string]any, []castEvent) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open the cast: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var header map[string]any
	var events []castEvent
	for n := 0; sc.Scan(); n++ {
		if n == 0 {
			if err := json.Unmarshal(sc.Bytes(), &header); err != nil {
				t.Fatalf("the header line is not a JSON object: %v\n%s", err, sc.Text())
			}
			continue
		}
		var raw []json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &raw); err != nil || len(raw) != 3 {
			t.Fatalf("line %d is not a [time, code, data] array: %v\n%s", n+1, err, sc.Text())
		}
		var e castEvent
		if json.Unmarshal(raw[0], &e.t) != nil || json.Unmarshal(raw[1], &e.code) != nil || json.Unmarshal(raw[2], &e.data) != nil {
			t.Fatalf("line %d has the wrong types: %s", n+1, sc.Text())
		}
		if strings.ContainsRune(e.data, '�') {
			t.Fatalf("line %d carries U+FFFD, a cut character: %s", n+1, sc.Text())
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read the cast: %v", err)
	}
	if header == nil {
		t.Fatalf("the cast is empty")
	}
	return header, events
}
