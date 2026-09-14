package app

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Native system clipboard fallback.
//
// TUIOS normally reaches the clipboard through OSC 52, which asks the user's
// terminal to carry the text. Terminals built on VTE (GNOME Terminal, Ptyxis)
// never implement it, so copy and paste silently fail there. When this process
// can reach a native clipboard tool (wl-clipboard on Wayland, xclip or xsel on
// X11, pbcopy/pbpaste on macOS), tuios talks to the system clipboard directly
// instead, and OSC 52 stays the path for a client whose clipboard lives
// somewhere else.
//
// Where the clipboard lives decides who may touch it. A plain local TUI is the
// person at this box. A loopback SSH session is the same person on the same
// box, so the server's native clipboard is still theirs. A remote SSH peer or
// a browser tab is someone else, whose clipboard this process must not reach;
// OSC 52 is how their terminal carries it.

type clipboardTool struct {
	name     string
	copyCmd  []string
	pasteCmd []string
}

// detectClipboardToolEnv is the environment a clipboard decision is made from.
// Both fields are injectable so the whole decision table becomes a plain test
// with no compositor and no host environment leaking in.
type detectClipboardToolEnv struct {
	getenv   func(string) string
	lookPath func(string) bool
}

// DetectClipboardTool finds a native clipboard tool for this session, or nil
// when none is reachable (a headless box, or no helper installed). Detection
// order is Wayland, then X11, then macOS.
func DetectClipboardTool() *clipboardTool {
	return detectClipboardTool(detectClipboardToolEnv{
		getenv:   os.Getenv,
		lookPath: hasExecutable,
	})
}

func hasExecutable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func detectClipboardTool(env detectClipboardToolEnv) *clipboardTool {
	getenv, lp := env.getenv, env.lookPath
	// Wayland: the runtime dir and the display socket both have to be named
	// for the tool to reach the compositor, so a tool on PATH with neither is
	// not a usable route.
	if getenv("XDG_RUNTIME_DIR") != "" && getenv("WAYLAND_DISPLAY") != "" {
		if lp("wl-copy") && lp("wl-paste") {
			return &clipboardTool{
				name:     "wl-clipboard",
				copyCmd:  []string{"wl-copy"},
				pasteCmd: []string{"wl-paste", "-n"},
			}
		}
	}
	if getenv("DISPLAY") != "" {
		if lp("xclip") {
			return &clipboardTool{
				name:     "xclip",
				copyCmd:  []string{"xclip", "-selection", "clipboard"},
				pasteCmd: []string{"xclip", "-selection", "clipboard", "-o"},
			}
		}
		if lp("xsel") {
			return &clipboardTool{
				name:     "xsel",
				copyCmd:  []string{"xsel", "--clipboard", "--input"},
				pasteCmd: []string{"xsel", "--clipboard", "--output"},
			}
		}
	}
	if lp("pbcopy") && lp("pbpaste") {
		return &clipboardTool{
			name:     "pbcopy/pbpaste",
			copyCmd:  []string{"pbcopy"},
			pasteCmd: []string{"pbpaste"},
		}
	}
	return nil
}

// Write sends text to the native clipboard. The tool owns the data: wl-copy
// forks a keeper that holds the selection until it is replaced, which is the
// semantics wanted, so the write is started and reaped in the background
// rather than waited on (waiting would block until the selection changes).
func (t *clipboardTool) Write(text string) error {
	cmd := exec.Command(t.copyCmd[0], t.copyCmd[1:]...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the keeper so it does not linger as a zombie
	return nil
}

// nativePasteTimeout bounds a native clipboard read. A hung xclip (the X
// server gone, a stalled selection) would otherwise leave the paste pending
// forever; on timeout the read fails and the caller reports that nothing came
// back rather than pretending an empty paste succeeded.
const nativePasteTimeout = 2 * time.Second

// Read returns the current native clipboard contents.
func (t *clipboardTool) Read() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), nativePasteTimeout)
	defer cancel()
	// wl-paste -n suppresses the trailing newline it otherwise adds; xclip -o
	// and pbpaste do not add one, so a selection that genuinely ends in a
	// newline keeps it.
	out, err := exec.CommandContext(ctx, t.pasteCmd[0], t.pasteCmd[1:]...).Output()
	return string(out), err
}

// hostTerminalIsVTE reports whether the outer terminal is VTE-based. VTE is the
// family that never answers OSC 52, so it is where the native route earns its
// keep; on a terminal that does answer, OSC 52 already works and spawning a
// helper would only add a duplicate clipboard-manager entry per selection.
func hostTerminalIsVTE() bool {
	return os.Getenv("VTE_VERSION") != ""
}

// ShouldUseNativeClipboard is the single decision point for the fallback. It is
// true only when a native tool is reachable AND the human is either at a VTE
// terminal on this box or on a loopback SSH session, which is the same person
// whose clipboard is this machine's.
func ShouldUseNativeClipboard(loopback bool) bool {
	return shouldUseNativeClipboard(detectClipboardToolEnv{
		getenv:   os.Getenv,
		lookPath: hasExecutable,
	}, hostTerminalIsVTE(), loopback)
}

func shouldUseNativeClipboard(env detectClipboardToolEnv, hostIsVTE, loopback bool) bool {
	// A loopback SSH session is the same human on the same box: the server
	// process cannot see the client's VTE_VERSION, but the native clipboard is
	// still theirs, so a loopback session is treated as VTE-hosted here.
	if !hostIsVTE && !loopback {
		return false
	}
	return detectClipboardTool(env) != nil
}
