package app

import (
	"os"
	"os/exec"
	"strings"
)

// clipboardTool describes a native system clipboard command pair. TUIOS normally
// talks to the clipboard through OSC 52, which requires the user's terminal to
// implement it. Terminals built on VTE (GNOME Terminal, Ptyxis) do not, so the
// copy/paste actions silently fail there. When the process runs on a machine
// with a native clipboard tool (wl-clipboard, xclip, pbcopy), we write to and
// read from the system clipboard directly instead of depending on the terminal.
//
// OSC 52 remains the fallback for remote clients: a client SSH'd into a
// headless box has no native clipboard of its own, and the terminal on the
// other end of the wire is the only channel that can reach the user's real
// clipboard. Local-first, OSC 52 as the safety net.
type clipboardTool struct {
	name     string   // human-readable, for logs/notifications
	copyCmd  []string // e.g. {"wl-copy"}
	pasteCmd []string // e.g. {"wl-paste"}
}

// detectClipboardTool finds a native clipboard tool for the current session.
// Returns nil when none is available (headless server, no clipboard binaries).
// Detection order: Wayland (wl-clipboard) → X11 (xclip, xsel) → macOS (pbcopy).
func DetectClipboardTool() *clipboardTool {
	// Wayland. XDG_RUNTIME_DIR + WAYLAND_DISPLAY must both be set for the
	// socket to be reachable; checking the env guards against a tool present
	// but unusable.
	if os.Getenv("XDG_RUNTIME_DIR") != "" && os.Getenv("WAYLAND_DISPLAY") != "" {
		if lookPath("wl-copy") && lookPath("wl-paste") {
			return &clipboardTool{name: "wl-clipboard", copyCmd: []string{"wl-copy"}, pasteCmd: []string{"wl-paste"}}
		}
	}
	// X11.
	if os.Getenv("DISPLAY") != "" {
		if lookPath("xclip") {
			return &clipboardTool{name: "xclip", copyCmd: []string{"xclip", "-selection", "clipboard"}, pasteCmd: []string{"xclip", "-selection", "clipboard", "-o"}}
		}
		if lookPath("xsel") {
			return &clipboardTool{name: "xsel", copyCmd: []string{"xsel", "--clipboard", "--input"}, pasteCmd: []string{"xsel", "--clipboard", "--output"}}
		}
	}
	// macOS.
	if lookPath("pbcopy") && lookPath("pbpaste") {
		return &clipboardTool{name: "pbcopy/pbpaste", copyCmd: []string{"pbcopy"}, pasteCmd: []string{"pbpaste"}}
	}
	return nil
}

// Write sends text to the native clipboard. The tool owns the data: wl-copy
// forks a keeper process that holds the selection until it is replaced, which
// is exactly the semantics we want (the text stays available after we exit).
// The write is started but not waited on: waiting would block until the keeper
// is replaced, which for a copy that nobody pastes is effectively forever.
// cmd.Start returns as soon as the keeper has the content, and a later copy
// replaces it naturally (wl-clipboard handles the handoff).
func (t *clipboardTool) Write(text string) error {
	cmd := exec.Command(t.copyCmd[0], t.copyCmd[1:]...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Start()
}

// read returns the current native clipboard contents. Best-effort: an empty
// clipboard or a failed read yields an empty string.
func (t *clipboardTool) Read() (string, error) {
	out, err := exec.Command(t.pasteCmd[0], t.pasteCmd[1:]...).Output()
	return strings.TrimSuffix(string(out), "\n"), err
}

// localClipboardAvailable reports whether a native clipboard tool is usable
// right now. Called on every copy/paste so a tool disappearing (session end,
// compositor exit) degrades gracefully back to OSC 52.
func LocalClipboardAvailable() bool {
	return DetectClipboardTool() != nil
}

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
