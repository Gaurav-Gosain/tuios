package main

import (
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestXpanesItemsFromArgsAndStdin(t *testing.T) {
	got, err := xpanesItems([]string{"a", " ", "b c"}, strings.NewReader("ignored\n"))
	if err != nil || !slices.Equal(got, []string{"a", "b c"}) {
		t.Fatalf("args: %q, %v", got, err)
	}
	got, err = xpanesItems(nil, strings.NewReader("host1\r\n\n  \nhost 2\nlast"))
	if err != nil || !slices.Equal(got, []string{"host1", "host 2", "last"}) {
		t.Fatalf("stdin: %q, %v", got, err)
	}
	if _, err := xpanesItems(nil, strings.NewReader("\n\n")); err == nil || !strings.Contains(err.Error(), "no items") {
		t.Fatalf("no items: %v", err)
	}
}

// Each quoted item reads back as itself in sh, whatever it holds.
func TestXpanesQuoteRoundTripsThroughSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	for _, item := range []string{"plain", "user@host:22", "two words", "it's", `"$HOME" $(id) ; rm -rf / #`, "tab\there", "new\nline", "*"} {
		out, err := exec.Command("sh", "-c", "printf %s "+xpanesQuote(item)).Output()
		if err != nil || string(out) != item {
			t.Errorf("item %q came back as %q (%v) through %s", item, out, err, xpanesQuote(item))
		}
	}
	if got := xpanesQuote("host-1.example.com"); got != "host-1.example.com" {
		t.Errorf("a safe word was quoted: %s", got)
	}
}

func TestXpanesPanesBuildsEachArgv(t *testing.T) {
	panes := xpanesPanes([]string{"a b", "c"}, 1, "echo ITEM-{}; exec sh", "{}", "/bin/zsh", xpanesSpeedyClose)
	want := []string{"env", "TUIOS_XPANES_ITEM=a b", "TUIOS_XPANES_INDEX=1", "sh", "-c", "echo ITEM-'a b'; exec sh"}
	if len(panes) != 2 || !slices.Equal(panes[0].Argv, want) || panes[0].Title != "a b" {
		t.Fatalf("panes = %+v", panes)
	}
	if got := panes[1].Argv; got[2] != "TUIOS_XPANES_INDEX=2" || got[5] != "echo ITEM-c; exec sh" {
		t.Fatalf("second pane argv = %q", got)
	}

	// -n 2 and a placeholder of its own.
	panes = xpanesPanes([]string{"x", "y z", "w"}, 2, "diff %", "%", "", xpanesSpeedyClose)
	if len(panes) != 2 || panes[0].Argv[5] != "diff x 'y z'" || panes[1].Argv[5] != "diff w" {
		t.Fatalf("grouped panes = %+v", panes)
	}
	if panes[0].Argv[1] != "TUIOS_XPANES_ITEM=x y z" {
		t.Fatalf("grouped item = %q", panes[0].Argv[1])
	}

	// No command: the shell, with the item in the environment.
	panes = xpanesPanes([]string{"db1"}, 1, "", "{}", "/bin/zsh", xpanesInteractive)
	if want := []string{"env", "TUIOS_XPANES_ITEM=db1", "TUIOS_XPANES_INDEX=1", "/bin/zsh"}; !slices.Equal(panes[0].Argv, want) {
		t.Fatalf("shell pane argv = %q", panes[0].Argv)
	}
}

func TestXpanesLayoutNames(t *testing.T) {
	for in, want := range map[string]string{
		"": "tiled", "t": "tiled", "tiled": "tiled",
		"eh": "even-horizontal", "Even-Horizontal": "even-horizontal",
		"ev": "even-vertical", "even-vertical": "even-vertical",
	} {
		if got, err := xpanesLayout(in); err != nil || got != want {
			t.Errorf("xpanesLayout(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := xpanesLayout("main-vertical"); err == nil {
		t.Error("main-vertical was accepted")
	}
}

// More than the limit needs --force, and nothing is dialed before the check.
func TestXpanesRefusesTooManyPanes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("xpanes refuses on Windows first")
	}
	items := make([]string, xpanesMaxPanes+1)
	for i := range items {
		items[i] = "h"
	}
	err := runXpanes(xpanesOptions{placeholder: "{}", layout: "tiled", perPane: 1}, items)
	if err == nil || !strings.Contains(err.Error(), "Add --force") {
		t.Fatalf("err = %v", err)
	}
	// Grouped two per pane, the same items fit.
	if n := len(xpanesPanes(items, 2, "", "{}", "sh", xpanesInteractive)); n > xpanesMaxPanes {
		t.Fatalf("%d panes with -n 2", n)
	}
}

// --ssh ends ssh's options before the item, so an item that starts with - is
// a host name and not an option.
func TestXpanesSSHEndsTheOptions(t *testing.T) {
	line := xpanesCommandLine(xpanesOptions{ssh: true, placeholder: "{}"})
	panes := xpanesPanes([]string{"-oProxyCommand=evil"}, 1, line, "{}", "sh", xpanesSpeedyClose)
	if got := panes[0].Argv[5]; got != "ssh -- -oProxyCommand=evil" {
		t.Fatalf("argv = %q", got)
	}
}

