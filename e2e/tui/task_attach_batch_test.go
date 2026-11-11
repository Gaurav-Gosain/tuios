package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTaskAttachSeveralFilesIsOneChange: tuios task attach with several
// files puts them on the card in one change, which the card's history records
// as one entry, and sending the same files again changes nothing. --note with
// several files is refused, and a link is named short.
//
// How it could pass wrongly: one entry could hide that only one file went
// in, so every file is read back in the listing. A retry that replaced each
// file would add a history entry, so the history is counted after the retry.
//
// Artifact: the CLI transcript, under artifactDir.
//
// Negative control: with the CLI sending one call per file (no files
// batch), the history holds three entries and the count fails.
func TestTaskAttachSeveralFilesIsOneChange(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-attach-batch"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	mustCLI(t, base, "task", "add", "-s", sess, "batch the proof")
	files := filepath.Join(base, "files")
	mustMkdir(files)
	var paths []string
	for _, n := range []string{"a.log", "b.log", "c.md"} {
		p := filepath.Join(files, n)
		if err := os.WriteFile(p, []byte("content of "+n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	var log strings.Builder
	run := func(args ...string) (string, error) {
		out, err := tuiosCLI(t, base, args...)
		fmt.Fprintf(&log, "$ tuios %s\n%s\n", strings.Join(args, " "), out)
		return out, err
	}
	defer func() { _ = os.WriteFile(filepath.Join(artifactDir(t), "transcript.txt"), []byte(log.String()), 0o644) }()

	if out, err := run(append([]string{"task", "attach", "-s", sess, "t1", "--note", "one note"}, paths...)...); err == nil || !strings.Contains(out, "--note") {
		t.Errorf("ASSERTION: --note with several files was not refused: %v\n%s", err, out)
	}
	if out, err := run(append([]string{"task", "attach", "-s", sess, "t1"}, paths...)...); err != nil || strings.Count(out, "Attached ") != 3 {
		t.Fatalf("ASSERTION: the three files were not attached: %v\n%s", err, out)
	}
	if out, err := run(append([]string{"task", "attach", "-s", sess, "t1"}, paths...)...); err != nil || strings.Count(out, "already, with the same content") != 3 {
		t.Errorf("ASSERTION: sending the same files again was not a no-op: %v\n%s", err, out)
	}
	if out, err := run("task", "attach", "-s", sess, "t1", "--link", "https://example.com/docs/retry/"); err != nil || !strings.Contains(out, "example.com/docs/retry") {
		t.Errorf("ASSERTION: the link was not named by its host and path: %v\n%s", err, out)
	}
	raw, _ := run("task", "get", "-s", sess, "t1", "--json")
	var card struct {
		Attachments []struct {
			Name string `json:"name"`
		} `json:"attachments"`
		History []struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		} `json:"history"`
	}
	if err := json.Unmarshal([]byte(raw), &card); err != nil {
		t.Fatalf("task get --json: %v\n%s", err, raw)
	}
	if len(card.Attachments) != 4 {
		t.Errorf("ASSERTION: the card holds %d attachments, want the three files and the link: %+v", len(card.Attachments), card.Attachments)
	}
	attached := 0
	for _, h := range card.History {
		if h.Action == "attached" {
			attached++
		}
	}
	if attached != 2 {
		t.Errorf("ASSERTION: the history holds %d attached entries, want 2 (the three files, then the link): %+v", attached, card.History)
	}
}
