package app

import (
	"context"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	pasteCmd []string // e.g. {"wl-paste", "-n"}
}

// detectClipboardToolEnv is the environment a clipboard decision is made from.
// Both fields are injectable so the whole decision table (Wayland vs X11 vs
// macOS, tool present or not) becomes a plain table test with no compositor.
type detectClipboardToolEnv struct {
	getenv   func(string) string
	lookPath func(string) bool
}

// DetectClipboardTool finds a native clipboard tool for the current session.
// Returns nil when none is available (headless server, no clipboard binaries).
// Detection order: Wayland (wl-clipboard) → X11 (xclip, xsel) → macOS (pbcopy).
func DetectClipboardTool() *clipboardTool {
	return detectClipboardTool(detectClipboardToolEnv{os.Getenv, lookPath})
}

func detectClipboardTool(env detectClipboardToolEnv) *clipboardTool {
	getenv, lp := env.getenv, env.lookPath
	// Wayland. XDG_RUNTIME_DIR + WAYLAND_DISPLAY must both be set for the
	// socket to be reachable; checking the env guards against a tool present
	// but unusable.
	if getenv("XDG_RUNTIME_DIR") != "" && getenv("WAYLAND_DISPLAY") != "" {
		if lp("wl-copy") && lp("wl-paste") {
			return &clipboardTool{name: "wl-clipboard", copyCmd: []string{"wl-copy"}, pasteCmd: []string{"wl-paste", "-n"}}
		}
	}
	// X11.
	if getenv("DISPLAY") != "" {
		if lp("xclip") {
			return &clipboardTool{name: "xclip", copyCmd: []string{"xclip", "-selection", "clipboard"}, pasteCmd: []string{"xclip", "-selection", "clipboard", "-o"}}
		}
		if lp("xsel") {
			return &clipboardTool{name: "xsel", copyCmd: []string{"xsel", "--clipboard", "--input"}, pasteCmd: []string{"xsel", "--clipboard", "--output"}}
		}
	}
	// macOS.
	if lp("pbcopy") && lp("pbpaste") {
		return &clipboardTool{name: "pbcopy/pbpaste", copyCmd: []string{"pbcopy"}, pasteCmd: []string{"pbpaste"}}
	}
	return nil
}

// Write sends text to the native clipboard. The tool owns the data: wl-copy
// forks a keeper process that holds the selection until it is replaced, which
// is exactly the semantics we want (the text stays available after we exit).
//
// The write itself is reported: a keeper that died before consuming stdin
// (compositor gone mid-copy) surfaces as an error from the pipe instead of
// being lost. The process is not waited on — waiting would block until the
// keeper is replaced, which for a copy that nobody pastes is effectively
// forever — so the reaping stays in a goroutine, and a write only blocks while
// the text is larger than the pipe buffer and the keeper is slow to consume
// it, which is fine on the Cmd goroutine the write runs on.
func (t *clipboardTool) Write(text string) error {
	cmd := exec.Command(t.copyCmd[0], t.copyCmd[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_, werr := io.WriteString(stdin, text)
	cerr := stdin.Close()
	go cmd.Wait() // reap the keeper so it does not become a zombie
	if werr != nil {
		return werr
	}
	return cerr
}

// pasteTimeout bounds a clipboard read. A hung xclip (X server gone, selinux
// stall) would otherwise leave the paste pending forever; with the timeout the
// read fails and the caller falls back to OSC 52.
const pasteTimeout = 2 * time.Second

// Read returns the current native clipboard contents. Best-effort: an empty
// clipboard or a failed read yields an empty string.
func (t *clipboardTool) Read() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pasteTimeout)
	defer cancel()
	// wl-paste -n suppresses the trailing newline wl-paste adds by default;
	// xclip -o and pbpaste do not add one, so a selection genuinely ending in
	// a newline keeps it instead of losing it to a TrimSuffix.
	out, err := exec.CommandContext(ctx, t.pasteCmd[0], t.pasteCmd[1:]...).Output()
	return string(out), err
}

// nativeClipboardTool is the single decision point for the clipboard fallback:
// it returns the native tool to use for this session, or nil when tuios must
// go through OSC 52 alone. The paste path (RequestHostPaste) and the write
// path (WriteClipboard) both call it once, on the Update loop, and hand the
// result to their Cmd, so the goroutine only ever sees a plain tool pointer
// and never touches the model.
//
// The native tool is reached only when ALL of these hold:
//  1. the session is local — not SSH, not a browser client: under tuios ssh /
//     tuios-web the human is elsewhere, and WAYLAND_DISPLAY / DISPLAY describe
//     the operator's desktop, so a native read or write would move data
//     between two people,
//  2. the config option is on,
//  3. the host terminal is VTE (the only family that never implements OSC 52;
//     everywhere else OSC 52 already works and spawning wl-copy/xclip would
//     just duplicate entries in the clipboard manager on every selection),
//  4. a tool is actually reachable.
func (m *OS) nativeClipboardTool() *clipboardTool {
	if m.IsSSHMode || m.BrowserClient {
		return nil
	}
	return nativeClipboardToolFor(detectClipboardToolEnv{os.Getenv, lookPath}, config.HostIsVTE(), m.Settings.ClipboardLocalFallback)
}

// nativeClipboardToolFor is nativeClipboardTool with the environment, the host
// detection and the setting injected, so the VTE/setting/tool half of the
// decision stays a table test with no compositor and no real env.
func nativeClipboardToolFor(env detectClipboardToolEnv, hostIsVTE, fallbackEnabled bool) *clipboardTool {
	if !fallbackEnabled || !hostIsVTE {
		return nil
	}
	return detectClipboardTool(env)
}

func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
