package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// requireLines asserts the three obligations every user-facing failure message
// carries: it says what failed, it names a likely cause, and it gives a command
// to run. The whole point of the diagnostic layer is that no message may skip
// one of these, so this is applied to every case rather than spot-checked.
func requireLines(t *testing.T, context string, err error, wantFragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected an error", context)
	}
	msg := err.Error()

	if !strings.Contains(msg, "Most likely cause:") {
		t.Errorf("%s: message names no likely cause:\n%s", context, msg)
	}
	if !strings.Contains(msg, "Fix:") {
		t.Errorf("%s: message names no fix:\n%s", context, msg)
	}
	if !strings.Contains(msg, "tuios ") {
		t.Errorf("%s: fix does not name a tuios command:\n%s", context, msg)
	}
	for _, want := range wantFragments {
		if !strings.Contains(msg, want) {
			t.Errorf("%s: message missing %q:\n%s", context, want, msg)
		}
	}
}

// TestDegradedStateMessages is the table of every degraded state the CLI can
// put in front of a user, and what each must say. Each row asserts the specific
// remedy alongside the shared what/why/fix structure.
func TestDegradedStateMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want []string
	}{
		{
			name: "daemon never started",
			err:  &diagnosticError{What: session.DaemonDiagnosis{State: session.DaemonAbsent}.Explain()},
			want: []string{"is not running", "tuios new"},
		},
		{
			name: "stale socket left by a crash",
			err: &diagnosticError{What: session.DaemonDiagnosis{
				State:      session.DaemonStaleSocket,
				SocketPath: "/run/user/1000/tuios/tuios.sock",
			}.Explain()},
			want: []string{"stale socket", "/run/user/1000/tuios/tuios.sock", "tuios kill-server"},
		},
		{
			name: "socket owned by another user",
			err: &diagnosticError{What: session.DaemonDiagnosis{
				State:      session.DaemonPermissionDenied,
				SocketPath: "/run/user/1000/tuios/tuios.sock",
			}.Explain()},
			want: []string{"Permission denied", "another user", "XDG_RUNTIME_DIR"},
		},
		{
			name: "session name that does not exist",
			err:  explainMissingSession("wrok", []string{"work", "notes"}),
			want: []string{`"wrok" was not found`, `Did you mean "work"`, "Sessions: notes, work.", "tuios ls"},
		},
		{
			name: "session name with no sessions at all",
			err:  explainMissingSession("work", nil),
			want: []string{"holds no sessions", "tuios new work", "tuios resurrect"},
		},
		{
			name: "terminal too small to render",
			err: &diagnosticError{
				What:  "The terminal is 20x5, which is too small to render a TUIOS session (minimum 40x12).",
				Cause: "the window is too small.",
				Fix:   "resize the terminal, then run 'tuios attach' again.",
			},
			want: []string{"20x5", "too small", "minimum 40x12"},
		},
		{
			name: "TERM with no capabilities",
			err:  checkTerminalCapabilities("dumb"),
			want: []string{`TERM is "dumb"`, "TERM=xterm-256color"},
		},
		{
			name: "TERM not set at all",
			err:  checkTerminalCapabilities(""),
			want: []string{"TERM is not set", "TERM=xterm-256color"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireLines(t, tc.name, tc.err, tc.want...)
		})
	}
}

// TestCapableTerminalsAreAccepted is the other half of the capability check:
// it must not reject terminals that work, or it becomes the problem.
func TestCapableTerminalsAreAccepted(t *testing.T) {
	for _, termEnv := range []string{
		"xterm", "xterm-256color", "screen", "screen-256color",
		"tmux-256color", "alacritty", "kitty", "wezterm", "linux", "vt100",
	} {
		if err := checkTerminalCapabilities(termEnv); err != nil {
			t.Errorf("TERM=%q was rejected but is renderable: %v", termEnv, err)
		}
	}
}

