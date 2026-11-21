package tuie2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// askpassCanary is the secret the tests type. No file, event, log or frame
// may ever hold it.
const askpassCanary = "Canary-7Qx-passphrase"

// askpassHelper builds testdata/askpasscall and returns its path and the
// file it writes its results to.
func askpassHelper(t *testing.T) (bin, out string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "askpasscall")
	if b, err := exec.Command("go", "build", "-o", bin, "./testdata/askpasscall").CombinedOutput(); err != nil {
		t.Fatalf("build askpasscall: %v\n%s", err, b)
	}
	return bin, filepath.Join(dir, "out.txt")
}

// askpassResult waits until the helper's result file holds want.
func askpassResult(t *testing.T, path, want string) {
	t.Helper()
	waitFileHas(t, path, "the helper wrote "+want, uiTimeout, func(s string) bool { return strings.Contains(s, want) })
}

// askpassEvents subscribes to every event of the session and returns what
// the stream carried so far, for the canary check.
func askpassEvents(t *testing.T, base, session string) func() string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	sub := exec.CommandContext(ctx, tuiosBin, "subscribe", "-s", session)
	sub.Dir = workDirIn(t, base)
	sub.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		sub.Env = append(sub.Env, key+"="+xdgDir(base, key))
	}
	pipe, err := sub.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		lines := bufio.NewScanner(pipe)
		for lines.Scan() {
			mu.Lock()
			seen.WriteString(lines.Text())
			seen.WriteByte('\n')
			mu.Unlock()
		}
		if err := lines.Err(); err != nil {
			mu.Lock()
			fmt.Fprintf(&seen, "the event stream failed: %v\n", err)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() { cancel(); _ = sub.Wait(); <-done })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return seen.String()
	}
}

// noCanaryIn fails when any file under base holds the canary.
func noCanaryIn(t *testing.T, base string) {
	t.Helper()
	files := 0
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		files++
		if bytes.Contains(b, []byte(askpassCanary)) {
			t.Errorf("the secret is in %s", path)
		}
		return nil
	})
	if files == 0 {
		t.Fatalf("no file under %s to check", base)
	}
}

// TestAskpassAsksThePersonForASecret is a secret prompt's round trip against
// a real daemon and a real client. A program in the pane in front of the
// person calls askpass. The client opens the secret dialog on it with no key
// pressed. A process in the pane that tries to answer with its own attach is
// refused. The person types the passphrase, which shows as dots, and enter
// sends it. The program gets exactly what was typed. The secret is then in no
// file under the test's base dir (the daemon's state, the Inbox file and the
// logs), in no event of the session's stream and on no frame.
//
// Negative control: with both nonce checks in verbAnswerAsk cut
// (humanNonceHeld and humanNonceFor), the pane's own call answers the prompt
// before the person sees it, and the wait for the dialog fails.
func TestAskpassAsksThePersonForASecret(t *testing.T) {
	term, base := attachClientBase(t)
	events := askpassEvents(t, base, "e2e-ctrlp")
	bin, out := askpassHelper(t)

	line := bin + " ask " + out + " \"Enter passphrase for key '/tmp/e2e/id_test': \" & " + bin + " steal " + out + "; wait\n"
	if b, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, b)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Unlock a key", "Enter passphrase for key '/tmp/e2e/id_test'", "send", "cancel")
	}, uiTimeout); err != nil {
		t.Fatalf("the secret dialog never opened on the pane in front: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "askpass-dialog")

	askpassResult(t, out, "STEAL=not_human")

	pause(t, "let the dialog's settle time pass, as a person reading it does", answerSettle)
	if err := term.SendKeys(askpassCanary); err != nil {
		t.Fatalf("type the secret: %v", err)
	}
	dots := strings.Repeat("•", len(askpassCanary))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), dots)
	}, uiTimeout); err != nil {
		t.Fatalf("the field never showed one dot per character: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "askpass-typed")
	if err := term.SendKeys("\r"); err != nil {
		t.Fatalf("send: %v", err)
	}
	sum := sha256.Sum256([]byte(askpassCanary))
	askpassResult(t, out, "ASKPASS=answered SHA="+hex.EncodeToString(sum[:]))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Unlock a key")
	}, uiTimeout); err != nil {
		t.Fatalf("the dialog stayed open after the answer: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "askpass-answered")

	if strings.Contains(term.Snapshot(), askpassCanary) {
		t.Errorf("the secret is on the client's frame")
	}
	stream := events()
	if !strings.Contains(stream, "secret") {
		t.Errorf("the event stream never carried the prompt, so it proves nothing:\n%s", stream)
	}
	if strings.Contains(stream, askpassCanary) {
		t.Errorf("the secret is in the event stream")
	}
	noCanaryIn(t, base)
	alive(t, term, "after a secret prompt")
}

// TestAskpassEscCancels cancels the dialog with esc. The program is told the
// prompt was cancelled and gets no secret, and the dialog closes.
//
// Negative control: with esc not bound in AskpassKey, the dialog stays and
// the program waits until its timeout.
func TestAskpassEscCancels(t *testing.T) {
	term, base := attachClientBase(t)
	bin, out := askpassHelper(t)

	line := bin + " ask " + out + " \"git@example.com's password: \"\n"
	if b, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, b)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Enter a password", "git@example.com's password")
	}, uiTimeout); err != nil {
		t.Fatalf("the secret dialog never opened: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "askpass-password")
	pause(t, "let the dialog's settle time pass", answerSettle)
	if err := term.SendKeys("\x1b"); err != nil {
		t.Fatalf("esc: %v", err)
	}
	askpassResult(t, out, "ASKPASS=cancelled")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Enter a password")
	}, uiTimeout); err != nil {
		t.Fatalf("the dialog stayed open after esc: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after a cancelled prompt")
}

