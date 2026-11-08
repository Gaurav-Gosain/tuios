package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The configured motions must behave like w and b, including counts and
// visual selection. Distinct start and destination columns catch no-ops; the
// two-word cases catch dropped counts. Rebinding and empty arrays must also
// remove the default keys, not merely add new ones.
func TestCopyModeWordMotionBindings(t *testing.T) {
	const line = "alpha beta gamma delta epsilon"
	const command = `clear; printf 'alpha beta gamma del%s epsilon\n' ta`
	right := tuitest.Key("\x1b[1;2C")
	left := tuitest.Key("\x1b[1;2D")

	for _, tc := range []struct {
		name, config      string
		forward, backward any
		unbound           bool
	}{
		{"defaults", "", right, left, false},
		{"rebound", "\n[keybindings.copy_mode]\ncopy_mode_word_forward = [\"ctrl+e\"]\ncopy_mode_word_backward = [\"ctrl+a\"]\n", tuitest.Ctrl('e'), tuitest.Ctrl('a'), true},
		{"disabled", "\n[keybindings.copy_mode]\ncopy_mode_word_forward = []\ncopy_mode_word_backward = []\n", nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, copyCursorConfig+tc.config)
			out := &lockedBuffer{}
			term := startIn(t, base, startOpts{out: out, env: copyColorOpts.env})
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, command, "delta", shellTimeout)
			row := lastRowWith(term.Screen(), line)
			if row < 0 {
				t.Fatalf("word-motion line missing\n%s", term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
				t.Fatalf("send prefix+[: %v", err)
			}
			waitCopyCursor(t, term, "prefix+[")
			at := func(needle string) int {
				t.Helper()
				col := screenCol(t, term, row, needle, 0)
				if err := term.SendKeys("?"+needle, tuitest.Enter); err != nil {
					t.Fatalf("search %s: %v", needle, err)
				}
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					r, c, ok := copyCursorCell(s)
					return ok && r == row && c == col
				}, uiTimeout); err != nil {
					t.Fatalf("search did not land on %s\n%s", needle, term.Snapshot())
				}
				return col
			}
			if tc.unbound {
				for _, key := range []any{right, left} {
					at("gamma")
					from := len(clipboardWrites(out))
					// The yank acknowledges the key; no sleep can mistake an
					// unprocessed key for a disabled binding.
					sendEach(t, term, "v", key, "y")
					if got := rawWrite(t, term, out, from, "unbound word motion"); got != "g" {
						t.Fatalf("unbound key %v extended selection: copied %q, want g", key, got)
					}
				}
			}
			if tc.forward == nil {
				copiesFrame(t, term, "word-motion-disabled")
				return
			}
			for _, motion := range []struct {
				name, start, one, two, vim string
				key                        any
			}{
				{"forward", "beta", "gamma", "delta", "w", tc.forward},
				{"backward", "delta", "gamma", "beta", "b", tc.backward},
			} {
				for _, count := range []int{1, 2} {
					start := at(motion.start)
					keys := []any{}
					if count == 2 {
						keys = append(keys, "2")
					}
					sendEach(t, term, append(keys, motion.vim)...)
					wantWord := motion.one
					if count == 2 {
						wantWord = motion.two
					}
					want := screenCol(t, term, row, wantWord, 0)
					if got := copyCursorMoved(t, term, row, start, motion.vim); got != want {
						t.Fatalf("%d%s reached column %d, want %d", count, motion.vim, got, want)
					}
					start = at(motion.start)
					sendEach(t, term, append(keys, motion.key)...)
					if got := copyCursorMoved(t, term, row, start, motion.name); got != want {
						t.Fatalf("%d %s reached column %d, want %d", count, motion.name, got, want)
					}
				}
				at(motion.start)
				from := len(clipboardWrites(out))
				sendEach(t, term, "v", motion.vim, "y")
				want := rawWrite(t, term, out, from, "visual "+motion.vim)
				at(motion.start)
				from = len(clipboardWrites(out))
				sendEach(t, term, "v", motion.key, "y")
				if got := rawWrite(t, term, out, from, "visual "+motion.name); got != want || want == "" {
					t.Fatalf("visual %s copied %q, want %q like %s", motion.name, got, want, motion.vim)
				}
			}
			copiesFrame(t, term, "word-motion-"+tc.name)
			alive(t, term, "after configurable word motions")
		})
	}
}
