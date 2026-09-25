//go:build darwin || dragonfly || freebsd || linux || solaris || aix

package main

import (
	"os"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// withoutHardTabs stops bubbletea from moving the cursor with HT, and returns
// the function that puts the terminal back the way it was.
//
// bubbletea's renderer moves the cursor across unchanged cells with a hard tab
// whenever the tty's TABDLY is TAB0, which is the default everywhere. tmux 3.6
// and later record a tab that crosses only blank cells as a tab cell, so that
// copy mode can hand the tab back, and that cell is written without the
// background the blanks had. A panel drawn on a background whose blanks a tab
// crossed then shows a hole of terminal ground: two dark cells beside the Inbox
// title was this. The bytes were right, which is why every emulator but tmux
// drew them right.
//
// bubbletea reads the setting from the tty state it saves before raw mode, and
// has no option for it, so the state it saves says TAB3. Raw mode turns output
// processing off, so TAB3 expands nothing while the program runs; the original
// state comes back when it exits. The renderer then moves with CUF, which is
// the same few bytes.
func withoutHardTabs() func() {
	fd := os.Stdin.Fd()
	if !term.IsTerminal(fd) {
		return func() {}
	}
	orig, err := term.GetState(fd)
	if err != nil || orig.Oflag&unix.TABDLY != unix.TAB0 {
		return func() {}
	}
	changed := *orig
	changed.Oflag = changed.Oflag&^unix.TABDLY | unix.TAB3
	if err := term.SetState(fd, &changed); err != nil {
		return func() {}
	}
	return func() { _ = term.SetState(fd, orig) }
}
