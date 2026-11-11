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

// TestTaskAttachTakesAPaneAndReadsAPullRequest: the daemon takes a picture
// and the text of a pane and puts them on a card, a pane without the read
// grant may not have it do so, and a pull request link reads its state
// through gh.
//
// It checks, in order: --screenshot -w PANE attaches a PNG (the bytes start
// with the PNG mark) named after the pane; --capture -w PANE attaches the
// pane's text, which holds a line the pane printed; a pane that holds the
// card but only the task grant is refused a screenshot with a word on the
// read grant; get-task-attachment of a pr link answers with the state, the
// title and the checks a stub gh gives.
//
// How it could pass wrongly: a capture that read an empty screen would still
// attach a text, so the line the pane printed is looked for in it. A picture
// of the wrong pane would still be a PNG, so its name must be the pane's.
//
// Artifact: the picture and the text read back, under artifactDir.
//
// Negative controls: with takeFromPane not checking the read grant, the
// refusal check fails. With get-task-attachment not reading the pr link
// (no prLinks.read), the state check fails.
func TestTaskAttachTakesAPaneAndReadsAPullRequest(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	fakes := filepath.Join(t.TempDir(), "fakes")
	mustMkdir(fakes)
	const pr = "https://github.com/o/r/pull/7"
	gh := "#!/bin/sh\ncase \"$1 $2\" in\n\"pr view\") echo '{\"number\":7,\"url\":\"" + pr +
		"\",\"state\":\"OPEN\",\"title\":\"Fix the layout\",\"statusCheckRollup\":[{\"__typename\":\"CheckRun\",\"status\":\"COMPLETED\",\"conclusion\":\"SUCCESS\"}]}'; exit 0;;\nesac\necho \"fake gh: $*\" >&2; exit 2\n"
	if err := os.WriteFile(filepath.Join(fakes, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The open default gives every pane admin; the refusal needs a pane
	// with the task grant alone.
	writeConfig(t, base, "[agents.permissions]\nmode = \"strict\"\ngrants = [\"task\"]\n")
	const sess = "e2e-attach-pane"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	mustCLI(t, base, "task", "add", "-s", sess, "show the build")

	art := artifactDir(t)
	build := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "build", "--no-focus", "--print-id"))
	mustCLI(t, base, "send-text", "-s", sess, "-w", build, "echo build-line-$((40+2))\n")
	waitForCaptureOn(t, base, sess, build, "build-line-42")

	mustCLI(t, base, "task", "attach", "-s", sess, "t1", "--screenshot", "-w", build)
	mustCLI(t, base, "task", "attach", "-s", sess, "t1", "--capture", "-w", build)
	mustCLI(t, base, "task", "attach", "-s", sess, "t1", "--link", pr)

	var listed struct {
		Attachments []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"attachments"`
	}
	raw := mustCLI(t, base, "task", "attachments", "-s", sess, "t1", "--json")
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		t.Fatalf("task attachments --json: %v\n%s", err, raw)
	}
	names := map[string]string{}
	for _, a := range listed.Attachments {
		names[a.Kind] = a.Name
	}
	if names["image"] != "screenshot build.png" || names["text"] != "capture build.txt" {
		t.Fatalf("ASSERTION: the pane's picture and text are named %q and %q, want \"screenshot build.png\" and \"capture build.txt\":\n%s", names["image"], names["text"], raw)
	}
	pic := filepath.Join(art, "screenshot.png")
	text := filepath.Join(art, "capture.txt")
	_ = os.Remove(pic)
	_ = os.Remove(text)
	mustCLI(t, base, "task", "attachment", "get", "-s", sess, "t1", "screenshot build.png", "-o", pic)
	mustCLI(t, base, "task", "attachment", "get", "-s", sess, "t1", "capture build.txt", "-o", text)
	if data, _ := os.ReadFile(pic); !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Errorf("ASSERTION: the screenshot attached is not a PNG (%d bytes)", len(data))
	}
	if data, _ := os.ReadFile(text); !strings.Contains(string(data), "build-line-42") {
		t.Errorf("ASSERTION: the capture attached does not hold the line the pane printed:\n%s", data)
	}

	// A pane with the task grant alone holds the card, and may not have the
	// daemon take a pane for it.
	worker := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "worker", "--no-focus", "--print-id"))
	out := filepath.Join(base, "worker.out")
	script := tuiosBin + " task claim t1 >/dev/null 2>&1; " + tuiosBin + " task attach t1 --screenshot --name worker.png -w " + build +
		" > " + out + ".tmp 2>&1; echo EXIT=$? >> " + out + ".tmp; mv " + out + ".tmp " + out + "\n"
	mustCLI(t, base, "send-text", "-s", sess, "-w", worker, script)
	got := waitForFileText(t, out, func(s string) bool { return strings.Contains(s, "EXIT=") }, "the worker never ran its attach")
	if strings.Contains(got, "EXIT=0") || !strings.Contains(got, "read grant") {
		t.Errorf("ASSERTION: a pane without the read grant had the daemon take a pane:\n%s", got)
	}

	var res struct {
		PR *struct {
			Number int    `json:"number"`
			State  string `json:"state"`
			Title  string `json:"title"`
			Checks string `json:"checks"`
		} `json:"pr"`
		PRError string `json:"pr_error"`
	}
	raw = mustCLI(t, base, "task", "attachment", "get", "-s", sess, "t1", pr, "--json")
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("attachment get --json: %v\n%s", err, raw)
	}
	if res.PR == nil || res.PR.Number != 7 || res.PR.State != "open" || res.PR.Title != "Fix the layout" || res.PR.Checks != "pass" {
		t.Errorf("ASSERTION: the pull request link reads %+v (error %q), want #7 open, \"Fix the layout\", checks pass", res.PR, res.PRError)
	}
	_ = os.WriteFile(filepath.Join(art, "report.txt"), fmt.Appendf(nil, "attachments:\n%s\nworker:\n%s\npr:\n%s\n", mustCLI(t, base, "task", "get", "-s", sess, "t1"), got, raw), 0o644)
}
