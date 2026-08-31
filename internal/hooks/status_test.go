package hooks

import (
	"strings"
	"testing"
)

// A hook used to run with its output discarded and its error dropped, so a
// command that was never found looked exactly like one that worked. These pin
// the three facts that make the difference visible, and the bound that keeps a
// noisy hook from costing whatever it decides to write.

// TestAFailingHookRecordsItsExitCodeAndStderr is the whole complaint: "my hook
// does not fire" was unanswerable because a hook that ran and failed reported
// nothing at all.
func TestAFailingHookRecordsItsExitCodeAndStderr(t *testing.T) {
	m := NewManager()
	m.Register(AfterNewWindow, "echo 'the disk is on fire' >&2; exit 3")

	m.Fire(AfterNewWindow, Context{WindowID: "w1"})
	m.Wait()

	statuses := m.Statuses()
	if len(statuses) != 1 {
		t.Fatalf("Statuses reported %d rows, want 1", len(statuses))
	}
	st := statuses[0]
	if st.Runs != 1 {
		t.Errorf("runs = %d, want 1", st.Runs)
	}
	if st.LastExit != 3 {
		t.Errorf("last exit = %d, want the 3 the command exited with", st.LastExit)
	}
	if !strings.Contains(st.LastError, "the disk is on fire") {
		t.Errorf("last error = %q, want the command's stderr", st.LastError)
	}
	if st.LastRun.IsZero() {
		t.Error("last run is zero, so nothing says when the hook ran")
	}
}

// TestAHookThatIsNotOnThePathReportsWhy covers the other half of the complaint.
// The command never starts, so there is no exit code to report and the shell's
// own message is the only thing that names the mistake.
func TestAHookThatIsNotOnThePathReportsWhy(t *testing.T) {
	m := NewManager()
	m.Register(AfterNewWindow, "tuios-no-such-program-exists")

	m.Fire(AfterNewWindow, Context{})
	m.Wait()

	st := m.Statuses()[0]
	if st.LastExit == 0 {
		t.Errorf("last exit = 0, but the command could not be run")
	}
	if st.LastError == "" {
		t.Error("last error is empty, so the failure is still silent")
	}
}

// TestASucceedingHookClearsThePreviousFailure keeps the row honest: a hook that
// was fixed has to stop reporting the failure it used to have, or the user
// cannot tell the fix worked.
func TestASucceedingHookClearsThePreviousFailure(t *testing.T) {
	m := NewManager()
	m.Register(AfterNewWindow, "exit ${TUIOS_WORKSPACE}")

	m.Fire(AfterNewWindow, Context{Workspace: 4})
	m.Wait()
	if got := m.Statuses()[0].LastError; got == "" {
		t.Fatal("the failing run recorded no error")
	}

	m.Fire(AfterNewWindow, Context{Workspace: 0})
	m.Wait()

	st := m.Statuses()[0]
	if st.LastError != "" {
		t.Errorf("last error = %q after a clean run, want empty", st.LastError)
	}
	if st.LastExit != 0 {
		t.Errorf("last exit = %d after a clean run, want 0", st.LastExit)
	}
	if st.Runs != 2 {
		t.Errorf("runs = %d, want both firings counted", st.Runs)
	}
}

// wantStderrCap is the bound, written out rather than read from
// stderrTailLimit. A test that asserts against the constant it is guarding
// passes whatever the constant is changed to, which is not a guard.
const wantStderrCap = 1024

// TestStderrIsKeptOnlyUpToTheLimit is the bound. A hook is a user command and
// may write without limit; keeping all of it would let a hook grow the process
// that runs it.
func TestStderrIsKeptOnlyUpToTheLimit(t *testing.T) {
	tail := &tailBuffer{limit: stderrTailLimit}
	if stderrTailLimit != wantStderrCap {
		t.Fatalf("stderrTailLimit is %d, want %d: the cap is the point of this file",
			stderrTailLimit, wantStderrCap)
	}
	// One megabyte, in chunks, the way a pipe delivers it.
	chunk := strings.Repeat("A", 4096)
	for range 256 {
		_, _ = tail.Write([]byte(chunk))
	}
	_, _ = tail.Write([]byte("THE-END"))

	got := tail.String()
	if len(got) > wantStderrCap {
		t.Fatalf("kept %d bytes of stderr, want at most %d", len(got), wantStderrCap)
	}
	if !strings.HasSuffix(got, "THE-END") {
		t.Errorf("kept the wrong end of the output: %q", got[max(0, len(got)-32):])
	}
}

