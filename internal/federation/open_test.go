package federation

import (
	"errors"
	"strings"
	"testing"
)

// TestOpenArgsIsAnInteractiveSSH pins the shape of the argv: a tty, the host's
// own options and address, its tuios, then the remote command.
func TestOpenArgsIsAnInteractiveSSH(t *testing.T) {
	h := Host{Name: "build", Addr: "me@buildbox", Command: "~/.local/bin/tuios", SSHOptions: []string{"-J", "bastion"}}
	args, err := h.OpenArgs("attach", "api")
	if err != nil {
		t.Fatalf("OpenArgs: %v", err)
	}
	got := strings.Join(args, " ")
	want := "-t -o ConnectTimeout=10 -J bastion me@buildbox ~/.local/bin/tuios attach api"
	if got != want {
		t.Errorf("ASSERTION: argv is\n  %s\nwant\n  %s", got, want)
	}
	for _, a := range args {
		if strings.Contains(a, "BatchMode") {
			t.Errorf("ASSERTION: an interactive open must not set BatchMode, a pane can answer a prompt: %v", args)
		}
	}
}

// TestQuoteRemoteArgKeepsOneWordOneWord is the reason the quoting exists: a
// session name with a space has to reach the far side as one argument.
func TestQuoteRemoteArgKeepsOneWordOneWord(t *testing.T) {
	cases := map[string]string{
		"api":         "api",
		"my session":  "'my session'",
		"a$b":         "'a$b'",
		"":            "''",
		"web-2.0_x@y": "web-2.0_x@y",
	}
	for in, want := range cases {
		got, err := QuoteRemoteArg(in)
		if err != nil {
			t.Errorf("QuoteRemoteArg(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ASSERTION: QuoteRemoteArg(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"it's", `back\slash`, "new\nline"} {
		if _, err := QuoteRemoteArg(bad); !errors.Is(err, ErrUnsafeRemoteArg) {
			t.Errorf("ASSERTION: QuoteRemoteArg(%q) accepted a name no shell quoting can carry safely: %v", bad, err)
		}
	}
}
