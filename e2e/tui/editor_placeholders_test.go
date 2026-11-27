package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// The editor setting may hold {file} and {line}, so an editor tuios does not
// know still opens at the line: quick look's e and review's o run the
// command with the path and the line in the placeholders' place, and add
// nothing of their own. An editor tuios does not know, with no placeholder,
// still gets the file alone.
//
// How it could pass wrongly, written down first:
//   - The stub could record an earlier run. Each case has its own daemon,
//     HOME and record file.
//   - The placeholders could be left in and the path added after them. The
//     record must hold exactly the expected arguments, in order.
//   - A shell could split the command, or tuios split it on every space. The
//     stub's folder holds a space, so either split would cut its path in two.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - with editorFileCommand ignoring the placeholders (always the old
//     rules), every placeholder case fails at "the editor got".
//   - with editorFileCommand adding the path after the placeholders,
//     "quick look e", "quick look e, one argument" and "review o" fail.
//   - with the no-placeholder branch adding "+LINE" for any editor, the two
//     no-placeholder cases fail.

// placeholderEditor writes an editor named myedit, which tuios does not
// know, that records its arguments one per line and a line "--" after them.
func placeholderEditor(t *testing.T, base string) (string, string) {
	t.Helper()
	dir := filepath.Join(base, "editor bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(base, "editor-args.txt")
	editor := filepath.Join(dir, "myedit")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> '" + record + "'\necho -- >> '" + record + "'\nexec sleep 60\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return editor, record
}

// waitEditorArgs waits for the editor's first run to record exactly want.
func waitEditorArgs(t *testing.T, term *tuitest.Terminal, record, what string, want []string) {
	t.Helper()
	waitUntil(t, what+": the editor got the arguments it should", uiTimeout, func() (bool, string) {
		data, _ := os.ReadFile(record)
		runs := strings.Split(string(data), "--\n")
		if len(runs) < 2 {
			return false, fmt.Sprintf("the editor has not run\n%s", term.Snapshot())
		}
		got := strings.Split(strings.TrimSuffix(runs[0], "\n"), "\n")
		ok := strings.Join(got, "\x00") == strings.Join(want, "\x00")
		return ok, fmt.Sprintf("%s: the editor got %q, want %q", what, got, want)
	})
}

// editorSetting is the editor command as one TOML string: the stub's path,
// quoted for the word split, then the arguments.
func editorSetting(editor, args string) string {
	cmd := "'" + editor + "'"
	if args != "" {
		cmd += " " + args
	}
	return fmt.Sprintf("[appearance.sidebar]\neditor = %q\n", cmd)
}

func TestEditorPlaceholdersOpenAtTheLine(t *testing.T) {
	quickLook := []struct {
		name, args string
		want       func(path string) []string
	}{
		{"quick look e", "--line {line} {file}", func(p string) []string { return []string{"--line", "120", p} }},
		{"quick look e, one argument", "-g {file}:{line}", func(p string) []string { return []string{"-g", p + ":120"} }},
		{"quick look e, line only", "+{line}", func(p string) []string { return []string{"+120", p} }},
		{"quick look e, no placeholder", "--wait", func(p string) []string { return []string{"--wait", p} }},
	}
	for _, tc := range quickLook {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			proj := pathsProject(t, base)
			editor, record := placeholderEditor(t, base)
			writeConfig(t, base, editorSetting(editor, tc.args))
			term := startIn(t, base, startOpts{cols: 120, rows: 36,
				env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "EDITOR=", "VISUAL=", "SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "opening a shell for the path")
			enterTerminalMode(t, term)
			runInShell(t, term, "cd ~/proj && clear && echo CD''DONE", "CDDONE", uiTimeout)
			runInShell(t, term, "printf 'build failed at %s:%s\\n' "+pathsFile+" 120", pathsFile+":120", uiTimeout)
			leaveTerminalMode(t, term)
			_ = term.WaitStable(uiTimeout)
			col, row := mustFind(t, term, pathsFile+":120")
			mouseClick(t, term, col+5, row, tuitest.MouseLeft, tuitest.ModCtrl)
			waitQuickLookAt(t, term, "ctrl+click on the path")
			if err := term.SendKeys("e"); err != nil {
				t.Fatal(err)
			}
			waitEditorArgs(t, term, record, tc.name, tc.want(filepath.Join(proj, pathsFile)))
			waitWindowCount(t, term, 2, "the editor pane")
		})
	}

	review := []struct {
		name, args string
		want       func(path string) []string
	}{
		{"review o", "--goto {line} {file}", func(p string) []string { return []string{"--goto", "10", p} }},
		{"review o, no placeholder", "", func(p string) []string { return []string{p} }},
	}
	for _, tc := range review {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			editor, record := placeholderEditor(t, base)
			writeConfig(t, base, editorSetting(editor, tc.args))
			repo := testutil.GitRepo(t)
			file := filepath.Join(repo, "a.go")
			write := func(changed bool) {
				t.Helper()
				var b strings.Builder
				for i := 1; i <= 30; i++ {
					if changed && i == 10 {
						b.WriteString("a.go CHANGED\n")
						continue
					}
					fmt.Fprintf(&b, "a.go line %d\n", i)
				}
				if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write(false)
			testutil.Git(t, repo, "add", "a.go")
			testutil.Git(t, repo, "commit", "-q", "-m", "a")
			if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
				t.Fatalf("new session in the repository: %v: %s", err, out)
			}
			cli := func(args ...string) {
				t.Helper()
				if out, err := tuiosCLI(t, base, args...); err != nil {
					t.Fatalf("%v: %v\n%s", args, err, out)
				}
			}
			daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
			cli("set-agent-state", "working", "-s", "agent", "-w", "0", "--harness", "claude-code")
			waitUntil(t, "the turn start is looked at", uiTimeout, func() (bool, string) {
				b, _ := os.ReadFile(daemonLog)
				return strings.Contains(string(b), "Turn start of window"), string(b)
			})
			write(true)
			cli("set-agent-state", "done", "-s", "agent", "-w", "0", "--harness", "claude-code", "-m", "done")
			waitUntil(t, "the checkpoint is taken", uiTimeout, func() (bool, string) {
				out, _ := tuiosCLI(t, base, "call", "list-checkpoints", `{"session":"agent","window":"0"}`)
				var got struct {
					Checkpoints []struct{} `json:"checkpoints"`
				}
				_ = json.Unmarshal([]byte(out), &got)
				return len(got.Checkpoints) >= 1, out
			})
			term := attachIn(t, base, "agent", startOpts{cols: 140, rows: 40, env: []string{"EDITOR=", "VISUAL="}})
			sendKeys(t, term, tuitest.Ctrl('b'), "v")
			waitScreen(t, term, "the review never showed the change", "CHANGED")
			// The cursor is on the file's one hunk; o opens it at its changed line.
			sendKeys(t, term, "o")
			waitEditorArgs(t, term, record, tc.name, tc.want(file))
		})
	}
}
