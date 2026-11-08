package tuie2e

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The places in a Claude Code transcript where the person, not the agent,
// decides: an interrupt, a declined call (with and without words), and the
// image a Read of a PNG gives back. The fixture testdata/transcript/
// claude-stops.jsonl is two turns written the way Claude Code writes them: a
// thinking block with only a signature, a Read whose result is an image
// block, a Bash call the person declined with a reason, an AskUserQuestion
// declined with none, and the "[Request interrupted by user for tool use]"
// mark as the last record.
//
// TestTranscriptStopsOnTheWire reads it with agent-transcript, as the phone
// and guios do. TestCockpitThreadStops reads it in the agents view.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with InterruptText returning false, the wire test fails at "the
//     interrupt" and the view test at "You stopped the agent".
//   - with declined returning false, the wire test fails at "Claude Code's
//     own words reached the page" and the view at the "(declined)" summary.
//   - with the resultImages call cut from recordEntries, the wire test fails
//     at "the image of the Read" and the view at "PNG image  6×4".
//   - with the nested branch cut from ReadImage, the wire test fails at
//     "the image data".
//   - with the interrupt and decline cases cut from the Finished rule in
//     Turns, the view never shows the second turn's "Worked for 1m 42s".

// stopsFixture is the transcript both tests read.
var stopsFixture = filepath.Join("testdata", "transcript", "claude-stops.jsonl")

// stopsEntry is an entry with the fields these tests read.
type stopsEntry struct {
	ID     string `json:"id"`
	Role   string `json:"role"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Tool   string `json:"tool"`
	Status string `json:"status"`
	ToolID string `json:"tool_id"`
	Image  *struct {
		MediaType string `json:"media_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"image"`
}

// stopsImage is the PNG the fixture's Read result holds.
func stopsImage(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(stopsFixture)
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(raw), `"media_type": "image/png", "data": "`)
	if !ok {
		t.Fatal("the fixture has no image")
	}
	data, _, _ := strings.Cut(rest, `"`)
	png, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	return png
}

// TestTranscriptStopsOnTheWire holds the entries of the fixture to what the
// person did: the interrupt is its own kind and no prompt, each decline is
// declined and not an error, the declined Bash call's result is the person's
// words and not Claude Code's canned text, the empty thinking block is kept,
// and the Read's image is an entry with its size whose data the image read
// returns. The artifact is transcript-stops.txt: every request and reply.
func TestTranscriptStopsOnTheWire(t *testing.T) {
	r := newATRig(t, "transcript-stops.txt", 1)
	fixture, err := os.ReadFile(stopsFixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(atProjects(r.base), "work", "5e0b7a11.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	win := r.wins[0]
	r.join(win, path, "5e0b7a11")
	res := r.read(win, nil)
	b, _ := json.Marshal(res["entries"])
	var entries []stopsEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		t.Fatal(err)
	}
	byTool := map[string][]stopsEntry{}
	var kinds []string
	for _, e := range entries {
		kinds = append(kinds, e.Role+":"+e.Kind)
		if e.ToolID != "" {
			byTool[e.ToolID] = append(byTool[e.ToolID], e)
		}
	}
	want := "user:text assistant:thinking assistant:tool_call tool:tool_result tool:text assistant:text assistant:tool_call tool:tool_result assistant:text " +
		"user:text assistant:thinking assistant:tool_call tool:tool_result assistant:tool_call tool:tool_result user:interrupt"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("ASSERTION: the entries are\n  %s\nwant\n  %s", got, want)
	}
	last := entries[len(entries)-1]
	if last.Kind != "interrupt" || last.Text != "" {
		t.Fatalf("ASSERTION: the interrupt: want an interrupt entry with no text, got %+v", last)
	}
	for _, e := range entries {
		if strings.Contains(e.Text, "Request interrupted") || strings.Contains(e.Text, "doesn't want to proceed") {
			t.Fatalf("ASSERTION: Claude Code's own words reached the page: %+v", e)
		}
	}
	rm := byTool["toolu_rm"]
	if len(rm) != 2 || rm[0].Status != "declined" || rm[1].Status != "declined" || rm[1].Text != "Keep the build folder, it has the fonts." {
		t.Fatalf("ASSERTION: the declined Bash call: want the call and its result declined, the result the person's words, got %+v", rm)
	}
	ask := byTool["toolu_ask"]
	if len(ask) != 2 || ask[0].Status != "declined" || ask[1].Text != "" {
		t.Fatalf("ASSERTION: the declined question: want declined with no text, got %+v", ask)
	}
	read := byTool["toolu_read"]
	if len(read) != 3 || read[0].Status != "ok" || read[2].Image == nil || read[2].Image.MediaType != "image/png" ||
		read[2].Image.Width != 6 || read[2].Image.Height != 4 || read[2].ID != read[1].ID+".0" {
		t.Fatalf("ASSERTION: the image of the Read: want an image entry after the result, 6x4 PNG, id <result>.0, got %+v", read)
	}
	ip := map[string]any{"session": transcriptSession, "window": win, "human_nonce": r.nonce, "image": read[2].ID}
	img, verr := r.ctl.call(t, "agent-transcript", ip)
	r.art.call("agent-transcript", ip, img, verr)
	if verr != nil {
		t.Fatalf("ASSERTION: the image data: the read of %s failed: %v", read[2].ID, verr)
	}
	data, _ := base64.StdEncoding.DecodeString(img["data"].(string))
	if img["media_type"] != "image/png" || !bytes.Equal(data, stopsImage(t)) {
		t.Fatalf("ASSERTION: the image data: want the fixture's PNG, got %v and %d bytes", img["media_type"], len(data))
	}
	// An id that names a place inside a block that holds no image is no
	// image.
	p := map[string]any{"session": transcriptSession, "window": win, "human_nonce": r.nonce, "image": entries[0].ID + ".0"}
	if _, verr := r.ctl.call(t, "agent-transcript", p); verr == nil || verr.Code != "invalid_params" {
		t.Fatalf("ASSERTION: an image id inside a prompt: want invalid_params, got %v", verr)
	}
}

