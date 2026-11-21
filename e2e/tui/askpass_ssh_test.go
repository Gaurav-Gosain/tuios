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
	dir := t.TempDir()
	notty := filepath.Join(dir, "notty")
	if b, err := exec.Command("go", "build", "-o", notty, "./testdata/notty").CombinedOutput(); err != nil {
		t.Fatalf("build notty: %v\n%s", err, b)
	}
	out := filepath.Join(dir, "pub.txt")

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

// TestAskpassKeepsThePersonsOwnProgram starts the daemon with an SSH_ASKPASS
// of the person's own. A pane keeps it, and gets no SSH_ASKPASS_REQUIRE from
// tuios.
//
// Negative control: with the check for a set SSH_ASKPASS in buildEnvFor cut,
// the pane's SSH_ASKPASS names the tuios-askpass link.
func TestAskpassKeepsThePersonsOwnProgram(t *testing.T) {
	t.Setenv("SSH_ASKPASS", "/usr/bin/my-askpass")
	t.Setenv("SSH_ASKPASS_REQUIRE", "")
	term, base := attachClientBase(t)
	line := "echo ASK=[$SSH_ASKPASS] REQ=[$SSH_ASKPASS_REQUIRE]\n"
	if b, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, b)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "ASK=[/usr/bin/my-askpass] REQ=[]")
	}, uiTimeout); err != nil {
		t.Fatalf("the pane did not keep the person's SSH_ASKPASS: %v\n%s", err, term.Snapshot())
	}
}
