package applist

import (
	"runtime"
	"testing"
)

// TestCommandLineQuotesExecArguments: the line the launcher types out is read
// by a shell, so every Exec argument has to reach it quoted.
func TestCommandLineQuotesExecArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows quotes by a different rule; this asserts the POSIX one")
	}
	e := Entry{Name: "term", Exec: []string{"/opt/bin/run it", "a b"}}
	if got, want := e.CommandLine(), `'/opt/bin/run it' 'a b'`; got != want {
		t.Fatalf("CommandLine = %q, want %q", got, want)
	}
}