// TestASingleOversizeWriteIsAlsoBounded covers the write that arrives in one
// piece rather than in chunks, which is the branch a chunked test never takes.
func TestASingleOversizeWriteIsAlsoBounded(t *testing.T) {
	tail := &tailBuffer{limit: stderrTailLimit}
	_, _ = tail.Write([]byte(strings.Repeat("B", 100000) + "TAIL"))

	got := tail.String()
	if len(got) > wantStderrCap {
		t.Fatalf("kept %d bytes of stderr, want at most %d", len(got), wantStderrCap)
	}
	if !strings.HasSuffix(got, "TAIL") {
		t.Errorf("kept the wrong end of the output: %q", got[max(0, len(got)-32):])
	}
}

// TestALoudHookIsBoundedEndToEnd drives the bound through a real command, so
// the limit is proved where it is wired and not only where it is implemented.
func TestALoudHookIsBoundedEndToEnd(t *testing.T) {
	m := NewManager()
	// Roughly 400 KiB of stderr, then a failure, so the row is recorded.
	m.Register(AfterNewWindow, `awk 'BEGIN{for(i=0;i<10000;i++) printf "line %d of noise\n", i > "/dev/stderr"}'; exit 9`)

	m.Fire(AfterNewWindow, Context{})
	m.Wait()

	st := m.Statuses()[0]
	if st.LastExit != 9 {
		t.Fatalf("last exit = %d, want 9", st.LastExit)
	}
	if len(st.LastError) > wantStderrCap {
		t.Fatalf("last error is %d bytes, want at most %d", len(st.LastError), wantStderrCap)
	}
	if !strings.Contains(st.LastError, "of noise") {
		t.Errorf("last error kept none of the output: %q", st.LastError)
	}
	// The tail, not the head: the last thing a failing command says is the part
	// that explains it.
	if !strings.Contains(st.LastError, "line 9999 of noise") {
		t.Errorf("last error kept the head of the output rather than the tail: %q", st.LastError)
	}
}

// TestReloadingTheTableForgetsTheOldRuns stops a row describing a command that
// is no longer at that position in the table.
func TestReloadingTheTableForgetsTheOldRuns(t *testing.T) {
	m := NewManager()
	m.Register(AfterNewWindow, "exit 5")
	m.Fire(AfterNewWindow, Context{})
	m.Wait()
	if m.Statuses()[0].Runs != 1 {
		t.Fatal("the first run was not recorded")
	}

	m.LoadFromConfig(map[string]any{"after-new-window": "true"})

	st := m.Statuses()[0]
	if st.Command != "true" {
		t.Fatalf("command = %q after a reload, want the new one", st.Command)
	}
	if st.Runs != 0 || st.LastExit != 0 || st.LastError != "" {
		t.Errorf("a reloaded hook carried the old command's run state: %+v", st)
	}
}

// TestStatusesCoverEveryRegisteredCommand keeps the report complete: a hook the
// user wrote and that has never run must still be listed, because "it is not
// there" and "it never ran" are different answers.
func TestStatusesCoverEveryRegisteredCommand(t *testing.T) {
	m := NewManager()
	m.Register(AfterNewWindow, "one")
	m.Register(AfterNewWindow, "two")
	m.Register(AfterDetach, "three")

	statuses := m.Statuses()
	if len(statuses) != 3 {
		t.Fatalf("Statuses reported %d rows, want 3", len(statuses))
	}
	for _, st := range statuses {
		if st.Runs != 0 || !st.LastRun.IsZero() {
			t.Errorf("a hook that never fired reports a run: %+v", st)
		}
		if st.Event == "" || st.Command == "" {
			t.Errorf("a row names neither its event nor its command: %+v", st)
		}
	}
}

// TestTwoCommandsOnOneEventKeepSeparateRows stops one failing command hiding
// behind a sibling that works.
func TestTwoCommandsOnOneEventKeepSeparateRows(t *testing.T) {
	m := NewManager()
	m.Register(AfterNewWindow, "true")
	m.Register(AfterNewWindow, "exit 7")

	m.Fire(AfterNewWindow, Context{})
	m.Wait()

	statuses := m.Statuses()
	if len(statuses) != 2 {
		t.Fatalf("Statuses reported %d rows, want 2", len(statuses))
	}
	byCommand := map[string]int{}
	for _, st := range statuses {
		byCommand[st.Command] = st.LastExit
	}
	if byCommand["true"] != 0 {
		t.Errorf("the working command reports exit %d, want 0", byCommand["true"])
	}
	if byCommand["exit 7"] != 7 {
		t.Errorf("the failing command reports exit %d, want 7", byCommand["exit 7"])
	}
}
