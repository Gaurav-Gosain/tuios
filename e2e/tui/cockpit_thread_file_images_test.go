package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitThreadPastedImageFile shows a screenshot the person pasted
// through tuios in the thread. The prompt holds only the path of the file in
// the daemon's paste folder, so the daemon reads the file for the card.
//
// Four prompts name four files: a real PNG in the paste folder, a text file
// in the paste folder named as a PNG, a real PNG outside the paste folder,
// and a link in the paste folder to that PNG. Only the first two get a card.
// The first shows the picture's size and "click to view". The second says
// it could not be read: the daemon sends no bytes that are not an image.
// The other two get no card. The artifact is the frame cockpit-paste-file.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with withFileImages not called, the test fails at "no image card".
//   - with the paste folder check cut from fileImagePaths, it fails at
//     "outside the paste folder".
//   - with the sniff cut from openFileImage, the test still passes: the
//     client's decoder refuses the text bytes too. The unit test
//     TestFileImageBoundary holds the sniff.
func TestCockpitThreadPastedImageFile(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	// A 160x90 picture, so the card draws it over several rows.
	pic := image.NewRGBA(image.Rect(0, 0, 160, 90))
	for y := range 90 {
		for x := range 160 {
			pic.Set(x, y, color.RGBA{R: uint8(60 + x), G: uint8(40 + y*2), B: 200, A: 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, pic); err != nil {
		t.Fatal(err)
	}
	shot := pngBuf.Bytes()
	paste := filepath.Join(filepath.Dir(frameSocket(base)), "paste")
	if err := os.MkdirAll(paste, 0o700); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(paste, "tuios-paste-20261108-101500-aaaa.png")
	fake := filepath.Join(paste, "tuios-paste-20261108-101600-bbbb.png")
	// outside has the paste name, in another folder: the paste folder of
	// another daemon, say.
	outside := filepath.Join(filepath.Dir(paste), "other", "tuios-paste-20261108-101800-dddd.png")
	if err := os.MkdirAll(filepath.Dir(outside), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paste, "tuios-paste-20261108-101700-cccc.png")
	for p, b := range map[string][]byte{real: shot, fake: []byte("SECRET-KEY-not-an-image\n"), outside: shot} {
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(atProjects(base), "work", "fa57e1e5.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for i, p := range []string{real, fake, outside, link} {
		for k, r := range []map[string]any{
			{"type": "user", "timestamp": fmt.Sprintf("2026-11-08T10:0%d:00.000Z", i), "message": map[string]any{"role": "user", "content": fmt.Sprintf("Prompt %d: look at %s and fix the header.", i+1, p)}},
			{"type": "assistant", "timestamp": fmt.Sprintf("2026-11-08T10:0%d:30.000Z", i), "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("Answer %d.", i+1)}}}},
		} {
			r["uuid"] = fmt.Sprintf("p%d-%d", i, k)
			r["isSidechain"] = false
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "header-fix"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "header-fix")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "fa57e1e5"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "done", "-s", "api", "-w", "header-fix", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	host := &lockedBuffer{}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 80, shippedLooks: true, out: host,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the thread", "Answer 4.")
	// The cards are read after the thread draws: wait until no card reads.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "click to view") && !strings.Contains(s.Text(), "Reading the image")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "cockpit-paste-file-missing", hostPictures(t, []byte(host.String()))...)
		t.Fatalf("ASSERTION: no image card for the pasted file: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	// The picture is placed on the card's rows, under its head line.
	headRow, headCol := findText(t, term, "PNG image  160×90")
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return strings.Contains(host.String(), fmt.Sprintf("\x1b[%d;%dH\x1b_Ga=p,i=", headRow+2, headCol+1))
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the pasted picture was not placed under its card: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	saveCockpitFrames(t, term, "cockpit-paste-file", hostPictures(t, []byte(host.String()))...)
	// cardsBetween counts the image cards from the row of one prompt to the
	// next.
	rows := []int{rowWith(s, "Prompt 1:"), rowWith(s, "Prompt 2:"), rowWith(s, "Prompt 3:"), rowWith(s, "Prompt 4:"), rowWith(s, "Answer 4.")}
	for i, r := range rows {
		if r < 0 {
			t.Fatalf("row %d of the thread is not on screen\n%s", i, term.Snapshot())
		}
	}
	cards := func(i int) []string {
		var out []string
		for y := rows[i]; y < rows[i+1]; y++ {
			if line := s.Line(y); strings.Contains(line, "PNG image") {
				out = append(out, strings.TrimSpace(line))
			}
		}
		return out
	}
	if c := cards(0); len(c) != 1 || !strings.Contains(c[0], "160×90") || !strings.Contains(c[0], "click to view") {
		t.Fatalf("ASSERTION: the pasted PNG's card is %q, want one card with its size and click to view\n%s", c, term.Snapshot())
	}
	if c := cards(1); len(c) != 1 || strings.Contains(c[0], "click to view") || strings.Contains(s.Text(), "SECRET-KEY") {
		t.Fatalf("ASSERTION: the text file named as a PNG gives %q, want one card that says it could not be read\n%s", c, term.Snapshot())
	}
	if c := cards(2); len(c) != 0 {
		t.Fatalf("ASSERTION: a PNG outside the paste folder got a card: %q\n%s", c, term.Snapshot())
	}
	if c := cards(3); len(c) != 0 {
		t.Fatalf("ASSERTION: a link in the paste folder got a card: %q\n%s", c, term.Snapshot())
	}
	alive(t, term, "after the pasted image files")
}
