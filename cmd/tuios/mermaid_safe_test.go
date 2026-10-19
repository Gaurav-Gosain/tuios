//go:build !windows && !js

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/mermaid"
)

// TestMermaidWritesNoTerminalControls runs tuios mermaid on diagrams whose
// labels, errors and fallback source hold terminal controls, and checks that
// none reaches stdout or stderr. "#27;" in a label decodes to ESC, so the
// first source would set the window title, and the second writes the
// clipboard through OSC 52 on terminals that allow it.
func TestMermaidWritesNoTerminalControls(t *testing.T) {
	sources := map[string]string{
		"label.mmd":     "flowchart LR\n  A[\"#27;]2;pwned#7;\"] -->|\"#155;31m\"| B\n",
		"raw.mmd":       "flowchart LR\n  A[\"x\x1b]52;c;aGk=\x07y\"] --> B\n",
		"error.mmd":     "flowchart LR\n  A[\x1b]2;pwned\x07 --> \n",
		"untyped.mmd":   "pie title \x1b[2J\x9b31m\u202e\n  \"a\" : 1\n",
		"sequence.mmd":  "sequenceDiagram\n  A->>B: #27;[2J#127;\n",
		"broken.md":     "# x\n\n```mermaid\nflowchart LR\n  A[\x1b[31m\n```\n",
		"invalid.mmd":   "flowchart LR\n  A[\"\xff\x1b\"] --> B\n",
		"bidi-note.mmd": "sequenceDiagram\n  Note over A: abc\u2066def\n",
		"marks.mmd":     "pie title a\u200eb\u2028c\u061c\n  \"a\" : 1\n",
	}
	dir := t.TempDir()
	for name, src := range sources {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, format := range []string{"text", "kitty"} {
			out, errOut := runCaptured(t, func(stdout *os.File) error {
				return runMermaid(path, mermaidOptions{format: format, cols: 120, rows: 40}, stdout)
			})
			if format == "kitty" {
				// The kitty APC frames and the line break after each are tuios's
				// own; what is left must be clean.
				out = strings.ReplaceAll(stripKitty(out), "\r\n", "\n")
			}
			for what, s := range map[string]string{"stdout": out, "stderr": errOut} {
				if bad := firstUnsafe(s); bad >= 0 {
					t.Errorf("%s -f %s: %s holds %U at %d:\n%q", name, format, what, []rune(s)[bad], bad, s)
				}
			}
		}
	}
}

// TestMermaidReadsRegularFilesOnly gives tuios mermaid a FIFO and a file
// past MaxSource. Neither may block or be read whole.
func TestMermaidReadsRegularFilesOnly(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe.mmd")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := mermaid.ReadSource(fifo); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, mermaid.ErrNotRegular) {
			t.Fatalf("FIFO: %v, want ErrNotRegular", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO blocked")
	}
	big := filepath.Join(dir, "big.mmd")
	if err := os.WriteFile(big, []byte("flowchart LR\n"+strings.Repeat("%%\n", mermaid.MaxSource/3+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mermaid.ReadSource(big); err == nil {
		t.Fatal("a file past MaxSource was read")
	}
}

func firstUnsafe(s string) int {
	for i, r := range []rune(s) {
		if r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r <= 0x9f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) ||
			r == 0x200e || r == 0x200f || r == 0x061c || r == 0x2028 || r == 0x2029) {
			return i
		}
	}
	return -1
}

func stripKitty(s string) string {
	for {
		i := strings.Index(s, "\x1b_G")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], "\x1b\\")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+2:]
	}
}

// runCaptured runs f with stdout in a file and os.Stderr in another, and
// returns what each got.
func runCaptured(t *testing.T, f func(stdout *os.File) error) (string, string) {
	t.Helper()
	dir := t.TempDir()
	out, err := os.Create(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.Create(filepath.Join(dir, "err"))
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = errFile
	_ = f(out)
	os.Stderr = saved
	_ = out.Close()
	_ = errFile.Close()
	a, _ := os.ReadFile(filepath.Join(dir, "out"))
	b, _ := os.ReadFile(filepath.Join(dir, "err"))
	return string(a), string(b)
}
