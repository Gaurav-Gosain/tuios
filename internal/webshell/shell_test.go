package webshell

import (
	"bytes"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// readUntil collects the guest's output until it contains want.
func readUntil(t *testing.T, p *Pty, want string) string {
	t.Helper()
	var mu sync.Mutex
	var got bytes.Buffer
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := p.Read(buf)
			mu.Lock()
			got.Write(buf[:n])
			hit := strings.Contains(got.String(), want)
			mu.Unlock()
			if hit || err != nil {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("timed out waiting for %q, got %q", want, got.String())
	}
	mu.Lock()
	defer mu.Unlock()
	return got.String()
}

func TestShellRunsCommands(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	SetEventSink(func(e Event) { mu.Lock(); events = append(events, e); mu.Unlock() })
	defer SetEventSink(nil)

	p := NewPty(80, 24)
	cmd := exec.Command("/bin/zsh")
	cmd.Env = []string{"TUIOS_WINDOW_ID=w1"}
	if err := p.Start(cmd); err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "❯")

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"ls lists home", "ls\r", "projects/"},
		{"cd then pwd", "cd projects && pwd\r", "/home/guest/projects"},
		{"cat a file", "cat hello.go\r", "hello from tuios"},
		{"echo writes a file", "echo hi there > note.txt\r", "❯"},
		{"the file reads back", "cat note.txt\r", "hi there"},
		{"unknown command", "nope\r", "command not found"},
		{"tab completes", "ca\t hel\t\r", "hello from tuios"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _ = p.Write([]byte(tt.input))
			readUntil(t, p, tt.want)
		})
	}

	_, _ = p.Write([]byte("exit\r"))
	if err := p.Wait(); err != nil {
		t.Fatalf("exit status: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 || events[0].Type != "shell.command" || events[0].WindowID != "w1" {
		t.Fatalf("first event = %+v", events)
	}
}

func TestTopQuits(t *testing.T) {
	p := NewPty(80, 24)
	if err := p.Start(exec.Command("sh")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "❯")
	_, _ = p.Write([]byte("top\r"))
	readUntil(t, p, "q to quit")
	_, _ = p.Write([]byte("q"))
	readUntil(t, p, "\x1b[?1049l")
	_ = p.Close()
}

// TestRunnableCommandsHighlightAsValid checks that the highlighter agrees with
// the executor: every name the shell runs, aliases included, is drawn green,
// and a name it does not run is drawn red.
func TestRunnableCommandsHighlightAsValid(t *testing.T) {
	names := commandNames()
	// The aliases that were once drawn red although they ran. They are listed
	// by hand so a change that drops one from the table fails here too.
	for _, alias := range []string{"ll", "la", "logout", "less", "more", "bat", "htop", "btop", "cmatrix", "claude", "vi"} {
		if !runnable(alias) {
			t.Errorf("%s is not runnable", alias)
		}
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			for _, line := range []string{name, name + " ", name + " arg"} {
				s := &shell{line: []rune(line)}
				if got := s.highlighted(); !strings.HasPrefix(got, green+name+reset) {
					t.Errorf("highlighted(%q) = %q, want the command in green", line, got)
				}
			}
		})
	}
	for _, name := range []string{"nope", "sh", "l"} {
		s := &shell{line: []rune(name)}
		if got := s.highlighted(); !strings.HasPrefix(got, red+name+reset) {
			t.Errorf("highlighted(%q) = %q, want the command in red", name, got)
		}
	}
}

// TestRunnableCommandsAreFound checks the other direction: nothing the
// highlighter draws green gets "command not found" when it runs. The
// fullscreen programs are left out because they wait for a key.
func TestRunnableCommandsAreFound(t *testing.T) {
	skip := map[string]bool{"top": true, "htop": true, "btop": true, "rain": true, "cmatrix": true, "agent": true, "claude": true, "exit": true, "logout": true}
	for _, name := range commandNames() {
		if skip[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			p := NewPty(80, 24)
			if err := p.Start(exec.Command("sh")); err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			readUntil(t, p, "❯")
			// echo collapses the double space, so the marker only matches
			// the command's output and never the echoed input line.
			_, _ = p.Write([]byte(name + "\recho end  mark\r"))
			if out := readUntil(t, p, "end mark\r\n"); strings.Contains(out, "command not found") {
				t.Errorf("%s: %q", name, out)
			}
		})
	}
}
