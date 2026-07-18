package main

import (
	"errors"
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

// A command that succeeds must leave nothing behind, or every successful run
// would exit non-zero on the strength of an earlier failure.
func TestInterceptErrorsLeavesSuccessAlone(t *testing.T) {
	cmd := &cobra.Command{
		Use:  "ls",
		RunE: func(*cobra.Command, []string) error { return nil },
	}

	var stashed error
	interceptErrors(cmd, &stashed)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE returned %v, want nil", err)
	}
	if stashed != nil {
		t.Fatalf("stashed = %v, want nil", stashed)
	}
	if reportCommandError(stashed) {
		t.Error("reportCommandError reported a failure for a command that succeeded")
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
		wantExit1 bool
		wantText  string
	}{
		{"deliberate quit or detach", app.ExitNormal, false, ""},
		{"session killed externally", app.ExitSessionKilled, true, "was terminated while you were attached"},
		{"daemon lost", app.ExitDaemonLost, true, "connection to the TUIOS daemon was lost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := reportSessionExit("work", tc.reason)

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

// The header has to survive not asking the terminal anything: dropping the
// query must not drop the styling with it.
func TestErrorStylesNeedNoTerminalQuery(t *testing.T) {
	styles := errorStyles()
	if got := styles.ErrorHeader.String(); !strings.Contains(got, "ERROR") {
		t.Errorf("ErrorHeader renders %q, want it to contain ERROR", got)
	}
	if got := styles.ErrorText.Render("hello"); !strings.Contains(got, "hello") {
		t.Errorf("ErrorText renders %q, want it to contain the message", got)
	}
}