// On another machine, a pane with no command has no argv, so the daemon there
// starts its own shell. On this machine it is $SHELL.
func TestXpanesShellOnAnotherMachine(t *testing.T) {
	if got := xpanesShell("build", "/usr/bin/fish"); got != "" {
		t.Fatalf("remote shell = %q, want the daemon's", got)
	}
	if got := xpanesShell("", "/usr/bin/fish"); got != "/usr/bin/fish" {
		t.Fatalf("local shell = %q", got)
	}
	if got := xpanesShell("", ""); got != "/bin/sh" {
		t.Fatalf("local shell without $SHELL = %q", got)
	}
	if panes := xpanesPanes([]string{"db1"}, 1, "", "{}", "", xpanesInteractive); len(panes[0].Argv) != 0 {
		t.Fatalf("a remote shell pane has argv %q", panes[0].Argv)
	}
}

// Without speedy mode the pane is the shell, and the command is the line
// tuios types into it, so the shell stays when the command stops.
func TestXpanesInteractiveTypesTheCommand(t *testing.T) {
	panes := xpanesPanes([]string{"a b"}, 1, "ping -c1 {}", "{}", "/bin/zsh", xpanesInteractive)
	if want := []string{"env", "TUIOS_XPANES_ITEM=a b", "TUIOS_XPANES_INDEX=1", "/bin/zsh"}; !slices.Equal(panes[0].Argv, want) {
		t.Fatalf("argv = %q", panes[0].Argv)
	}
	if panes[0].Line != "ping -c1 'a b'" {
		t.Fatalf("line = %q", panes[0].Line)
	}
}

// -s runs the command as the pane's program and then holds the pane until
// Enter. -ss runs it alone, so the pane closes with it.
func TestXpanesSpeedyHoldsAndSpeedyCloseDoesNot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	hold := xpanesPanes([]string{"x"}, 1, "echo RAN-{}", "{}", "/bin/zsh", xpanesSpeedy)[0]
	if hold.Line != "" || hold.Argv[3] != "sh" || !strings.HasSuffix(hold.Argv[5], "read _") {
		t.Fatalf("-s pane = %+v", hold)
	}
	// Run it with Enter on stdin: the command runs, the message shows, and
	// the read takes the Enter.
	cmd := exec.Command(hold.Argv[3], hold.Argv[4:]...)
	cmd.Stdin = strings.NewReader("\n")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "RAN-x") || !strings.Contains(string(out), xpanesHoldMessage) {
		t.Fatalf("-s pane printed %q, %v", out, err)
	}
	closing := xpanesPanes([]string{"x"}, 1, "echo RAN-{}", "{}", "/bin/zsh", xpanesSpeedyClose)[0]
	if closing.Line != "" || closing.Argv[5] != "echo RAN-x" {
		t.Fatalf("-ss pane = %+v", closing)
	}
}

func TestXpanesSpeedyModeOptions(t *testing.T) {
	cases := []struct {
		o       xpanesOptions
		command string
		want    int
		err     string
	}{
		{xpanesOptions{}, "", xpanesInteractive, ""},
		{xpanesOptions{speedy: 1}, "ls", xpanesSpeedy, ""},
		{xpanesOptions{speedy: 2}, "ls", xpanesSpeedyClose, ""},
		{xpanesOptions{ssh: true}, "ssh -- {}", xpanesSpeedy, ""},
		{xpanesOptions{ssh: true, speedy: 2}, "ssh -- {}", xpanesSpeedyClose, ""},
		{xpanesOptions{speedy: 1}, "", 0, "Add -c or --ssh"},
		{xpanesOptions{speedy: 3}, "ls", 0, "one or two times"},
	}
	for _, c := range cases {
		got, err := xpanesSpeedyMode(c.o, c.command)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%+v: err = %v, want %q", c.o, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%+v: %d, %v; want %d", c.o, got, err, c.want)
		}
	}
}

// -s and -ss parse as speedy mode, and the session has only its long form.
func TestXpanesFlags(t *testing.T) {
	cmd := newXpanesCommand()
	if err := cmd.ParseFlags([]string{"-ss", "--interval", "0.25", "--session", "work"}); err != nil {
		t.Fatal(err)
	}
	f := cmd.Flags()
	if n, _ := f.GetCount("speedy"); n != 2 {
		t.Fatalf("-ss = %d", n)
	}
	if v, _ := f.GetFloat64("interval"); v != 0.25 {
		t.Fatalf("--interval = %v", v)
	}
	if s, _ := f.GetString("session"); s != "work" {
		t.Fatalf("--session = %q", s)
	}
	if f.ShorthandLookup("s").Name != "speedy" {
		t.Fatal("-s is not speedy mode")
	}
}
