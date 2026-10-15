package tuie2e

import (
	"os"
	"path/filepath"
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

	// With the key forwarded, the pane's reply moves focus. A shell plays the
	// plugin: it reads the forwarded key and answers with the focus request.
	// The key it read is kept, because tuios moving focus on the key itself
	// would also pass the focus check below.
	keyFile := filepath.Join(base, "nav-key")
	// The marker is printed once the terminal is raw, so the key cannot
	// arrive while the line discipline would still hold it.
	if err := term.SendKeys(`stty raw -echo; printf 'NAV-%s-READY\r\n' $((3*7)); dd bs=64 count=1 of=`+keyFile+` 2>/dev/null; printf '\033]7777;tuios-nvim-navigator;focus;left\007'; stty sane`, tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("NAV-21-READY", shellTimeout); err != nil {
		t.Fatalf("the pane never got ready for the key: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("\x1b[1;3D"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, after = focusedLayout(t, base, session); after != before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("focus stayed on %s after a forwarded key and the pane's focus request", before)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if key, err := os.ReadFile(keyFile); err != nil || len(key) == 0 {
		t.Fatalf("the focus key never reached the active pane (read %q, %v)", key, err)
	}
}
