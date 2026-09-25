package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/spf13/cobra"
)

// `tuios attach` used to tear down its interface, print why it was stopping,
// and then sit there for four seconds before the process ended, which reads as
// a client that has hung. The stall was fang's error renderer querying the
// terminal for its background color, so the tests here pin the mechanism that
// keeps a command failure away from that renderer, and pin the exit status each
// of the three ways an attach can end must produce.

// A failing command must report success to cobra, so that fang finds no error
// to render, and must leave the error where main can print it and set the exit
// status from it. Returning the error instead is what put it through the slow
// renderer.
func TestInterceptErrorsKeepsFailuresFromFang(t *testing.T) {
	want := errors.New("boom")
	cmd := &cobra.Command{
		Use:  "attach",
		RunE: func(*cobra.Command, []string) error { return want },
	}

	var stashed error
	interceptErrors(cmd, &stashed)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE returned %v to cobra, want nil so fang renders nothing", err)
	}
	if !errors.Is(stashed, want) {
		t.Fatalf("stashed error = %v, want %v", stashed, want)
	}
	if !cmd.SilenceUsage {
		t.Error("a command that failed with an explained error should not also print usage")
	}
}

// Subcommands are where every session command lives, so the walk has to reach
// them and not just the root.
func TestInterceptErrorsReachesSubcommands(t *testing.T) {
	want := errors.New("nested")
	sub := &cobra.Command{
		Use:  "attach",
		RunE: func(*cobra.Command, []string) error { return want },
	}
	root := &cobra.Command{Use: "tuios"}
	root.AddCommand(sub)

	var stashed error
	interceptErrors(root, &stashed)

	if err := sub.RunE(sub, nil); err != nil {
		t.Fatalf("subcommand RunE returned %v, want nil", err)
	}
	if !errors.Is(stashed, want) {
		t.Fatalf("stashed error = %v, want %v", stashed, want)
	}
}

// The three ways an attach ends. A deliberate quit is not a failure and must
// exit zero; a session killed from elsewhere and a lost daemon are both
// failures and must exit non-zero with their diagnostic, so that a script or an
// agent driving tuios does not read a dead session as success.
func TestAttachExitStatusPerReason(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reason    app.ExitReason
		killed    bool
		wantExit1 bool
		wantText  string
	}{
		{"detach", app.ExitNormal, false, false, ""},
		{"deliberate quit that killed the session", app.ExitNormal, true, false, ""},
		{"session killed externally", app.ExitSessionKilled, false, true, "was terminated while you were attached"},
		{"daemon lost", app.ExitDaemonLost, false, true, "connection to the TUIOS daemon was lost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := reportSessionExit("work", "", tc.reason, tc.killed)

			if got := err != nil; got != tc.wantExit1 {
				t.Fatalf("reportSessionExit returned error=%v, want error=%v (err: %v)", got, tc.wantExit1, err)
			}
			if !tc.wantExit1 {
				return
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("diagnostic does not mention %q:\n%s", tc.wantText, err.Error())
			}
			// main exits non-zero on exactly this signal.
			if !reportCommandError(err) {
				t.Error("reportCommandError did not report a failure, so the client would exit 0")
			}
		})
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what was
// written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = orig
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(data)
}
