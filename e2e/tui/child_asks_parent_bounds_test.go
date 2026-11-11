package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// childAsksAndTriesItself asks the same question as childAsksQuestion, and
// while it waits, tries to answer its own question with respond, as a child
// that is not its own parent; SELF names the file the attempt writes.
const childAsksAndTriesItself = `sleep 3
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
( sleep 4; tuios respond -w "$TUIOS_PANE_ID" choose 1 > "$SELF" 2>&1; echo "RC=$?" >> "$SELF" ) &
stty raw -echo
c=$(dd bs=1 count=1 2>/dev/null)
stty sane
echo "GOT-$c"
exec sleep 600
`

// childAsksTrust stops on Claude Code's folder trust question.
const childAsksTrust = `sleep 3
printf '%s\n' \
 '╭───────────────────────────────────────────╮' \
 '│ Do you trust the files in this folder?    │' \
 '│ ❯ 1. Yes, proceed                         │' \
 '│   2. No, exit                             │' \
 '╰───────────────────────────────────────────╯'
exec sleep 600
`

// childAsksSignIn reports that it waits for a sign-in.
const childAsksSignIn = `sleep 3
tuios set-agent-state needs_input --kind auth -m 'Sign in to continue'
exec sleep 600
`

// childBoundsFixture is a daemon with a lead pane that reads as an agent, a
// fake claude first on PATH, and the scripts written under base.
type childBoundsFixture struct {
	t          *testing.T
	base, sess string
	lead       string
}

func newChildBoundsFixture(t *testing.T, config string, scripts map[string]string) *childBoundsFixture {
	t.Helper()
	fakeClaudeBin := filepath.Join(t.TempDir(), "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	f := &childBoundsFixture{t: t, base: t.TempDir(), sess: "crew"}
	bin := filepath.Join(f.base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fakeClaudeBin, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SELF", filepath.Join(f.base, "self.out"))
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(f.base, name), []byte(body), 0o755); err != nil { //nolint:gosec // a pane runs it
			t.Fatal(err)
		}
	}
	writeConfig(t, f.base, "[daemon]\nagent_detect_seconds = 1\n"+config)
	killDaemon(t, f.base)
	mustCLI(t, f.base, "new", f.sess, "--detach")
	f.lead = f.agentShell("lead", "")
	return f
}

// agentShell opens a shell pane that reads as an agent, as a lead or a
// worker that starts agents of its own does, with parent as its parent link
// ("" for none), and returns its id.
func (f *childBoundsFixture) agentShell(name, parent string) string {
	f.t.Helper()
	var id string
	if parent == "" {
		id = strings.TrimSpace(mustCLI(f.t, f.base, "new-window", "-s", f.sess, name, "--no-focus", "--print-id"))
	} else {
		id = strings.TrimSpace(mustCLI(f.t, f.base, "start-agent", "sh", "-s", f.sess, "--name", name, "--parent", parent, "--ready-timeout", "2000", "--print-id"))
	}
	f.typeIn(id, "tuios set-agent-state idle --harness claude-code -m "+name)
	waitUntil(f.t, name+" reads as an agent", 20*time.Second, func() (bool, string) {
		out, _ := tuiosCLI(f.t, f.base, "get-agent-state", "-s", f.sess, "-w", id)
		return strings.Contains(out, "idle"), out
	})
	return id
}

// child starts the script under base as a fake claude whose parent is parent.
func (f *childBoundsFixture) child(name, script, parent string) {
	f.t.Helper()
	_, _ = tuiosCLI(f.t, f.base, "start-agent", "claude "+filepath.Join(f.base, script), "-s", f.sess, "--name", name, "--parent", parent, "--ready-timeout", "20000")
}

func (f *childBoundsFixture) typeIn(pane, line string) {
	f.t.Helper()
	mustCLI(f.t, f.base, "send-text", "-s", f.sess, "-w", pane, line+"\n")
}

