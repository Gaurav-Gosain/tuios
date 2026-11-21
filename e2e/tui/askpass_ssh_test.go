//go:build unix

package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// askpassKey makes an ed25519 key locked with askpassCanary, with the real
// ssh-keygen, and returns its path and its public key line. ssh-keygen -y on
// it asks for the passphrase through the same OpenSSH code ssh uses, which
// honours SSH_ASKPASS and SSH_ASKPASS_REQUIRE.
func askpassKey(t *testing.T) (key, pub string) {
	t.Helper()
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	key = filepath.Join(t.TempDir(), "id_e2e")
	if b, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", askpassCanary, "-C", "e2e", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("make the key: %v\n%s", err, b)
	}
	b, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b))
	return key, f[0] + " " + f[1]
}

// askpassCleanEnv takes the developer's own askpass program out of the
// daemon's environment, which panes inherit, so the test sees what tuios
// sets.
func askpassCleanEnv(t *testing.T) {
	t.Setenv("SSH_ASKPASS", "")
	t.Setenv("SSH_ASKPASS_REQUIRE", "")
}

// TestAskpassAgentSSHAsksInTheInbox runs ssh-keygen as an agent's tool runs
// a command: in a session of its own, with no terminal. With the pane's
// SSH_ASKPASS, OpenSSH runs tuios's askpass program, which finds no terminal
// and asks the daemon. The dialog names ssh-keygen as the program, with no
// warning. The person types the passphrase, and ssh-keygen unlocks the key
// and prints its public half.
//
// Negative control: with the AskpassEnv lines in buildEnvFor cut, OpenSSH has
// no askpass program to run, ssh-keygen exits 255, and no dialog opens.
func TestAskpassAgentSSHAsksInTheInbox(t *testing.T) {
	askpassCleanEnv(t)
	key, pub := askpassKey(t)
	term, base := attachClientBase(t)
	notty := buildNotty(t)
	out := filepath.Join(t.TempDir(), "pub.txt")

	line := notty + " " + out + " ssh-keygen -y -f " + key + "\n"
	if b, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, b)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Unlock a key", "Program: ssh-keygen -y -f", "Passphrase:")
	}, uiTimeout); err != nil {
		t.Fatalf("the secret dialog never opened for ssh-keygen: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Snapshot(), "does not know this program") {
		t.Errorf("ssh-keygen was shown as an unknown program\n%s", term.Snapshot())
	}
	saveFrame(t, term, "askpass-ssh-keygen")
	pause(t, "let the dialog's settle time pass, as a person reading it does", answerSettle)
	if err := term.SendKeys(askpassCanary + "\r"); err != nil {
		t.Fatalf("type the passphrase: %v", err)
	}
	waitFileHas(t, out+".exit", "ssh-keygen ended", uiTimeout, func(s string) bool { return s != "" })
	got, _ := os.ReadFile(out)
	code, _ := os.ReadFile(out + ".exit")
	if string(code) != "0" || !strings.HasPrefix(strings.TrimSpace(string(got)), pub) {
		t.Fatalf("ssh-keygen exited %s and printed %q, want 0 and %q", code, got, pub)
	}
	if strings.Contains(term.Snapshot(), askpassCanary) {
		t.Errorf("the passphrase is on the client's frame")
	}
	noCanaryIn(t, base)
	alive(t, term, "after an agent's ssh asked for a passphrase")
}

// TestAskpassSSHInAShellPaneAsksInThePane is the person's own ssh. With
// SSH_ASKPASS_REQUIRE=force in every pane, OpenSSH runs tuios's askpass
// program even with a terminal. The program sees that ssh-keygen is the
// terminal's foreground job, so it asks in the pane, as ssh would without
// it: the prompt, then the passphrase with echo off. No dialog opens. The
// person types the passphrase into the pane, and the key unlocks.
//
// Negative control: with foregroundTerminal answering false, the prompt goes
// to the Inbox instead, the dialog opens, and the check that no dialog opens
// fails.
func TestAskpassSSHInAShellPaneAsksInThePane(t *testing.T) {
	askpassCleanEnv(t)
	key, pub := askpassKey(t)
	term, base := attachClientBase(t)
	out := filepath.Join(t.TempDir(), "pub.txt")

	line := "ssh-keygen -y -f " + key + " > " + out + "; echo KG=$?\n"
	if b, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, b)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Enter passphrase for")
	}, uiTimeout); err != nil {
		t.Fatalf("the passphrase prompt never showed in the pane: %v\n%s", err, term.Snapshot())
	}
	stayTrue(t, "no secret dialog opens for a command the person runs in the pane", time.Second, func() (bool, string) {
		snap := term.Snapshot()
		return !strings.Contains(snap, "Unlock a key"), snap
	})
	saveFrame(t, term, "askpass-ssh-in-pane")
	enterTerminalMode(t, term)
	if err := term.SendKeys(askpassCanary + "\r"); err != nil {
		t.Fatalf("type the passphrase: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "KG=0")
	}, uiTimeout); err != nil {
		t.Fatalf("ssh-keygen did not unlock the key with the passphrase typed in the pane: %v\n%s", err, term.Snapshot())
	}
	got, _ := os.ReadFile(out)
	if !strings.HasPrefix(strings.TrimSpace(string(got)), pub) {
		t.Fatalf("ssh-keygen printed %q, want %q", got, pub)
	}
	if strings.Contains(term.Snapshot(), askpassCanary) {
		t.Errorf("the pane echoed the passphrase\n%s", term.Snapshot())
	}
	alive(t, term, "after ssh asked in the pane")
}

