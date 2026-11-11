package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// childAsksQuestion is a Claude Code stand-in that asks its parent a question
// on Claude Code's form over the input box, after the detector has named it,
// and prints the one key it is answered with.
const childAsksQuestion = `sleep 3
printf '%s\n' \
 '─────────────────────────────────────────' \
 '  Which file should I edit first?' \
 '  ❯ 1. main.go' \
 '    2. util.go' \
 '  Enter to confirm · Esc to cancel' \
 '─────────────────────────────────────────' \
 '❯' \
 '─────────────────────────────────────────' \
 '  ⏵⏵ auto mode on (shift+tab to cycle)'
stty raw -echo
c=$(dd bs=1 count=1 2>/dev/null)
stty sane
echo "GOT-$c"
exec sleep 600
`

// childAsksApproval is a Claude Code stand-in that stops on a tool approval.
const childAsksApproval = `sleep 3
printf '%s\n' \
 ' Bash command' \
 '   rm -rf build' \
 ' Do you want to proceed?' \
 ' ❯ 1. Yes' \
 '   2. No'
exec sleep 600
`

// TestAWorkersQuestionReachesItsLead is item 1 of the lead's list
// (design/lead-ergonomics.md): a worker a lead started with start-agent asked a
// question in its own pane, nobody watched that pane, and the lead could not
// answer it. Now the question is mailed to the lead, and the lead answers it
// with respond, with no respond grant. An approval stays the person's: no mail,
// and the lead's respond is refused.
//
// How it could pass wrongly, written down first:
//   - the mail could come from something other than the question (a notice of
//     the start): the test wants the question's own words in it;
//   - respond could "succeed" without the key reaching the child: the child
//     prints the key it read, and the test wants GOT-2 on its screen;
//   - the lead could answer because it holds respond some other way: the lead
//     pane is an ordinary pane with the default grants, and the same respond
//     on the approval child must be refused;
//   - the approval could be held back by timing rather than by rule: the
//     approval child is started first and the test reads the lead's mail after
//     the question's arrived, so an approval mail would be there by then.
//
// NEGATIVE CONTROL: on main the lead gets no mail (the route is only for
// delegated children) and its respond is refused with not_human.
func TestAWorkersQuestionReachesItsLead(t *testing.T) {
	fakeClaudeBin := filepath.Join(t.TempDir(), "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fakeClaudeBin, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	asker, approver := filepath.Join(base, "asker.sh"), filepath.Join(base, "approver.sh")
	for path, body := range map[string]string{asker: childAsksQuestion, approver: childAsksApproval} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil { //nolint:gosec // a pane runs it
			t.Fatal(err)
		}
	}
	writeConfig(t, base, "[daemon]\nagent_detect_seconds = 1\n")
	killDaemon(t, base)
	const sess = "crew"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create %s: %v\n%s", sess, err, out)
	}
	lead := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "lead", "--no-focus", "--print-id"))
	typeInLead := func(line string) {
		t.Helper()
		mustCLI(t, base, "send-text", "-s", sess, "-w", lead, line+"\n")
	}
	// The lead is an agent pane, as a lead is: start-agent links a child only
	// to a pane that runs an agent.
	typeInLead("tuios set-agent-state idle --harness claude-code -m lead")
	waitUntil(t, "the lead pane reads as an agent", 20*time.Second, func() (bool, string) {
		out, _ := tuiosCLI(t, base, "get-agent-state", "-s", sess, "-w", lead)
		return strings.Contains(out, "idle"), out
	})
	for _, c := range []struct{ name, script string }{{"approver", approver}, {"asker", asker}} {
		typeInLead("tuios start-agent 'claude " + c.script + "' -s " + sess + " --name " + c.name + " --cwd " + base + " --ready-timeout 20000 > " + filepath.Join(base, c.name+".start") + " 2>&1")
	}

	var mail string
	waitUntil(t, "the lead got mail with the worker's question", 60*time.Second, func() (bool, string) {
		mail, _ = tuiosCLI(t, base, "read-agent-messages", "-s", sess, "-w", lead)
		return strings.Contains(mail, "Which file should I edit first?"), mail
	})
	if strings.Contains(mail, "rm -rf build") || strings.Contains(mail, "approver") {
		t.Errorf("ASSERTION: the lead was mailed the approval, which is the person's:\n%s", mail)
	}

	out := filepath.Join(base, "respond.out")
	typeInLead("tuios respond -s " + sess + " -w asker choose 2 > " + out + " 2>&1; echo RC=$? >> " + out)
	got := waitForFileText(t, out, func(s string) bool { return strings.Contains(s, "RC=") }, "the lead's respond never finished")
	if !strings.Contains(got, "RC=0") {
		t.Errorf("ASSERTION: the lead's respond to its worker's question failed:\n%s", got)
	}
	waitUntil(t, "the worker read the lead's answer", 20*time.Second, func() (bool, string) {
		screen, _ := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", "asker")
		return strings.Contains(screen, "GOT-2"), screen
	})

	waitUntil(t, "the approval child waits on its approval", 30*time.Second, func() (bool, string) {
		s, _ := tuiosCLI(t, base, "get-agent-state", "-s", sess, "-w", "approver")
		return strings.Contains(s, "needs_input"), s
	})
	refused := filepath.Join(base, "refused.out")
	typeInLead("tuios respond -s " + sess + " -w approver choose 1 > " + refused + " 2>&1; echo RC=$? >> " + refused)
	got = waitForFileText(t, refused, func(s string) bool { return strings.Contains(s, "RC=") }, "the lead's respond to the approval never finished")
	if strings.Contains(got, "RC=0") {
		t.Errorf("ASSERTION: the lead answered its worker's approval, which is the person's:\n%s", got)
	}
	saveText(t, "child-asks-parent", "mail:\n"+mail+"\nrespond to the question:\n"+got)
}