// respondFrom runs respond from pane against target and returns what it
// printed and whether it succeeded.
func (f *childBoundsFixture) respondFrom(pane, target, choice string) (string, bool) {
	f.t.Helper()
	out := filepath.Join(f.base, "respond-"+pane[:8]+"-"+target+".out")
	f.typeIn(pane, "tuios respond -s "+f.sess+" -w "+target+" choose "+choice+" > "+out+" 2>&1; echo RC=$? >> "+out)
	got := waitForFileText(f.t, out, func(s string) bool { return strings.Contains(s, "RC=") }, "respond from "+pane+" never finished")
	return got, strings.Contains(got, "RC=0")
}

func (f *childBoundsFixture) waitNeedsInput(name string) {
	f.t.Helper()
	waitUntil(f.t, name+" waits on its prompt", 30*time.Second, func() (bool, string) {
		out, _ := tuiosCLI(f.t, f.base, "get-agent-state", "-s", f.sess, "-w", name)
		return strings.Contains(out, "needs_input"), out
	})
}

func (f *childBoundsFixture) mailOf(pane string) string {
	f.t.Helper()
	out, _ := tuiosCLI(f.t, f.base, "read-agent-messages", "-s", f.sess, "-w", pane, "--peek")
	return out
}

// inboxItemOf is the Inbox item of window name, with whether it is held for
// the parent, and false when there is none.
func (f *childBoundsFixture) inboxItemOf(name string) (held, found bool, raw string) {
	f.t.Helper()
	raw, _ = tuiosCLI(f.t, f.base, "call", "list-attention", `{"session":"`+f.sess+`"}`)
	var res struct {
		Items []struct {
			Name            string `json:"name"`
			WithParentUntil int64  `json:"with_parent_until"`
		} `json:"items"`
	}
	if i := strings.Index(raw, "{"); i >= 0 {
		_ = json.Unmarshal([]byte(raw[i:]), &res)
	}
	for _, it := range res.Items {
		if it.Name == name {
			return it.WithParentUntil != 0, true, raw
		}
	}
	return false, false, raw
}

// TestOnlyTheParentAnswersItsChildsQuestion pins who may answer a child's
// question without the respond grant: its parent, and no one else. The lead
// starts a worker (mid) and a sibling of it; mid has a child of its own
// (grandchild) that asks a question and tries to answer it itself.
//
// How it could pass wrongly, written down first:
//   - every refusal could be the same refusal for a reason that is not the
//     rule (the prompt not read, the window not found): the parent's own
//     answer to the same prompt must succeed afterwards and reach the child;
//   - the grandparent could be refused only because its mail never came: the
//     check is respond, not mail, and the grandparent is the lead, whose
//     right over its own child is shown in TestAWorkersQuestionReachesItsLead;
//   - the child's self-answer could be refused only because it ran too early:
//     it runs 4 s after the question is drawn, when the parent's answer
//     works.
//
// NEGATIVE CONTROL: with parentMayRespond reading the root instead of the
// parent (target.AgentLink.Root.Window == window), the lead's answer to its
// grandchild succeeds.
func TestOnlyTheParentAnswersItsChildsQuestion(t *testing.T) {
	f := newChildBoundsFixture(t, "", map[string]string{"asker.sh": childAsksAndTriesItself})
	mid := f.agentShell("mid", f.lead)
	sibling := f.agentShell("sibling", f.lead)
	f.child("grandchild", "asker.sh", mid)
	f.waitNeedsInput("grandchild")

	waitUntil(t, "mid got its child's question by mail", 30*time.Second, func() (bool, string) {
		m := f.mailOf(mid)
		return strings.Contains(m, "Which file should I edit first?"), m
	})
	self := waitForFileText(t, filepath.Join(f.base, "self.out"), func(s string) bool { return strings.Contains(s, "RC=") }, "the child's own respond never finished")
	if strings.Contains(self, "RC=0") {
		t.Errorf("ASSERTION: the child answered its own question:\n%s", self)
	}
	for who, pane := range map[string]string{"the grandparent (lead)": f.lead, "a sibling": sibling} {
		if got, ok := f.respondFrom(pane, "grandchild", "1"); ok {
			t.Errorf("ASSERTION: %s answered the grandchild's question:\n%s", who, got)
		}
	}
	if got, ok := f.respondFrom(mid, "grandchild", "2"); !ok {
		t.Fatalf("ASSERTION: the parent could not answer its child's question:\n%s", got)
	}
	waitUntil(t, "the grandchild read its parent's answer, and only that", 20*time.Second, func() (bool, string) {
		screen, _ := tuiosCLI(t, f.base, "capture-pane", "-s", f.sess, "-w", "grandchild")
		return strings.Contains(screen, "GOT-2"), screen
	})
}

