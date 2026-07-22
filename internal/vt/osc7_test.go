package vt

import "testing"

// TestOSC7_FiresWorkingDirectoryCallback proves the detection hook: an OSC 7
// working-directory sequence invokes the WorkingDirectory callback with the
// reported file:// URI, which is what tuios wires to project-tape detection.
func TestOSC7_FiresWorkingDirectoryCallback(t *testing.T) {
	e := NewEmulator(80, 24)
	defer e.Close()

	var got string
	var calls int
	e.SetCallbacks(Callbacks{
		WorkingDirectory: func(cwd string) {
			got = cwd
			calls++
		},
	})

	// OSC 7 ; file://host/path ST
	e.Write([]byte("\x1b]7;file://localhost/home/user/project\x1b\\"))

	if calls != 1 {
		t.Fatalf("WorkingDirectory called %d times, want 1", calls)
	}
	if got != "file://localhost/home/user/project" {
		t.Fatalf("cwd = %q, want the reported file URI", got)
	}
}
