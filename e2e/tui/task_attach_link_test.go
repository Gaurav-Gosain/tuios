package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAttachToACardOnAnotherMachine: a pane on home holds a project card
// that build owns, and attaches to it over the link: a file, and a picture
// of one of home's panes that home's daemon takes. build stores both, and
// reads them back byte for byte. A pane on home that does not hold the card is
// refused, and a file over the 3 MiB a link carries is refused before it
// crosses.
//
// It checks, in order: the claim from home; task attach -s build:far from
// the holder; task attach --screenshot -w PANE -s build:far, where PANE is a
// pane of home; build's get-task lists both with the holder's window as by;
// the bytes build gives back equal the file home sent, and the picture is a
// PNG; a second pane on home is refused; a 4 MiB file is refused with the
// link's limit named.
//
// How it could pass wrongly: an attach answered by home's own daemon would
// never reach build, so the attachments are read on build, through build's
// CLI. A screenshot resolved on build would name another pane or none, so
// the picture must come from home's pane, whose name the attachment takes.
//
// Artifact: the transcript and the files read back from build, under
// artifactDir.
//
// Negative control: with forwardAttach not sending remote_window, build
// reads the attach as the link's own, which may change any card, and the
// pane that does not hold the card attaches to it.
func TestAttachToACardOnAnotherMachine(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	remote := remoteMachine(t)
	art := artifactDir(t)
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { _ = os.WriteFile(filepath.Join(art, "transcript.txt"), []byte(report.String()), 0o644) }()

	startLinkedPair(t, base, remote, "far", "far2")
	mustCLI(t, remote, "task", "add", "-s", "far", "--project", "https://example.com/acme/api", "Fix the retry")

	worker := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "worker", "--no-focus", "--print-id"))
	other := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "other", "--no-focus", "--print-id"))
	build := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "buildlog", "--no-focus", "--print-id"))
	mustCLI(t, base, "send-text", "-s", "home", "-w", build, "echo the-build-passed\n")
	waitForCaptureOn(t, base, "home", build, "the-build-passed")

	out, code := cliInPane(t, base, "home", worker, "claim", "task", "claim", "t1", "-s", "build:far")
	note("claim: exit %d\n%s", code, out)
	if code != 0 {
		t.Fatalf("ASSERTION: the claim from home failed: exit %d\n%s", code, out)
	}

	files := filepath.Join(base, "files")
	mustMkdir(files)
	notes := filepath.Join(files, "notes.md")
	body := []byte("# Fixed\n\nThe retry waits with jitter now.\n")
	if err := os.WriteFile(notes, body, 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = cliInPane(t, base, "home", worker, "attach-file", "task", "attach", "t1", notes, "-s", "build:far")
	note("attach the notes: exit %d\n%s", code, out)
	if code != 0 || !strings.Contains(out, "on build") {
		t.Fatalf("ASSERTION: the attach from home failed, or did not reach build: exit %d\n%s", code, out)
	}
	out, code = cliInPane(t, base, "home", worker, "attach-shot", "task", "attach", "t1", "--screenshot", "-w", "buildlog", "-s", "build:far")
	note("attach a screenshot of home's pane: exit %d\n%s", code, out)
	if code != 0 {
		t.Fatalf("ASSERTION: the screenshot attach from home failed: exit %d\n%s", code, out)
	}

	// build holds both, by the holder.
	raw := mustCLI(t, remote, "task", "get", "-s", "far", "t1", "--json")
	note("build get-task:\n%s", raw)
	var card struct {
		Attachments []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			By   string `json:"by"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(raw), &card); err != nil {
		t.Fatalf("task get --json: %v\n%s", err, raw)
	}
	kinds := map[string]string{}
	for _, a := range card.Attachments {
		kinds[a.Name] = a.Kind
		if !strings.HasPrefix(a.By, "link:") {
			t.Errorf("ASSERTION: build records %s as by %q, want the link", a.Name, a.By)
		}
	}
	if kinds["notes.md"] != "markdown" || kinds["screenshot buildlog.png"] != "image" {
		t.Fatalf("ASSERTION: build holds %v, want notes.md (markdown) and \"screenshot buildlog.png\" (image)", kinds)
	}
	back := filepath.Join(art, "notes.md")
	pic := filepath.Join(art, "screenshot.png")
	_ = os.Remove(back)
	_ = os.Remove(pic)
	mustCLI(t, remote, "task", "attachment", "get", "-s", "far", "t1", "notes.md", "-o", back)
	mustCLI(t, remote, "task", "attachment", "get", "-s", "far", "t1", "screenshot buildlog.png", "-o", pic)
	if got, _ := os.ReadFile(back); !bytes.Equal(got, body) {
		t.Errorf("ASSERTION: build gives back %q, want the notes home sent", got)
	}
	if got, _ := os.ReadFile(pic); !bytes.HasPrefix(got, []byte("\x89PNG\r\n\x1a\n")) {
		t.Errorf("ASSERTION: the screenshot build holds is not a PNG (%d bytes)", len(got))
	}

	// A pane that does not hold the card is refused by build.
	out, code = cliInPane(t, base, "home", other, "steal", "task", "attach", "t1", notes, "--name", "other.md", "-s", "build:far")
	note("other pane attaches: exit %d\n%s", code, out)
	if code == 0 {
		t.Errorf("ASSERTION: a pane that does not hold the card attached to it on build: %s", out)
	}
	// A file over what a link carries is refused before it crosses.
	big := filepath.Join(files, "big.log")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), 4<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = cliInPane(t, base, "home", worker, "big", "task", "attach", "t1", big, "-s", "build:far")
	note("attach 4 MiB: exit %d\n%s", code, out)
	if code == 0 || !strings.Contains(out, "3 MiB over a link") {
		t.Errorf("ASSERTION: a 4 MiB file was not refused with the link's limit: %s", out)
	}
}
