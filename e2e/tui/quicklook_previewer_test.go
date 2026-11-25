package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// quickLookProjStart writes files into ~/proj and the config cfg, and lists
// ~/proj in the rail's files section. The folder is in the test's HOME, so a
// frame shows ~/proj and no scratch folder.
func quickLookProjStart(t *testing.T, base, cfg string, files map[string]string, logPath *string) *tuitest.Terminal {
	t.Helper()
	dir := filepath.Join(xdgDir(base, "HOME"), "proj")
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if cfg != "" {
		writeConfig(t, base, cfg)
	}
	term := startIn(t, base, startOpts{cols: 140, rows: 40, env: []string{"TUIOS_KITTY_GRAPHICS=0"}, logPath: logPath})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the listing")
	enterTerminalMode(t, term)
	runInShell(t, term, "cd ~/proj && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
	leaveTerminalMode(t, term)
	toggleSidebarViaPalette(t, term)
	return term
}

// writeScript writes an executable shell script and returns its path.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestQuickLookRunsAPluginPreviewer installs a plugin with a [[previewer]]
// from a folder. It waits for approval, and tuios plugins approve says what
// runs in quick look. Then quick look opens a.note on it: the program in the
// plugin's folder runs.
// The artifact is the frame quick-look-previewer-plugin.
func TestQuickLookRunsAPluginPreviewer(t *testing.T) {
	base := t.TempDir()
	// A folder previewer of this machine's config. With a daemon, quick look
	// asks the daemon for it and for the stats of the rows.
	writeConfig(t, base, "[[quick_look.previewer]]\nname = \"tree\"\nfolder = true\ncommand = [\"sh\", \"-c\", \"ls -1 \\\"$1\\\" | sed 's/^/TREE /'\", \"sh\", \"{path}\"]\noutput = \"text\"\n")
	plug := filepath.Join(t.TempDir(), "plug")
	writeScript(t, filepath.Join(plug, "bin"), "notes.sh", "printf 'PLUGIN %s\\n' \"$1\"\n")
	manifest := "api = 1\nid = \"e2e.notes\"\nname = \"Notes preview\"\nversion = \"1.0.0\"\n\n[[previewer]]\nid = \"notes\"\nmatch = [\"*.note\"]\ncommand = [\"./bin/notes.sh\", \"{name}\"]\n\n[[previewer]]\nid = \"none\"\nmatch = [\"*.note\"]\ncommand = [\"./bin/none.sh\"]\n"
	if err := os.WriteFile(filepath.Join(plug, "tuios-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "plugins", "install", plug, "--yes"); err != nil {
		t.Fatalf("install the plugin: %v\n%s", err, out)
	}
	// A previewer runs a program, so the plugin waits for approval, and the
	// approval says what runs. tuios asks a running daemon to confirm.
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "approve", "e2e.notes", "--yes"); err != nil || !strings.Contains(out, "in quick look") {
		t.Fatalf("ASSERTION: tuios plugins approve does not say that the previewer runs in quick look: %v\n%s", err, out)
	}
	term := quickLookProjStart(t, base, "", map[string]string{"a.note": "plain note body\n", "box/one.txt": "one\n"}, nil)
	waitForAll(t, term, uiTimeout, "the listing", "a.note")
	quickLookFocusRail(t, term, "a.note")
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show the plugin's previewer", []string{" e2e.notes/notes ", "PLUGIN a.note"}, nil)
	saveCockpitFrames(t, term, "quick-look-previewer-plugin")
	sendKeys(t, term, "v")
	railLists(t, term, "v did not show the plugin previewer whose program is missing", []string{"The program of e2e.notes/none is not on this machine."}, nil)

	// k: box/, where the daemon runs the folder previewer and reads the
	// stats of the rows.
	sendKeys(t, term, "k")
	railLists(t, term, "the daemon did not run the folder previewer", []string{" tree ", "TREE one.txt"}, nil)
	sendKeys(t, term, "v")
	railLists(t, term, "the folder rows show no size from the daemon", []string{"one.txt", "4 B"}, []string{"TREE one.txt"})

	// The approval holds the previewer's match too: a plugin that widens it
	// is not the plugin you approved.
	if err := os.WriteFile(filepath.Join(plug, "tuios-plugin.toml"), []byte(strings.Replace(manifest, `["*.note"]`, `["*"]`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if listed, _ := tuiosCLI(t, base, "plugins", "list"); !strings.Contains(listed, "different from the approved") {
		t.Fatalf("ASSERTION: a plugin that widened its previewer's match kept its approval:\n%s", listed)
	}
}

// quickLookWidthRe is the width the colour previewer printed.
var quickLookWidthRe = regexp.MustCompile(`RED a\.note w=(\d+)`)

// TestQuickLookRunsPreviewers sets four previewers in config.toml and opens
// quick look on the files they match. a.note opens on the colour previewer:
// its red text is red, and the title and APC escapes it prints do not reach
// the terminal. v shows the broken previewer's exit code and error output, and v
// again the file's own text. The colour previewer names {width}, so a resize
// of the terminal runs it again with the new width. A previewer that runs
// past its timeout says so. A previewer whose output is a picture is drawn as
// one. A folder previewer is the first view of a folder,
// and v goes on to the folder's own rows. The artifacts are the frames
// quick-look-previewer-ansi, quick-look-previewer-error,
// quick-look-previewer-image and quick-look-previewer-folder.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - on main, the test fails at "space did not show the colour previewer".
//   - with quickLookSGROnly keeping every escape, the test fails at "an
//     escape of the previewer other than a colour reached the terminal".
//   - with the size check cut from quickLookRunRead, the test fails at "a
//     resize did not run the colour previewer again".
func TestQuickLookRunsPreviewers(t *testing.T) {
	base := t.TempDir()
	var ptyLog string
	bin := filepath.Join(base, "bin")
	colour := writeScript(t, bin, "colour.sh", "printf '\\033[31mRED %s\\033[0m w=%s\\n' \"$1\" \"$2\"\nprintf '\\033]2;ZZTITLE\\007\\033_ZZAPC\\033\\\\after the title\\n'\n")
	broken := writeScript(t, bin, "broken.sh", "echo 'bad input on line 4' >&2\nexit 3\n")
	// lone leaves a child in a session of its own, which keeps the output
	// open for 30 seconds after lone ends. The child writes its pid.
	lonePID := filepath.Join(base, "lone.pid")
	lone := writeScript(t, bin, "lone.sh", "setsid sh -c 'echo $$ > \"$1\"; exec sleep 30' sh \"$1\" &\necho 'LONE done'\n")
	cfg := `[[quick_look.previewer]]
name = "colour"
match = ["*.note"]
command = ["` + colour + `", "{name}", "{width}"]

[[quick_look.previewer]]
name = "broken"
match = ["*.note"]
command = ["` + broken + `"]

[[quick_look.previewer]]
name = "slow"
match = ["*.slow"]
command = ["sleep", "20"]
timeout = "1s"

[[quick_look.previewer]]
name = "pic"
match = ["*.pict"]
command = ["cat", "{path}"]
output = "image"

[[quick_look.previewer]]
name = "lone"
match = ["*.lone"]
command = ["` + lone + `", "` + lonePID + `"]

[[quick_look.previewer]]
name = "big"
match = ["*.huge"]
command = ["cat", "{path}"]
output = "image"

[[quick_look.previewer]]
name = "tree"
folder = true
command = ["sh", "-c", "ls -1 \"$1\" | sed 's/^/TREE /'", "sh", "{path}"]
output = "text"
`
	term := quickLookProjStart(t, base, cfg, map[string]string{
		"a.note": "plain note body\n", "z.slow": "slow file\n", "box/one.txt": "one\n", "box/two.txt": "two\n",
		"b.pict": string(gradientPNG(t, 64, 32)),
		"c.lone": "lone\n", "d.huge": strings.Repeat("x", 9<<20),
	}, &ptyLog)
	t.Cleanup(func() {
		// The child of lone, by the pid it wrote, while it still sleeps.
		if b, err := os.ReadFile(lonePID); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				if cmd, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); strings.HasPrefix(string(cmd), "sleep\x0030") {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
		}
	})
	waitForAll(t, term, uiTimeout, "the listing", "a.note", "b.pict", "z.slow", "box/")

	quickLookFocusRail(t, term, "a.note")
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show the colour previewer", []string{" colour ", " broken ", "RED a.note w=", "after the title"}, nil)
	// The canvas drops a title escape, but it passes an APC through to the
	// terminal tuios runs in. Quick look keeps only the colours.
	if raw, err := os.ReadFile(ptyLog); err != nil || strings.Contains(term.Screen().Text(), "ZZTITLE") || strings.Contains(string(raw), "ZZAPC") {
		t.Fatalf("ASSERTION: an escape of the previewer other than a colour reached the terminal (%v):\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	rc, rr, ok := quickLookPanelFind(s, "RED a.note")
	wc, _, _ := quickLookPanelFind(s, "w=")
	if !ok || s.Cell(rc, rr).Fg == s.Cell(wc, rr).Fg {
		t.Fatalf("ASSERTION: the red text of the colour previewer is not drawn in its own colour:\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "quick-look-previewer-ansi")
	m := quickLookWidthRe.FindStringSubmatch(term.Screen().Text())
	if m == nil {
		t.Fatalf("no width in the colour previewer's output:\n%s", term.Snapshot())
	}
	width := m[1]

	sendKeys(t, term, "v")
	railLists(t, term, "v did not show the broken previewer", []string{"broken stopped with exit code 3.", "bad input on line 4"}, []string{"RED a.note"})
	saveCockpitFrames(t, term, "quick-look-previewer-error")
	sendKeys(t, term, "v")
	railLists(t, term, "v did not show the file's own text", []string{"plain note body"}, []string{"bad input on line 4"})

	// Open it again on the colour previewer, and make the terminal narrower.
	sendKeys(t, term, " ")
	sendKeys(t, term, " ")
	railLists(t, term, "space again did not show the colour previewer", []string{"RED a.note w=" + width}, nil)
	if err := term.Resize(110, 40); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := quickLookWidthRe.FindStringSubmatch(s.Text())
		return m != nil && m[1] != width
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a resize did not run the colour previewer again with a new width (was %s): %v\n%s", width, err, term.Snapshot())
	}

	// The rail keeps the keyboard: j moves its cursor from a.note to b.pict,
	// whose previewer prints a PNG. With no graphics, quick look draws it in
	// half blocks.
	sendKeys(t, term, " ")
	sendKeys(t, term, "j")
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show the picture previewer", []string{" pic "}, nil)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, rows := s.Size()
		for y := range rows {
			for x := quickLookPanelLeft; x < cols; x++ {
				if c := s.Cell(x, y).Content; c == "▀" || c == "▄" {
					return true
				}
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the picture previewer's PNG is not drawn: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "quick-look-previewer-image")

	// c.lone: lone ends at once, and its child holds the output. The run
	// still ends, a short time after lone.
	sendKeys(t, term, " ")
	sendKeys(t, term, "j")
	sendKeys(t, term, " ")
	railLists(t, term, "a previewer whose child holds the output does not finish", []string{"LONE done"}, nil)

	// d.huge: a picture past the size quick look can draw.
	sendKeys(t, term, " ")
	sendKeys(t, term, "j")
	sendKeys(t, term, " ")
	railLists(t, term, "a picture that is too large", []string{"The picture that big made is too large."}, nil)

	// j again: z.slow.
	sendKeys(t, term, " ")
	sendKeys(t, term, "j")
	sendKeys(t, term, " ")
	railLists(t, term, "a previewer past its timeout", []string{"slow did not finish in time."}, nil)

	sendKeys(t, term, " ")
	for range 5 {
		sendKeys(t, term, "k")
	}
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show the folder previewer", []string{" tree ", "TREE one.txt", "TREE two.txt"}, nil)
	saveCockpitFrames(t, term, "quick-look-previewer-folder")
	sendKeys(t, term, "v")
	railLists(t, term, "v did not show the folder's rows", []string{"2 items", "one.txt"}, []string{"TREE one.txt"})

	// The folder changes while the panel stays open. Back on box/, the
	// folder previewer runs again.
	if err := os.WriteFile(filepath.Join(xdgDir(base, "HOME"), "proj", "box", "three.txt"), []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sendKeys(t, term, "j")
	railLists(t, term, "j did not show a.note", []string{"RED a.note"}, nil)
	sendKeys(t, term, "k")
	railLists(t, term, "the folder previewer showed the folder as it was", []string{"TREE three.txt"}, nil)
}
