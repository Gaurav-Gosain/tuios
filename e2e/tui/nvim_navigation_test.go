package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func TestNvimNavigationOSCNeedsActivePane(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nnvim_navigation = true\n")
	const session = "nvim-osc"
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}})
	killDaemon(t, base)
	waitBoot(t, term)
	for range 2 {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 2, "nvim OSC setup")
	enableTiling(t, term)
	enterTerminalMode(t, term)

	_, before := focusedLayout(t, base, session)
	if err := term.SendKeys(`printf '\033]7777;tuios-nvim-navigator;focus;left\007'`, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	_, after := focusedLayout(t, base, session)
	if after != before {
		t.Fatalf("inactive pane moved focus from %s to %s", before, after)
	}
}

func TestNvimNavigationOSCNeedsForwardedKey(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nnvim_navigation = true\n")
	const session = "nvim-osc-active"
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", session}})
	killDaemon(t, base)
	waitBoot(t, term)
	for range 2 {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 2, "nvim OSC setup")
	enableTiling(t, term)
	enterTerminalMode(t, term)

	_, before := focusedLayout(t, base, session)
	if err := term.SendKeys(`printf '\033]7777;tuios-nvim-navigator;state;active\007'`, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if err := term.SendKeys(`printf '\033]7777;tuios-nvim-navigator;focus;left\007'`, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	_, after := focusedLayout(t, base, session)
	if after != before {
		t.Fatalf("active pane moved focus from %s to %s without a forwarded key", before, after)
	}
}
