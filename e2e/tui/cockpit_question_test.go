package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const claudeFormAnswerScript = `#!/bin/sh
stty raw -echo
printf '\033[2J\033[H'
sed 's/$/\r/' "$1"
k=$(dd bs=1 count=1 2>/dev/null)
printf '%s' "$k" > "$2"
stty sane
printf '\033[2J\033[Hanswered %s\r\n' "$k"
sleep 600
`

// TestCockpitQuestionShowsItsOptionsAsKeys: Claude Code asks with
// AskUserQuestion. Its PermissionRequest hook fires and its pane draws the
// form. The agents view lists the question as a question, by its text, not
// as "Allow approve AskUserQuestion". The card shows the form's options as
// keys. 2 asks to choose Green, enter answers, and the pane gets the key 2.
//
// How this could pass wrongly, written down first:
//   - The row could say the question and the card still offer only dismiss.
//     The test waits for the option buttons on the card.
//   - 2 could answer at once, with no look at the prompt. The test checks
//     the pane got nothing before enter.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on main the row reads
// "Allow  approve AskUserQuestion" and the card has no options.
func TestCockpitQuestionShowsItsOptionsAsKeys(t *testing.T) {
	base, term := approvalsClient(t)
	if out, err := tuiosCLI(t, base, "new", "e2e-ask", "--detach"); err != nil {
		t.Fatalf("create e2e-ask: %v\n%s", err, out)
	}
	script, form, got := filepath.Join(base, "form.sh"), filepath.Join(base, "form.txt"), filepath.Join(base, "got")
	if err := os.WriteFile(script, []byte(claudeFormAnswerScript), 0o700); err != nil {
		t.Fatal(err)
	}
	screen := formScreen("Enter to select · ↑/↓ to navigate · Esc to cancel")
	if err := os.WriteFile(form, []byte(strings.Join(screen, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, base, "send-text", "-s", "e2e-ask", "sh "+script+" "+form+" "+got+"\n")
	waitForCapture(t, base, nil, []string{"-s", "e2e-ask"}, "Which color for the test?")
	runHookIn(t, base, "e2e-ask", "0", `{"hook_event_name":"PermissionRequest","session_id":"e2e-q","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which color for the test?","header":"Color","multiSelect":false,"options":[{"label":"Red","description":""},{"label":"Green","description":""},{"label":"Blue","description":""}]}]}}`)

	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Asks", "Which color for the test?", "Red", "Green", "Blue", "1-5 choose") && !strings.Contains(s.Text(), "approve AskUserQuestion")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the question is not a question with its options on the card: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-question-card")
	pause(t, "a held question takes an answer key only after it has been on screen for a moment", answerSettle)
	sendKeys(t, term, "2")
	waitText(t, term, "the ask before choosing", "Choose 2?")
	if b, _ := os.ReadFile(got); len(b) > 0 {
		t.Fatalf("ASSERTION: 2 answered before enter: %q", b)
	}
	saveCockpitFrames(t, term, "cockpit-question-choose")
	sendKeys(t, term, tuitest.Enter)
	waitUntil(t, "the pane gets the key 2", uiTimeout, func() (bool, string) {
		b, _ := os.ReadFile(got)
		return string(b) == "2", string(b)
	})
	alive(t, term, "after a question was answered from its card")
}

func runHookIn(t *testing.T, base, session, window, payload string) {
	t.Helper()
	cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", session, "--window", window)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Stdin = strings.NewReader(payload)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the hook failed: %v\n%s", err, out)
	}
}
