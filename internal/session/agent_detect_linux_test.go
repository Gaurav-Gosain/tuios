//go:build linux

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadExe checks the real binary is resolved from procfs, using this test
// process as the one process guaranteed to be there. It is the read that lets the
// detector see past a process that renamed itself.
func TestReadExe(t *testing.T) {
	got := readExe(os.Getpid())
	if got == "" {
		t.Fatal("readExe returned nothing for the running test process")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("readExe = %q, want an absolute path", got)
	}
	if strings.HasSuffix(got, " (deleted)") {
		t.Errorf("readExe = %q, want the deleted marker stripped", got)
	}
	if readExe(-1) != "" {
		t.Error("readExe returned a path for an impossible pid")
	}
}

// TestParseStatTPGID checks the tpgid is read from field 8 even when the comm in
// field 2 contains spaces and parentheses.
func TestParseStatTPGID(t *testing.T) {
	// pid (comm) state ppid pgrp session tty_nr tpgid ...
	line := "1234 (weird (name) x) S 1000 1234 1000 34816 4321 4194304 ..."
	got, ok := parseStatTPGID(line)
	if !ok || got != 4321 {
		t.Fatalf("parseStatTPGID = (%d, %v), want (4321, true)", got, ok)
	}

	if _, ok := parseStatTPGID("garbage without paren"); ok {
		t.Error("parseStatTPGID accepted a line with no ')'")
	}
	if _, ok := parseStatTPGID("1 (init) S 0 1"); ok {
		t.Error("parseStatTPGID accepted a truncated line")
	}
}

// TestReadProcessInfoSelf checks the three readings agree about this process,
// which is the one process a test can be certain of.
func TestReadProcessInfoSelf(t *testing.T) {
	info := readProcessInfo(os.Getpid())
	if info.comm == "" {
		t.Error("readProcessInfo returned no comm for the running test process")
	}
	if len(info.argv) == 0 {
		t.Error("readProcessInfo returned no argv for the running test process")
	}
	if info.exe == "" {
		t.Error("readProcessInfo returned no exe for the running test process")
	}
	if got := readProcessInfo(-1); got.comm != "" || got.argv != nil || got.exe != "" {
		t.Errorf("readProcessInfo(-1) = %+v, want the zero value", got)
	}
}