// TestCockpitThreadStops opens the fixture in the agents view. The interrupt
// is one quiet line and no "You" prompt or "turn 3"; the declined calls say
// declined, not failed, and the Bash one shows what the person said; the read
// shows its image as a card; each thinking block says how long it took; and both turns
// have a summary line with their time, the second one too, though it ends on
// the interrupt and not on an answer. The artifact is the frame
// cockpit-stops (text and PNG).
func TestCockpitThreadStops(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
	for _, s := range []string{"home", "api"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	fixture, err := os.ReadFile(stopsFixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(atProjects(base), "work", "5e0b7a11.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
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
	hook.Stdin = bytes.NewReader(hookPayload(path, "5e0b7a11"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "done", "-s", "api", "-w", "header-fix", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 200, rows: 55, shippedLooks: true, env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Try the header again.", "Check the screenshot and fix the header.")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the thread never showed both prompts: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatal(err)
	}
	saveCockpitFrames(t, term, "cockpit-stops")
	text := term.Screen().Text()
	for _, w := range []string{
		"You stopped the agent",
		"PNG image  6×4",
		"Thought for 12s",
		"Thought for 5s",
		"Keep the build folder, it has the fonts.",
		"Worked for 45s · ran 1 command and read 1 file (declined)",
		"Worked for 1m 42s · ran 1 command and used 1 tool (stopped)",
		"turn 2",
	} {
		if !strings.Contains(text, w) {
			t.Fatalf("ASSERTION: the thread does not show %q\n%s", w, term.Snapshot())
		}
	}
	if n := strings.Count(text, "declined"); n < 3 {
		t.Fatalf("ASSERTION: want both calls and the first summary marked declined, got %d\n%s", n, term.Snapshot())
	}
	for _, w := range []string{"failed", "Request interrupted", "doesn't want to proceed", "turn 3"} {
		if strings.Contains(text, w) {
			t.Fatalf("ASSERTION: the thread shows %q\n%s", w, term.Snapshot())
		}
	}
	alive(t, term, "after the thread")
}
