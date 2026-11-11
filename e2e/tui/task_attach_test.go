package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTaskAttachmentsSurviveARestartAndGoWithTheCard: files and a link are
// put on a card through the CLI, read back byte for byte after
// restart-server, and the stored files go when the card goes.
//
// It checks, in order: a PNG, a Markdown file and a pull request link are
// attached and classified as image, markdown and pr; the same name again is
// refused without --replace; a file over [tasks] attachment_max_mib is
// refused with an error that names the option; a pane that does not hold the
// card is refused, and the same pane may attach once it claims the card;
// task get lists the attachments; after restart-server each file comes back
// with the same sha256; detach removes one file from the store, and task rm
// removes the rest.
//
// How it could pass wrongly: a get that answered from the client's own copy
// would hide a lost store, so the files are read back only after the daemon
// restarted, and compared by hash with the bytes written here. A store that
// never wrote would leave no file to remove, so the store folder is read
// before the detach and the rm, and must hold each file first.
//
// Artifact: the files read back after the restart and the CLI transcript,
// under artifactDir.
//
// Negative controls: with pushCopy keeping Attachments, nothing here fails,
// which is why task_state_push_test.go holds that rule. With the sweep
// skipped after remove-task (verbRemoveTask not calling sweepAttachments),
// the last check fails: the store still holds the files.
func TestTaskAttachmentsSurviveARestartAndGoWithTheCard(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-attach"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	mustCLI(t, base, "task", "add", "-s", sess, "-p", "fix the layout", "fix the layout")

	art := artifactDir(t)
	var log strings.Builder
	run := func(args ...string) (string, error) {
		out, err := tuiosCLI(t, base, args...)
		fmt.Fprintf(&log, "$ tuios %s\n%s", strings.Join(args, " "), out)
		if err != nil {
			fmt.Fprintf(&log, "(exit: %v)\n", err)
		}
		log.WriteString("\n")
		return out, err
	}
	defer func() {
		_ = os.WriteFile(filepath.Join(art, "transcript.txt"), []byte(log.String()), 0o644)
	}()

	files := filepath.Join(base, "files")
	mustMkdir(files)
	pic := filepath.Join(files, "after.png")
	writeTestPNG(t, pic)
	note := filepath.Join(files, "notes.md")
	if err := os.WriteFile(note, []byte("# Result\n\nThe columns line up now.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(files, "big.log")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	// A 1 MiB limit makes the refusal cheap to reach. The open default
	// gives every pane admin, so the pane's rule needs strict grants.
	writeConfig(t, base, "[tasks]\nattachment_max_mib = 1\n\n[agents.permissions]\nmode = \"strict\"\ngrants = [\"read\", \"task\"]\n")
	mustCLI(t, base, "restart-server", "--yes")
	waitForSessionInfo(t, base, sess)

	if out, err := run("task", "attach", "-s", sess, "t1", pic, "--note", "the layout after the fix"); err != nil {
		t.Fatalf("ASSERTION: attaching the picture failed: %v\n%s", err, out)
	}
	if out, err := run("task", "attach", "-s", sess, "t1", note); err != nil {
		t.Fatalf("ASSERTION: attaching the notes failed: %v\n%s", err, out)
	}
	const pr = "https://github.com/o/r/pull/12"
	if out, err := run("task", "attach", "-s", sess, "t1", "--link", pr); err != nil {
		t.Fatalf("ASSERTION: attaching the link failed: %v\n%s", err, out)
	}
	if out, err := run("task", "attach", "-s", sess, "t1", pic); err == nil || !strings.Contains(out, "replace") {
		t.Errorf("ASSERTION: the same name again was not refused with a word on replace: %v\n%s", err, out)
	}
	if out, err := run("task", "attach", "-s", sess, "t1", pic, "--replace", "--note", "the layout after the fix"); err != nil {
		t.Errorf("ASSERTION: --replace was refused: %v\n%s", err, out)
	}
	if out, err := run("task", "attach", "-s", sess, "t1", big); err == nil || !strings.Contains(out, "attachment_max_mib") {
		t.Errorf("ASSERTION: a file over the limit was not refused with the option's name: %v\n%s", err, out)
	}

	// A pane attaches only to the card it holds.
	pane := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "worker", "--no-focus", "--print-id"))
	paneOut := filepath.Join(base, "pane.out")
	script := tuiosBin + " task attach t1 " + note + " --name from-pane.md > " + paneOut + ".1 2>&1; echo EXIT=$? >> " + paneOut + ".1; " +
		tuiosBin + " task claim t1 >/dev/null 2>&1; " +
		tuiosBin + " task attach t1 " + note + " --name from-pane.md > " + paneOut + ".2 2>&1; echo EXIT=$? >> " + paneOut + ".2; " +
		"cat " + paneOut + ".1 " + paneOut + ".2 > " + paneOut + ".tmp; mv " + paneOut + ".tmp " + paneOut + "\n"
	mustCLI(t, base, "send-text", "-s", sess, "-w", pane, script)
	got := waitForFileText(t, paneOut, func(s string) bool { return strings.Count(s, "EXIT=") == 2 }, "the pane never ran its attaches")
	fmt.Fprintf(&log, "pane %s:\n%s\n", pane, got)
	first, second, _ := strings.Cut(got, "EXIT=")
	if strings.HasPrefix(second, "0") || !strings.Contains(first, "refused") {
		t.Errorf("ASSERTION: a pane that does not hold the card attached to it:\n%s", got)
	}
	if !strings.Contains(got, "EXIT=0") {
		t.Errorf("ASSERTION: the pane could not attach once it held the card:\n%s", got)
	}

	detail, _ := run("task", "get", "-s", sess, "t1")
	for _, want := range []string{"Attachments:", "after.png", "image", "notes.md", "markdown", pr, "the layout after the fix", "from-pane.md"} {
		if !strings.Contains(detail, want) {
			t.Errorf("ASSERTION: task get does not show %q:\n%s", want, detail)
		}
	}

	run("restart-server", "--yes")
	waitForSessionInfo(t, base, sess)

	var listed struct {
		Attachments []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			Hash string `json:"hash"`
		} `json:"attachments"`
	}
	raw, _ := run("task", "attachments", "-s", sess, "t1", "--json")
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		t.Fatalf("task attachments --json: %v\n%s", err, raw)
	}
	kinds := map[string]string{}
	for _, a := range listed.Attachments {
		kinds[a.Name] = a.Kind
	}
	want := map[string]string{"after.png": "image", "notes.md": "markdown", pr: "pr", "from-pane.md": "markdown"}
	for name, kind := range want {
		if kinds[name] != kind {
			t.Errorf("ASSERTION: after the restart %s is %q, want %q (all: %v)", name, kinds[name], kind, kinds)
		}
	}
	for name, src := range map[string]string{"after.png": pic, "notes.md": note} {
		dst := filepath.Join(art, name)
		_ = os.Remove(dst)
		if out, err := run("task", "attachment", "get", "-s", sess, "t1", name, "-o", dst); err != nil {
			t.Fatalf("ASSERTION: attachment get %s failed: %v\n%s", name, err, out)
		}
		if a, b := fileSHA(t, src), fileSHA(t, dst); a != b {
			t.Errorf("ASSERTION: %s came back with sha256 %s, want %s", name, b, a)
		}
	}
	link, _ := run("task", "attachment", "get", "-s", sess, "t1", pr, "-o", "-")
	if strings.TrimSpace(link) != pr {
		t.Errorf("ASSERTION: the link came back as %q, want %q", link, pr)
	}

	store := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "attachments")
	stored := func(src string) string {
		h := fileSHA(t, src)
		return filepath.Join(store, h[:2], h)
	}
	for _, src := range []string{pic, note} {
		if _, err := os.Stat(stored(src)); err != nil {
			t.Fatalf("ASSERTION: the store has no file for %s: %v", filepath.Base(src), err)
		}
	}
	run("task", "detach", "-s", sess, "t1", "after.png")
	waitForFileGone(t, stored(pic), "the picture's file stayed after its only card dropped it")
	if _, err := os.Stat(stored(note)); err != nil {
		t.Errorf("ASSERTION: detaching the picture removed the notes' file too: %v", err)
	}
	run("task", "rm", "-s", sess, "t1")
	waitForFileGone(t, stored(note), "the notes' file stayed after its card was removed")
}

// writeTestPNG writes a small real PNG to path.
func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := range 16 {
		for x := range 32 {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 16), B: 160, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitForFileGone waits until no file is at path.
func waitForFileGone(t *testing.T, path, why string) {
	t.Helper()
	waitUntil(t, why, uiTimeout, func() (bool, string) {
		_, err := os.Stat(path)
		return os.IsNotExist(err), path + " is still there"
	})
}
