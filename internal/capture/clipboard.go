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

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

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
		return "", errfmt.Errorf("%s", r.Reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch r.Tool {
	case "wl-copy":
		cmd = childproc.CommandContext(ctx, "wl-copy", "-t", mediaType)
		cmd.Stdin = strings.NewReader(string(data))
	case "xclip":
		cmd = childproc.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", mediaType, "-i")
		cmd.Stdin = strings.NewReader(string(data))
		detach(cmd)
	case "osascript":
		if mediaType != "image/png" {
			return "", errfmt.Errorf("this machine can only copy PNG images")
		}
		script := fmt.Sprintf(`set the clipboard to (read (POSIX file %q) as «class PNGf»)`, path)
		cmd = childproc.CommandContext(ctx, "osascript", "-e", script)
	case "powershell":
		if mediaType != "image/png" {
			return "", errfmt.Errorf("this machine can only copy PNG images")
		}
		script := fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms;`+
				`[System.Windows.Forms.Clipboard]::SetImage([System.Drawing.Image]::FromFile(%q))`, path)
		cmd = childproc.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script)
	default:
		return "", errfmt.Errorf("no clipboard helper")
	}

	if err := cmd.Run(); err != nil {
		return "", errfmt.Errorf("%s could not copy: %s", r.Tool, err.Error())
	}
	return r.Tool, nil
}