// TestAskpassKeepsThePersonsOwnChoice starts the daemon with the person's own
// askpass settings. A pane keeps them, and gets nothing from tuios: an
// SSH_ASKPASS of their own, and an SSH_ASKPASS_REQUIRE of never with no
// SSH_ASKPASS, which tuios must not turn into force.
//
// Negative control: with the check for a set SSH_ASKPASS or
// SSH_ASKPASS_REQUIRE in buildEnvFor cut, the pane's SSH_ASKPASS names the
// tuios-askpass link.
func TestAskpassKeepsThePersonsOwnChoice(t *testing.T) {
	for _, c := range []struct {
		name, askpass, require, want string
	}{
		{"own program", "/usr/bin/my-askpass", "", "ASK=[/usr/bin/my-askpass] REQ=[]"},
		{"require never", "", "never", "ASK=[] REQ=[never]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("SSH_ASKPASS", c.askpass)
			t.Setenv("SSH_ASKPASS_REQUIRE", c.require)
			term, base := attachClientBase(t)
			line := "echo ASK=[$SSH_ASKPASS] REQ=[$SSH_ASKPASS_REQUIRE]\n"
			if b, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
				t.Fatalf("send-text: %v\n%s", err, b)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(s.Text(), c.want)
			}, uiTimeout); err != nil {
				t.Fatalf("the pane did not keep the person's settings, want %s: %v\n%s", c.want, err, term.Snapshot())
			}
		})
	}
}

// buildNotty builds testdata/notty and returns its path.
func buildNotty(t *testing.T) string {
	t.Helper()
	notty := filepath.Join(t.TempDir(), "notty")
	if b, err := exec.Command("go", "build", "-o", notty, "./testdata/notty").CombinedOutput(); err != nil {
		t.Fatalf("build notty: %v\n%s", err, b)
	}
	return notty
}

// TestAskpassGitTerminalPromptZeroFailsFast is a tool that wants a prompt to
// fail at once, as go mod download does with GIT_TERMINAL_PROMPT=0. The
// askpass program asks nothing: ssh-keygen fails within seconds, as it did
// before tuios set SSH_ASKPASS, and no dialog opens.
//
// Negative control: with the GIT_TERMINAL_PROMPT check in runAsAskpass cut,
// the dialog opens and ssh-keygen waits for the person.
func TestAskpassGitTerminalPromptZeroFailsFast(t *testing.T) {
	askpassCleanEnv(t)
	key, _ := askpassKey(t)
	term, base := attachClientBase(t)
	notty := buildNotty(t)
	out := filepath.Join(t.TempDir(), "pub.txt")
	start := time.Now()
	line := "GIT_TERMINAL_PROMPT=0 " + notty + " " + out + " ssh-keygen -y -f " + key + "\n"
	if b, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, b)
	}
	waitFileHas(t, out+".exit", "ssh-keygen ended", uiTimeout, func(s string) bool { return s != "" })
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("ssh-keygen took %s to fail, want a fast failure", took)
	}
	if code, _ := os.ReadFile(out + ".exit"); string(code) == "0" {
		t.Errorf("ssh-keygen unlocked the key with no answer")
	}
	stayTrue(t, "no dialog opens for a prompt GIT_TERMINAL_PROMPT=0 refuses", time.Second, func() (bool, string) {
		snap := term.Snapshot()
		return !strings.Contains(snap, "Unlock a key"), snap
	})
}

// TestAskpassWarnsWhenARealSSHRunsAnotherAskpass is a prompt that a real
// ssh-keygen asks through an askpass program of an agent's own, which calls
// askpass itself. The parent is a real ssh-keygen, but the chain is not
// tuios's: the dialog shows the warning.
//
// Negative control: with askpassChainKnown back to a check of the parent
// alone, no warning shows and the test fails.
func TestAskpassWarnsWhenARealSSHRunsAnotherAskpass(t *testing.T) {
	askpassCleanEnv(t)
	key, _ := askpassKey(t)
	term, base := attachClientBase(t)
	notty := buildNotty(t)
	helper, _ := askpassHelper(t)
	fish := filepath.Join(t.TempDir(), "fish")
	b, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fish, b, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "pub.txt")
	line := "SSH_ASKPASS=" + fish + " " + notty + " " + out + " ssh-keygen -y -f " + key + "\n"
	if b, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, b)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Unlock a key", "Program: ssh-keygen", "tuios cannot check that this program asks")
	}, uiTimeout); err != nil {
		t.Fatalf("a real ssh-keygen running another askpass program got no warning: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "askpass-other-askpass")
	pause(t, "let the dialog's settle time pass", answerSettle)
	if err := term.SendKeys("\x1b"); err != nil {
		t.Fatalf("esc: %v", err)
	}
	waitFileHas(t, out+".exit", "ssh-keygen ended", uiTimeout, func(s string) bool { return s != "" })
}
