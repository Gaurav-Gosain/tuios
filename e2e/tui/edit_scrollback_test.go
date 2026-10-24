package tuie2e

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// How TestCopyModeEditScrollback could pass wrongly, written down first:
//   - The editor could get the client's copy of the pane, which after an
//     attach holds only the newest 1000 history rows. The daemon case prints
//     3000 rows before any client attaches and requires the first of them.
//   - The search could land on the typed command rather than its output. The
//     command spells the marker as "row-$i", so only the output holds
//     "row-2990".
//   - A +LINE that is right by accident: the line is read from the file the
//     editor got, and row-2990 is far from both ends of it.
//   - The file could be removed only when the client ends. The client is
//     still running when the test looks for the file.
//   - The fake editor could fail before it records anything. Each record is
//     required, and the editor prints a marker the test waits for.

// fakeVim is an editor named vim, so tuios passes it +LINE. It records its
// arguments, a copy of the file, and the modes of the file and its folder,
// then holds the terminal for a moment.
const fakeVim = `#!/bin/sh
dir=%DIR%
: > "$dir/args"
for a in "$@"; do printf '%s\n' "$a" >> "$dir/args"; f=$a; done
cp "$f" "$dir/content"
printf '%s' "$f" > "$dir/path"
mode() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }
mode "$f" > "$dir/mode"
mode "$(dirname "$f")" > "$dir/dirmode"
echo EDITOR""RAN
sleep 1
`

// TestCopyModeEditScrollback presses ctrl+g in copy mode with the cursor on
// one row of a long scrollback. The editor must get the whole scrollback as
// plain text in a private file, open at the cursor's line, and the file must
// be gone after the editor exits.
func TestCopyModeEditScrollback(t *testing.T) {
	const rows = 3000
	loop := `for i in $(seq 1 ` + strconv.Itoa(rows) + `); do echo "row-$i"; done`
	for _, daemon := range []bool{false, true} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			rec := t.TempDir()
			editor := filepath.Join(t.TempDir(), "vim")
			script := strings.ReplaceAll(fakeVim, "%DIR%", rec)
			if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			writeConfig(t, base, copyCursorConfig)
			opts := startOpts{env: append(slices.Clone(copyColorOpts.env), "EDITOR="+editor, "VISUAL=")}

			var term *tuitest.Terminal
			if daemon {
				killDaemon(t, base)
				if o, err := tuiosCLI(t, base, "new", "e2e-edit", "--detach"); err != nil {
					t.Fatalf("create session: %v\n%s", err, o)
				}
				if o, err := tuiosCLI(t, base, "send-keys", "-s", "e2e-edit", "--raw", loop); err != nil {
					t.Fatalf("send the loop: %v\n%s", err, o)
				}
				if o, err := tuiosCLI(t, base, "send-keys", "-s", "e2e-edit", "Enter"); err != nil {
					t.Fatalf("send Enter: %v\n%s", err, o)
				}
				deadline := time.Now().Add(shellTimeout)
				for {
					out, _ := tuiosOut(base, "capture-pane", "-s", "e2e-edit")
					if strings.Contains(out, "\nrow-3000") {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("the loop never finished in the detached session\n%s", out)
					}
					time.Sleep(100 * time.Millisecond)
				}
				opts.args = []string{"attach", "e2e-edit"}
				term = startIn(t, base, opts)
				if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
					t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
				}
				windowManagementMode(t, term)
				enterTerminalMode(t, term)
			} else {
				term = startIn(t, base, opts)
				waitBoot(t, term)
				newWindow(t, term)
				enterTerminalMode(t, term)
				runInShell(t, term, loop, "row-3000", shellTimeout)
			}
			time.Sleep(300 * time.Millisecond)

			copySearch(t, term, "row-2990", lastRowWith(term.Screen(), "row-2990"))
			saveFrame(t, term, "edit-scrollback-"+name+"-copy-cursor")
			if err := term.SendKeys(tuitest.Ctrl('g')); err != nil {
				t.Fatalf("send ctrl+g: %v", err)
			}
			if err := term.WaitForText("EDITORRAN", uiTimeout); err != nil {
				t.Fatalf("ctrl+g in copy mode did not run the editor: %v\n%s", err, term.Snapshot())
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "EDITORRAN")
			}, uiTimeout); err != nil {
				t.Fatalf("tuios did not come back after the editor: %v\n%s", err, term.Snapshot())
			}

			read := func(name string) string {
				t.Helper()
				b, err := os.ReadFile(filepath.Join(rec, name))
				if err != nil {
					t.Fatalf("the editor did not record %s: %v", name, err)
				}
				return string(b)
			}
			args := strings.Split(strings.TrimSpace(read("args")), "\n")
			path := read("path")
			content := read("content")
			if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
				_ = os.WriteFile(filepath.Join(dir, "edit-scrollback-"+name+"-args.txt"), []byte(read("args")), 0o644)
				_ = os.WriteFile(filepath.Join(dir, "edit-scrollback-"+name+"-file.txt"), []byte(content), 0o644)
			}

			lines := strings.Split(content, "\n")
			at := -1
			for i, l := range lines {
				l = strings.TrimRight(l, " ")
				if l == "row-2990" {
					at = i
				}
				if strings.Contains(l, "\x1b") {
					t.Fatalf("line %d of the file has an escape code: %q", i+1, l)
				}
			}
			for _, want := range []string{"row-1", "row-1000", "row-3000"} {
				if !slices.ContainsFunc(lines, func(l string) bool { return strings.TrimRight(l, " ") == want }) {
					t.Fatalf("the file has no line %q: the editor did not get the whole scrollback (%d lines)", want, len(lines))
				}
			}
			if at < 0 {
				t.Fatalf("the file has no line row-2990")
			}
			want := []string{"+" + strconv.Itoa(at+1), path}
			if len(args) < 2 || !slices.Equal(args[len(args)-2:], want) {
				t.Fatalf("the editor got %q, want it to end with %q (row-2990 is line %d of the file)", args, want, at+1)
			}
			if m := strings.TrimSpace(read("mode")); m != "600" {
				t.Fatalf("the file mode is %s, want 600", m)
			}
			if m := strings.TrimSpace(read("dirmode")); m != "700" {
				t.Fatalf("the folder mode is %s, want 700", m)
			}

			deadline := time.Now().Add(uiTimeout)
			for {
				_, fileErr := os.Stat(path)
				_, dirErr := os.Stat(filepath.Dir(path))
				if os.IsNotExist(fileErr) && os.IsNotExist(dirErr) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the scrollback file %s or its folder is still there after the editor exited", path)
				}
				time.Sleep(100 * time.Millisecond)
			}
			saveFrame(t, term, "edit-scrollback-"+name+"-after")
		})
	}
}
