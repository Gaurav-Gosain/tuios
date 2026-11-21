package app

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

type clipboardTool struct {
	name     string
	copyCmd  []string
	pasteCmd []string
}

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

func (t *clipboardTool) Write(text string) error {
	cmd := exec.Command(t.copyCmd[0], t.copyCmd[1:]...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

const nativePasteTimeout = 2 * time.Second

func (t *clipboardTool) Read() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), nativePasteTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, t.pasteCmd[0], t.pasteCmd[1:]...).Output()
	return string(out), err
}

func hostTerminalLacksOSC52(getenv func(string) string) bool {
	return getenv("VTE_VERSION") != "" || getenv("TERM_PROGRAM") == "Apple_Terminal"
}

// ShouldUseNativeClipboard is the single decision point for the fallback. It is
// true only when a native tool is reachable AND the human is either at a
// terminal without OSC 52 on this box or on a loopback SSH session, which is
// the same person whose clipboard is this machine's.
func ShouldUseNativeClipboard(loopback bool) bool {
	return shouldUseNativeClipboard(detectClipboardToolEnv{
		getenv:   os.Getenv,
		lookPath: hasExecutable,
	}, hostTerminalLacksOSC52(os.Getenv), loopback)
}

func shouldUseNativeClipboard(env detectClipboardToolEnv, hostLacksOSC52, loopback bool) bool {
	if !hostLacksOSC52 && !loopback {
		return false
	}
	return detectClipboardTool(env) != nil
}