// TestExplainVerbErrorRendersTheDaemonHint checks the CLI shows the remedy the
// daemon computed rather than inventing a second one, and that every hint field
// reaches the user.
func TestExplainVerbErrorRendersTheDaemonHint(t *testing.T) {
	err := explainVerbError("list-windows", &session.VerbCallError{
		Code:    session.ErrVerbSessionNotFound,
		Message: "session wrok not found",
		Hint: &session.VerbHint{
			Param:      "session",
			Command:    "tuios ls",
			DidYouMean: "work",
			Available:  []string{"work", "notes"},
		},
	})

	msg := err.Error()
	for _, want := range []string{
		"list-windows failed",
		"session wrok not found",
		`Did you mean "work"?`,
		"Available: work, notes.",
		"Fix: run 'tuios ls'.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

// TestExplainVerbErrorNamesAcceptedValues covers the invalid_params shape an
// agent hits most: a closed value set the caller guessed wrong.
func TestExplainVerbErrorNamesAcceptedValues(t *testing.T) {
	err := explainVerbError("wait-for", &session.VerbCallError{
		Code:    session.ErrVerbInvalidParams,
		Message: "unknown condition window-outpt",
		Hint: &session.VerbHint{
			Param:      "condition",
			Accepted:   []string{"session-exists", "window-output", "window-exit", "window-idle"},
			DidYouMean: "window-output",
		},
	})

	msg := err.Error()
	for _, want := range []string{
		"Accepted values for condition:",
		"window-output, window-exit, window-idle",
		`Did you mean "window-output"?`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

// TestExplainVerbErrorWithoutHintStillNamesTheCode makes sure an error the
// daemon did not annotate degrades to something usable rather than losing the
// code entirely.
func TestExplainVerbErrorWithoutHintStillNamesTheCode(t *testing.T) {
	err := explainVerbError("resize", &session.VerbCallError{
		Code:    session.ErrVerbInternal,
		Message: "ioctl failed",
	})
	msg := err.Error()
	if !strings.Contains(msg, "resize failed: ioctl failed") {
		t.Errorf("message lost the failure: %s", msg)
	}
	if !strings.Contains(msg, session.ErrVerbInternal) {
		t.Errorf("message lost the error code: %s", msg)
	}
}

// TestExplainVerbErrorPassesThroughNonVerbErrors keeps transport failures from
// being mislabeled as verb failures.
func TestExplainVerbErrorPassesThroughNonVerbErrors(t *testing.T) {
	original := errors.New("connection reset by peer")
	if got := explainVerbError("send-keys", original); !errors.Is(got, original) {
		t.Errorf("a non-verb error should pass through unchanged, got %v", got)
	}
}

// TestExplainDialErrorPassesThroughMismatch is the CLI half of the upgrade bug:
// the protocol mismatch must reach the user with its own message, not be
// rewritten into a generic connection failure.
func TestExplainDialErrorPassesThroughMismatch(t *testing.T) {
	mismatch := &session.ProtocolMismatchError{
		ClientVersion: "1.4.0",
		DaemonVersion: "0.9.0",
	}

	got := explainDialError(mismatch)

	var out *session.ProtocolMismatchError
	if !errors.As(got, &out) {
		t.Fatalf("mismatch was rewritten into %T: %v", got, got)
	}
	msg := got.Error()
	for _, want := range []string{"daemon 0.9.0", "CLI 1.4.0", "tuios kill-server"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

// TestDiagnosticErrorUnwraps keeps errors.Is/As working through the diagnostic
// wrapper, so callers can still branch on the underlying cause.
func TestDiagnosticErrorUnwraps(t *testing.T) {
	underlying := errors.New("boom")
	d := &diagnosticError{What: "Something failed.", Err: underlying}
	if !errors.Is(d, underlying) {
		t.Error("diagnosticError does not unwrap to its cause")
	}
}

// TestDiagnosticErrorLayout pins the message shape, since the value of this
// layer is that every message reads the same way.
func TestDiagnosticErrorLayout(t *testing.T) {
	d := &diagnosticError{
		What:  "Session was not found.",
		Cause: "the name is wrong.",
		Extra: []string{"Sessions: a, b."},
		Fix:   "run 'tuios ls'.",
	}
	want := "Session was not found.\n" +
		"Most likely cause: the name is wrong.\n" +
		"Sessions: a, b.\n" +
		"Fix: run 'tuios ls'."
	if got := d.Error(); got != want {
		t.Errorf("layout:\n%s\nwant:\n%s", got, want)
	}
}

// TestTruncateList keeps a long list of window ids from burying the fix line.
func TestTruncateList(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}

	if got := truncateList(items, 10); len(got) != 5 {
		t.Errorf("a short list should pass through unchanged, got %v", got)
	}
	got := truncateList(items, 2)
	if len(got) != 3 || got[2] != "and 3 more" {
		t.Errorf("truncateList = %v, want the first 2 plus a remainder note", got)
	}
}

// TestClosestNameMatchesTheDaemonPolicy keeps the CLI's suggestion policy
// aligned with the daemon's, so the two never disagree about what a typo meant.
func TestClosestNameMatchesTheDaemonPolicy(t *testing.T) {
	names := []string{"work", "notes", "scratch"}

	tests := []struct {
		target string
		want   string
	}{
		{"wrok", "work"},
		{"scratchh", "scratch"},
		{"note", "notes"},
		{"work", ""},
		{"", ""},
		{"completely-different", ""},
	}
	for _, tc := range tests {
		if got := closestName(tc.target, names); got != tc.want {
			t.Errorf("closestName(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

// TestReportSessionExitDistinguishesTheOutcomes pins the three ways an attached
// client can stop. A kill and a lost daemon are not detaches: they must be
// reported as such and must not exit zero, or a script cannot tell whether its
// session survived.
func TestReportSessionExitDistinguishesTheOutcomes(t *testing.T) {
	tests := []struct {
		name      string
		reason    app.ExitReason
		wantError bool
		want      []string
	}{
		{
			name:      "normal detach",
			reason:    app.ExitNormal,
			wantError: false,
		},
		{
			name:      "session killed underneath the client",
			reason:    app.ExitSessionKilled,
			wantError: true,
			want:      []string{`Session "work" was terminated`, "kill-session", "tuios ls"},
		},
		{
			name:      "daemon lost",
			reason:    app.ExitDaemonLost,
			wantError: true,
			want:      []string{"connection to the TUIOS daemon was lost", "tuios attach work"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := reportSessionExit("work", tc.reason)

			if !tc.wantError {
				if err != nil {
					t.Fatalf("a normal detach must not be an error, got: %v", err)
				}
				return
			}
			requireLines(t, tc.name, err, tc.want...)
		})
	}
}
