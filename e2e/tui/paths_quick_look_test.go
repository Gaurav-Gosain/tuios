package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The paths tests print and show these. The project folder sits in the test's
// HOME, so a screenshot shows no scratch folder.
const (
	pathsFile    = "internal/app/os.go"
	pathsMissing = "internal/app/missing.go"
	pathsURL     = "https://example.com/pane-url"
)

// pathsProject makes ~/proj with internal/app/os.go of 200 numbered lines,
// and returns the folder.
func pathsProject(t *testing.T, base string) string {
	t.Helper()
	proj := filepath.Join(xdgDir(base, "HOME"), "proj")
	if err := os.MkdirAll(filepath.Join(proj, "internal", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("package app\n")
	for i := 2; i <= 200; i++ {
		fmt.Fprintf(&b, "// line %d of os.go\n", i)
	}
	if err := os.WriteFile(filepath.Join(proj, pathsFile), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj
}

// pathsEditor writes an editor named vi that records its arguments, and
// returns the editor and its record. Named vi, it is given +LINE.
func pathsEditor(t *testing.T, base string) (string, string) {
	t.Helper()
	dir := filepath.Join(base, "editor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(base, "edited.txt")
	editor := filepath.Join(dir, "vi")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> '"+record+"'\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return editor, record
}

// waitQuickLookAt waits for quick look to show os.go from line 120.
func waitQuickLookAt(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "os.go") && strings.Contains(txt, "line 120 of os.go") && !strings.Contains(txt, "line 2 of os.go")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s: quick look did not show os.go from line 120: %v\n%s", what, err, term.Snapshot())
	}
}

// waitQuickLookClosed waits for the preview to be gone.
func waitQuickLookClosed(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "line 120 of os.go") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close quick look: %v\n%s", err, term.Snapshot())
	}
}

// TestPanePathsOpenInQuickLook prints a path that is there, one that is not
// and a URL in a pane, from the project folder. The pointer on the path that
// is there underlines it, and on the missing one does not. The URL still
// opens with the link opener on ctrl+click. ctrl+click on the path opens
// quick look at line 120, and e there opens the editor at +120. The artifacts are the frames
// pane-path-hover and pane-path-quick-look.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with the panePathAt fallback cut from LinkHoverAt, the test fails at
//     "the path never underlined".
//   - with pathThere taking every path as there, the test fails at "the
//     missing path is underlined".
//   - with the line dropped in OpenQuickLookPath, the test fails at
//     "quick look did not show os.go from line 120".
func TestPanePathsOpenInQuickLook(t *testing.T) {
	base := t.TempDir()
	proj := pathsProject(t, base)
	editor, edited := pathsEditor(t, base)
	record := filepath.Join(base, "opened.txt")
	opener := filepath.Join(base, "opener.sh")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+record+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, fmt.Sprintf("[appearance]\nlink_opener = %q\n", opener))
	term := startIn(t, base, startOpts{cols: 120, rows: 36, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "EDITOR=" + editor, "SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}})
	useShippedLooks(base)
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the paths")
	enterTerminalMode(t, term)
	runInShell(t, term, "cd ~/proj && clear && echo CD''DONE", "CDDONE", uiTimeout)
	// The command holds no :120 and no whole URL, so the only copies on
	// screen are the output's.
	runInShell(t, term, "printf 'build failed at %s:%s\\nnot found: %s:3\\ndocs: https://%s\\n' "+pathsFile+" 120 "+pathsMissing+" "+strings.TrimPrefix(pathsURL, "https://"), pathsURL, uiTimeout)
	leaveTerminalMode(t, term)
	_ = term.WaitStable(uiTimeout)

	// The output row, not the command line: the command holds no :120.
	col, row := mustFind(t, term, pathsFile+":120")
	mid := col + 5
	if cellUnderlined(term.Screen(), mid, row) {
		t.Fatalf("ASSERTION: the path is underlined with no pointer on it\n%s", term.Snapshot())
	}
	mouseHover(t, term, mid, row)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return cellUnderlined(s, mid, row) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the path never underlined: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "pane-path-hover")

	// The missing path: the disk is asked, says no, and nothing is drawn.
	mcol, mrow := mustFind(t, term, pathsMissing)
	mouseHover(t, term, mcol+5, mrow)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !cellUnderlined(s, mid, row) }, uiTimeout); err != nil {
		t.Fatalf("the underline did not leave the path: %v\n%s", err, term.Snapshot())
	}
	stayTrue(t, "the missing path is not underlined", time.Second, func() (bool, string) {
		return !cellUnderlined(term.Screen(), mcol+5, mrow), term.Snapshot()
	})

	// The URL in the same output still opens with the link opener.
	ucol, urow := mustFind(t, term, pathsURL)
	mouseClick(t, term, ucol+4, urow, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{pathsURL}, "ASSERTION: ctrl+click on the URL beside the paths")

	mouseClick(t, term, mid, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitQuickLookAt(t, term, "ctrl+click on the path")
	saveCockpitFrames(t, term, "pane-path-quick-look")

	// e opens the editor at the line on top.
	if err := term.SendKeys("e"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "ASSERTION: e in quick look starts the editor with +120 and the path", uiTimeout, func() (bool, string) {
		data, _ := os.ReadFile(edited)
		got := strings.Fields(string(data))
		return len(got) == 2 && got[0] == "+120" && got[1] == filepath.Join(proj, pathsFile), fmt.Sprintf("the editor was started with %q\n%s", data, term.Snapshot())
	})
	waitWindowCount(t, term, 2, "the editor pane")
	alive(t, term, "after the path clicks")
}

