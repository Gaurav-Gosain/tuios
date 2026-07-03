//go:build unix

package vt

import (
	"os"
	"path/filepath"
	"testing"
)

// writeShmFile writes a temp file under /dev/shm and returns its base name.
func writeShmFile(t *testing.T, contents []byte) string {
	t.Helper()
	f, err := os.CreateTemp("/dev/shm", "tuios-shm-test-*")
	if err != nil {
		t.Skipf("cannot create /dev/shm test file: %v", err)
	}
	name := filepath.Base(f.Name())
	if _, err := f.Write(contents); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		t.Fatalf("write shm file: %v", err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/dev/shm", name)) })
	return name
}

func TestLoadSharedMemoryClampsBeyondFileSize(t *testing.T) {
	contents := []byte("small payload")
	name := writeShmFile(t, contents)

	// Hostile size far larger than the backing file. Without the clamp this
	// mmaps past EOF and the copy raises an uncatchable SIGBUS.
	data, err := loadSharedMemory(name, 1<<20)
	if err != nil {
		t.Fatalf("expected clamp, got error: %v", err)
	}
	if len(data) != len(contents) {
		t.Fatalf("expected clamped length %d, got %d", len(contents), len(data))
	}
	if string(data) != string(contents) {
		t.Fatalf("clamped data mismatch: %q", data)
	}
}

func TestLoadSharedMemoryRejectsEmptyFile(t *testing.T) {
	name := writeShmFile(t, nil)
	if _, err := loadSharedMemory(name, 4096); err == nil {
		t.Fatal("expected error for zero-size backing file")
	}
}

func TestLoadSharedMemoryRejectsEmptyName(t *testing.T) {
	if _, err := loadSharedMemory("", 4096); err == nil {
		t.Fatal("expected error for empty shared memory name")
	}
}
