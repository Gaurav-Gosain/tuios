package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const startingRC = `PS1='$ \[\e]133;B\a\]'
PS0='\e]133;C\a'
PROMPT_COMMAND='printf "\e]133;D;%s\a" "$?"; if [ -z "$started" ]; then started=1; echo SHELL-STARTING; sleep 1; fi; printf "\e]133;A\a"'
`

// TestRunWaitsForTheFirstPromptAfterADMark: run in a pane whose shell has
// sent its first mark, a D, but not yet the A of its first prompt. run waits
// for that prompt and types the command, as it does for a shell that has
// sent no mark at all. A D with no command running does not put the shell
// at a prompt, and run used to refuse the pane there with not_at_prompt.
//
// NEGATIVE CONTROL: origin/main before this change. The run fails with
// not_at_prompt.
func TestRunWaitsForTheFirstPromptAfterADMark(t *testing.T) {
	const session = "firstprompt"
	base := t.TempDir()
	killDaemon(t, base)
	rc := filepath.Join(base, "startingrc")
	if err := os.WriteFile(rc, []byte(startingRC), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", session, "sh", "--", "bash", "--noprofile", "--rcfile", rc, "-i"); err != nil {
		t.Fatalf("open the pane: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "wait-for", "window-output", "-s", session, "-w", "sh", "--pattern", "SHELL-STARTING", "--timeout", "20000"); err != nil {
		t.Fatalf("the shell never started its first prompt: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "run", "-s", session, "-w", "sh", "--timeout", "20000", "--", "printf 'FIRST%s\\n' -RUN")
	if err != nil {
		t.Fatalf("ASSERTION: run in a shell between its first D and A marks failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "FIRST-RUN") {
		t.Fatalf("ASSERTION: run did not return the command's output:\n%s", out)
	}
}
