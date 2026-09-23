//go:build !windows

package main

import (
	"errors"
	"net"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestDialVerbExplainsAMissingDaemonLikeTheProbe holds dialVerb, which dials
// first and diagnoses only on failure, to the message, cause, fix and exit
// status the up-front probe gave, for an absent daemon and a stale socket.
func TestDialVerbExplainsAMissingDaemonLikeTheProbe(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T)
		state session.DaemonState
	}{
		{"absent", func(*testing.T) {}, session.DaemonAbsent},
		{"stale socket", func(t *testing.T) {
			path, err := session.GetSocketPath()
			if err != nil {
				t.Fatalf("socket path: %v", err)
			}
			l, err := net.Listen("unix", path)
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			// What a killed daemon leaves: the file, with nothing behind it.
			l.(*net.UnixListener).SetUnlinkOnClose(false)
			_ = l.Close()
		}, session.DaemonStaleSocket},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
			tc.setup(t)
			if got := session.DiagnoseDaemon().State; got != tc.state {
				t.Fatalf("the setup left state %v, want %v", got, tc.state)
			}

			client, err := dialVerb()
			if err == nil {
				_ = client.Close()
				t.Fatal("dialVerb connected with no daemon")
			}
			var got *diagnosticError
			if !errors.As(err, &got) {
				t.Fatalf("dialVerb returned %T %v, want a diagnosticError", err, err)
			}
			var want *diagnosticError
			if !errors.As(requireDaemon(), &want) {
				t.Fatal("requireDaemon found a daemon")
			}
			if got.What != want.What || got.Cause != want.Cause || got.Fix != want.Fix || got.Status != want.Status {
				t.Fatalf("dialVerb said\n%+v\nthe probe says\n%+v", got, want)
			}
			if exitStatus(err) != noDaemonStatus {
				t.Fatalf("exit status %d, want %d", exitStatus(err), noDaemonStatus)
			}
		})
	}
}
