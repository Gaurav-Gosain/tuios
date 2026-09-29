//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPasswordFileOfAnotherUserIsRefused: a file that another user owns can
// be changed by that user, so it is not a secret of this one.
func TestPasswordFileOfAnotherUserIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(path, []byte("pw"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPasswordFileMode(path, info, os.Getuid()); err != nil {
		t.Fatalf("own file refused: %v", err)
	}
	if err := checkPasswordFileMode(path, info, os.Getuid()+1); err == nil {
		t.Fatal("a file owned by another user was accepted")
	}
}

// TestPasswordFileMode400IsAccepted: read-only for the owner is fine.
func TestPasswordFileMode400IsAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(path, []byte("pw\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if pw, err := readPasswordFile(path); err != nil || pw != "pw" {
		t.Fatalf("mode 400 file: %q, %v", pw, err)
	}
}
