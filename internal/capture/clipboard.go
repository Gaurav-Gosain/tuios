package capture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Native image clipboard.
//
// OSC 52 is text only, so an image can never ride the escape stream. Putting a
// PNG on a clipboard therefore means running a helper on the machine that owns
// the clipboard, and the rule learned from the PR #133 review is that only a
// process actually sitting on the user's machine may do it. The CLI qualifies:
// it reaches the daemon over a unix socket, so the two are the same machine.
// An SSH client attached to a tuios ssh server does not, and never calls this.
//
// Detection is gated on the display environment and cached, the helper is
// given a bounded deadline, and a failure degrades to the file path plus a
// notice. It is never an error state: the file is already written and the
// capture already succeeded.

// ClipboardRoute is what a copy would do on this machine.
type ClipboardRoute struct {
	// Available reports whether an image copy can be attempted at all.
	Available bool
	// Tool names the helper, for the reason line when it is missing.
	Tool string
	// Reason says why no route exists, in plain words for a person to read.
	Reason string
}

var (
	routeOnce sync.Once
	route     ClipboardRoute
)

// ImageRoute reports whether this process can put an image on the clipboard.
// It probes once: a PATH lookup per capture would be waste, and the answer
// cannot change under a running process in any way that matters.
func ImageRoute() ClipboardRoute {
	routeOnce.Do(func() { route = detectImageRoute() })
	return route
}

// clipboardTimeout bounds a helper. wl-copy and xclip fork a server process
// that holds the selection and does not exit, so the wait is on the write
// finishing, not on the tool.
const clipboardTimeout = 5 * time.Second

func detectImageRoute() ClipboardRoute {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("osascript"); err == nil {
			return ClipboardRoute{Available: true, Tool: "osascript"}
		}
		return ClipboardRoute{Reason: "This machine has no osascript to copy an image with."}
	case "windows":
		if _, err := exec.LookPath("powershell"); err == nil {
			return ClipboardRoute{Available: true, Tool: "powershell"}
		}
		return ClipboardRoute{Reason: "This machine has no PowerShell to copy an image with."}
	default:
		// Gate on the display environment: without one there is no clipboard
		// to write to, and running the tool would fail slowly instead of the
		// answer being known now.
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if _, err := exec.LookPath("wl-copy"); err == nil {
				return ClipboardRoute{Available: true, Tool: "wl-copy"}
			}
			return ClipboardRoute{Reason: "Install wl-clipboard to copy an image on Wayland."}
		}
		if os.Getenv("DISPLAY") != "" {
			if _, err := exec.LookPath("xclip"); err == nil {
				return ClipboardRoute{Available: true, Tool: "xclip"}
			}
			return ClipboardRoute{Reason: "Install xclip to copy an image on X11."}
		}
		return ClipboardRoute{Reason: "This session has no display, so it has no clipboard."}
	}
}

// CopyImage puts the bytes of an image on the clipboard. It returns the tool
// that did it, or an error the caller turns into a notice.
func CopyImage(path string, data []byte, mediaType string) (string, error) {
	r := ImageRoute()
	if !r.Available {
		return "", fmt.Errorf("%s", r.Reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch r.Tool {
	case "wl-copy":
		cmd = exec.CommandContext(ctx, "wl-copy", "-t", mediaType)
		cmd.Stdin = strings.NewReader(string(data))
	case "xclip":
		// xclip forks a process that keeps serving the selection after the
		// write returns. Without this it would be killed with its parent and
		// the clipboard would be empty by the time anyone pasted.
		cmd = exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", mediaType, "-i")
		cmd.Stdin = strings.NewReader(string(data))
		cmd.SysProcAttr = detachAttr()
	case "osascript":
		if mediaType != "image/png" {
			return "", fmt.Errorf("this machine can only copy PNG images")
		}
		script := fmt.Sprintf(`set the clipboard to (read (POSIX file %q) as «class PNGf»)`, path)
		cmd = exec.CommandContext(ctx, "osascript", "-e", script)
	case "powershell":
		if mediaType != "image/png" {
			return "", fmt.Errorf("this machine can only copy PNG images")
		}
		script := fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms;`+
				`[System.Windows.Forms.Clipboard]::SetImage([System.Drawing.Image]::FromFile(%q))`, path)
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script)
	default:
		return "", fmt.Errorf("no clipboard helper")
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s could not copy: %s", r.Tool, msg)
	}
	return r.Tool, nil
}
