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
	panes := xpanesPanes([]string{"a b", "c"}, 1, "echo ITEM-{}; exec sh", "{}", "/bin/zsh")
	want := []string{"env", "TUIOS_XPANES_ITEM=a b", "TUIOS_XPANES_INDEX=1", "sh", "-c", "echo ITEM-'a b'; exec sh"}
	if len(panes) != 2 || !slices.Equal(panes[0].Argv, want) || panes[0].Title != "a b" {
		t.Fatalf("panes = %+v", panes)
	}
	if got := panes[1].Argv; got[2] != "TUIOS_XPANES_INDEX=2" || got[5] != "echo ITEM-c; exec sh" {
		t.Fatalf("second pane argv = %q", got)
	}

	// -n 2 and a placeholder of its own.
	panes = xpanesPanes([]string{"x", "y z", "w"}, 2, "diff %", "%", "")
	if len(panes) != 2 || panes[0].Argv[5] != "diff x 'y z'" || panes[1].Argv[5] != "diff w" {
		t.Fatalf("grouped panes = %+v", panes)
	}
	if panes[0].Argv[1] != "TUIOS_XPANES_ITEM=x y z" {
		t.Fatalf("grouped item = %q", panes[0].Argv[1])
	}

	// No command: the shell, with the item in the environment.
	panes = xpanesPanes([]string{"db1"}, 1, "", "{}", "/bin/zsh")
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
	if n := len(xpanesPanes(items, 2, "", "{}", "sh")); n > xpanesMaxPanes {
		t.Fatalf("%d panes with -n 2", n)
	}
}