// TestATrustOrSignInPromptReachesOnlyThePerson pins what a parent never
// answers: a child's folder trust question (an approval by its rule) and a
// sign-in. Each goes to the person at once, not held for the parent, is not
// mailed to it, and the parent's respond is refused.
//
// How it could pass wrongly, written down first:
//   - the Inbox could have no item at all, which is not "the person's at
//     once": the item must be there, and not held;
//   - the mail could come late: the check holds for 5 s after the prompts are
//     up, longer than the question's mail takes in the other tests (under 2 s).
//
// NEGATIVE CONTROL: with AgentKindReachesPerson left out of childQuestionLook,
// the trust question is mailed to the lead.
func TestATrustOrSignInPromptReachesOnlyThePerson(t *testing.T) {
	f := newChildBoundsFixture(t, "", map[string]string{"trust.sh": childAsksTrust, "signin.sh": childAsksSignIn})
	f.child("truster", "trust.sh", f.lead)
	f.child("signer", "signin.sh", f.lead)
	f.waitNeedsInput("truster")
	f.waitNeedsInput("signer")
	for _, name := range []string{"truster", "signer"} {
		waitUntil(t, name+"'s prompt is in the Inbox, not held for the parent", 20*time.Second, func() (bool, string) {
			held, found, raw := f.inboxItemOf(name)
			return found && !held, raw
		})
	}
	stayTrue(t, "the lead is mailed neither prompt", 5*time.Second, func() (bool, string) {
		m := f.mailOf(f.lead)
		return !strings.Contains(m, "truster") && !strings.Contains(m, "signer"), m
	})
	for _, name := range []string{"truster", "signer"} {
		if got, ok := f.respondFrom(f.lead, name, "1"); ok {
			t.Errorf("ASSERTION: the lead answered %s's prompt, which is the person's:\n%s", name, got)
		}
	}
}

// TestParentFirstOffGivesTheQuestionToThePerson: with [agents.mail]
// questions_to_parent = false, a child's question is not mailed to its parent,
// is the person's at once, and the parent has no right to answer it.
//
// NEGATIVE CONTROL: without the parentFirst check in respond, the lead's
// answer succeeds.
func TestParentFirstOffGivesTheQuestionToThePerson(t *testing.T) {
	f := newChildBoundsFixture(t, "\n[agents.mail]\nquestions_to_parent = false\n", map[string]string{"asker.sh": childAsksQuestion})
	f.child("asker", "asker.sh", f.lead)
	f.waitNeedsInput("asker")
	waitUntil(t, "the question is in the Inbox, not held for the parent", 20*time.Second, func() (bool, string) {
		held, found, raw := f.inboxItemOf("asker")
		return found && !held, raw
	})
	stayTrue(t, "the lead is not mailed the question", 5*time.Second, func() (bool, string) {
		m := f.mailOf(f.lead)
		return !strings.Contains(m, "Which file should I edit first?"), m
	})
	if got, ok := f.respondFrom(f.lead, "asker", "2"); ok {
		t.Errorf("ASSERTION: the lead answered its child's question with questions_to_parent off:\n%s", got)
	}
}