// TestAskpassWarnsAboutAFakeSSH is the prompt a program that fishes for a
// passphrase would show. A copy of a binary named ssh, in a folder the pane
// can write, asks for the passphrase of the person's key, with a token in a
// URL on its command line. The dialog warns that tuios does not know the
// program, though its name is ssh. The token is masked in the dialog, and it
// is in no event of the session's stream, in no list-attention answer and in
// no file.
//
// Negative control: with KnownProgram back to a check of the base name, the
// warning is gone and the test fails. With MaskCommandLine cut, the token
// shows in the dialog.
func TestAskpassWarnsAboutAFakeSSH(t *testing.T) {
	const token = "TOKEN-77xqZ"
	term, base := attachClientBase(t)
	events := askpassEvents(t, base, "e2e-ctrlp")
	helper, out := askpassHelper(t)
	fake := filepath.Join(t.TempDir(), "ssh")
	b, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fake, b, 0o755); err != nil {
		t.Fatal(err)
	}

	// The token goes on the command line from a file, so the pane never
	// shows it as typed.
	urlFile := filepath.Join(t.TempDir(), "url")
	if err := os.WriteFile(urlFile, []byte("https://bob:"+token+"@example.com/r.git"), 0o600); err != nil {
		t.Fatal(err)
	}
	line := fake + " spawn " + out + " \"Enter passphrase for key '~/.ssh/id_ed25519': \" \"$(cat " + urlFile + ")\"\n"
	if b, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, b)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Unlock a key", "tuios does not know this program", "bob:***@example.com")
	}, uiTimeout); err != nil {
		t.Fatalf("the dialog did not warn about a copy named ssh, or did not mask its token: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "askpass-fake-ssh")
	if strings.Contains(term.Snapshot(), token) {
		t.Errorf("the token is on the client's frame")
	}
	listed, err := tuiosCLI(t, base, "list-attention", "--json")
	if err != nil {
		t.Fatalf("list-attention: %v\n%s", err, listed)
	}
	if !strings.Contains(listed, "bob:***@example.com") || strings.Contains(listed, token) {
		t.Errorf("list-attention does not hold the masked program line, or holds the token:\n%s", listed)
	}
	pause(t, "let the dialog's settle time pass", answerSettle)
	if err := term.SendKeys("\x1b"); err != nil {
		t.Fatalf("esc: %v", err)
	}
	askpassResult(t, out, "ASKPASS=cancelled")
	stream := events()
	if !strings.Contains(stream, "bob:***@example.com") || strings.Contains(stream, token) {
		t.Errorf("the event stream does not hold the masked program line, or holds the token:\n%s", stream)
	}
	noTokenIn(t, base, token)
}

// noTokenIn fails when any file under base holds s.
func noTokenIn(t *testing.T, base, s string) {
	t.Helper()
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, []byte(s)) {
			t.Errorf("%q is in %s", s, path)
		}
		return nil
	})
}

// TestAskpassDialogLetsGoWhenFocusMoves: the dialog is about one pane. When
// the person moves to another pane it closes and gives the keys back, and the
// prompt waits in the Inbox. Back on the pane, the dialog opens again.
//
// Negative control: with syncAskpass keeping an open dialog whatever pane is
// in front, the dialog stays after the focus moves and the test fails.
func TestAskpassDialogLetsGoWhenFocusMoves(t *testing.T) {
	term, base := attachClientBase(t)
	bin, out := askpassHelper(t)
	if b, err := tuiosCLI(t, base, "split-window", "right", "-s", "e2e-ctrlp", "--name", "other", "--no-focus"); err != nil {
		t.Fatalf("split-window: %v\n%s", err, b)
	}
	line := bin + " ask " + out + " \"Enter passphrase for key '/tmp/e2e/id_focus': \"\n"
	if b, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", "-w", "0", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, b)
	}
	dialog := func(s tuitest.Screen) bool { return screenHas(s, "Unlock a key", "id_focus") }
	if err := term.WaitFor(dialog, uiTimeout); err != nil {
		t.Fatalf("the dialog never opened: %v\n%s", err, term.Snapshot())
	}

	if b, err := tuiosCLI(t, base, "focus-window", "-s", "e2e-ctrlp", "other"); err != nil {
		t.Fatalf("focus-window: %v\n%s", err, b)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !dialog(s) }, uiTimeout); err != nil {
		t.Fatalf("the dialog stayed open after the focus moved to another pane: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "askpass-focus-moved")
	if b, _ := os.ReadFile(out); len(b) != 0 {
		t.Fatalf("the prompt ended when the focus moved: %s", b)
	}

	if b, err := tuiosCLI(t, base, "focus-window", "-s", "e2e-ctrlp", "0"); err != nil {
		t.Fatalf("focus-window back: %v\n%s", err, b)
	}
	if err := term.WaitFor(dialog, uiTimeout); err != nil {
		t.Fatalf("the dialog did not open again on the pane that asks: %v\n%s", err, term.Snapshot())
	}
	pause(t, "let the dialog's settle time pass", answerSettle)
	if err := term.SendKeys("\x1b"); err != nil {
		t.Fatalf("esc: %v", err)
	}
	askpassResult(t, out, "ASKPASS=cancelled")
	alive(t, term, "after the focus moved away from a prompt and back")
}
