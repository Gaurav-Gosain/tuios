package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestKeepFailedPatchUsesAnUnpredictableFile: the kept patch goes in the shared
// temporary directory. A fixed name there lets another user create the file
// first, read the patch, or change it before the user runs git apply. Each
// call must make a new file only this user can read.
func TestKeepFailedPatchUsesAnUnpredictableFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	// A file planted at the old fixed name, readable by everyone.
	planted := filepath.Join(tmp, "tuios-pull-feature.patch")
	if err := os.WriteFile(planted, []byte("planted"), 0o666); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(planted, 0o666)

	first, err := keepFailedPatch("feature", []byte("diff one"))
	if err != nil {
		t.Fatalf("keep: %v", err)
	}
	second, err := keepFailedPatch("feature", []byte("diff two"))
	if err != nil {
		t.Fatalf("keep: %v", err)
	}

	if first == planted || second == planted {
		t.Fatalf("the patch went to the predictable path %s", planted)
	}
	if first == second {
		t.Fatalf("two pulls of one branch share the path %s", first)
	}
	if got, _ := os.ReadFile(planted); string(got) != "planted" {
		t.Fatalf("the planted file was overwritten: %q", got)
	}
	for path, want := range map[string]string{first: "diff one", second: "diff two"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s holds %q (%v), want %q", path, got, err, want)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Fatalf("%s has mode %o, want 600", path, perm)
			}
		}
	}
}
