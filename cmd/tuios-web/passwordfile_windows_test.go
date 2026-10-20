//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/winacl"
)

// TestPasswordFileDACL: on Windows the owner and the DACL decide who can read
// the password. A file only its owner can read is accepted. The same file is
// refused once Everyone may read it.
func TestPasswordFileDACL(t *testing.T) {
	me, _, err := winacl.CurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(path, []byte("pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	icacls := func(args ...string) {
		t.Helper()
		out, err := exec.Command("icacls", append([]string{path}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("icacls %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	// The remedy the refusal prints: drop inherited entries, keep the owner.
	icacls("/inheritance:r", "/grant:r", "*"+me.String()+":F")
	if pw, err := readPasswordFile(path); err != nil || pw != "pw" {
		t.Fatalf("a file only its owner can read: %q, %v", pw, err)
	}

	// S-1-1-0 is Everyone.
	icacls("/grant", "*S-1-1-0:(R)")
	if _, err := readPasswordFile(path); err == nil {
		t.Fatal("a password file Everyone can read was accepted")
	}
}
