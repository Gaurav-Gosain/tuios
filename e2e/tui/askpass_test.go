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
// Negative control: with both nonce checks in verbAskpassAnswer cut
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