// TestCockpitPathsOpenInQuickLook clicks paths in the agents view's thread:
// a relative one taken from the agent's folder, and one under ~/. Each
// opens quick look at its line, over the view, and esc gives the view back.
// The artifact is the frame cockpit-path-quick-look.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with canvasPathAt not called from cockpitLineLinkAt, the test fails
//     at "quick look did not show os.go from line 120" on the ~/ path.
//   - with cockpitPathDir returning "", the relative path opens nothing and
//     the test fails at the same place on it.
func TestCockpitPathsOpenInQuickLook(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	proj := pathsProject(t, base)
	writeConfig(t, base, "[agents]\nview_list = \"strip\"\n")
	if out, err := tuiosCLI(t, base, "new", "api", "--detach", "--cwd", proj); err != nil {
		t.Fatalf("create session api: %v\n%s", err, out)
	}
	path := filepath.Join(atProjects(base), "work", "0c0ffee3.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pathsTranscript(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "api", "-w", "0", "--name", "path-fix"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", "path-fix")
	hook.Dir = proj
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "0c0ffee3"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "done", "-s", "api", "-w", "path-fix", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "api"}, cols: 120, rows: 40, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "the thread", "Where is the retry loop", "~/proj/"+pathsFile)
	_ = term.WaitStable(uiTimeout)

	for _, text := range []string{"~/proj/" + pathsFile + ":120", "in " + pathsFile + ":120"} {
		col, row := mustFind(t, term, text)
		if strings.HasPrefix(text, "in ") {
			col += 3
		}
		mouseHover(t, term, col+4, row)
		mouseClick(t, term, col+4, row, tuitest.MouseLeft, 0)
		waitQuickLookAt(t, term, "a click on "+text)
		if text[0] == '~' {
			saveCockpitFrames(t, term, "cockpit-path-quick-look")
		}
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatal(err)
		}
		waitQuickLookClosed(t, term)
		waitText(t, term, "the view after quick look", "Where is the retry loop")
	}
	alive(t, term, "after the path clicks")
}

// pathsTranscript is the transcript of TestCockpitPathsOpenInQuickLook: a
// prompt and an answer that names the file twice, relative and under ~/.
func pathsTranscript(t *testing.T) []byte {
	t.Helper()
	recs := []map[string]any{
		{"type": "user", "timestamp": "2026-10-20T10:00:00.000Z", "message": map[string]any{"role": "user",
			"content": "Where is the retry loop?"}},
		{"type": "assistant", "timestamp": "2026-10-20T10:00:30.000Z", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "It is in " + pathsFile + ":120 of this checkout.\n\nThe same file from home: ~/proj/" + pathsFile + ":120 here."},
		}}},
	}
	var buf bytes.Buffer
	for i, r := range recs {
		r["uuid"] = fmt.Sprintf("p%d", i)
		r["isSidechain"] = false
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
