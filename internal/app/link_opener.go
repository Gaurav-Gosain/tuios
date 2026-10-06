package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/anmitsu/go-shlex"
)

// The program that opens a web link, and how a failure gets back to the user.
//
// The order is the one a person can steer from the outside:
//
//  1. appearance.link_opener, when it is set.
//  2. $BROWSER, the variable every Unix tool reads. A remote editor sets it to
//     a helper that opens the page on the machine the person sits at, so it
//     wins over the ssh check below.
//  3. The desktop's own opener: open on macOS, the protocol handler on
//     Windows, wslview under WSL, xdg-open elsewhere.
//
// Step 3 is skipped when the desktop is plainly not here: under ssh, or on a
// Linux box with no display. xdg-open on such a box opens a browser in front of
// nobody, or falls back to a text browser on tuios's own terminal. The address
// goes on the clipboard instead, and the outer terminal can still open it from
// the OSC 8 link tuios draws around it.

// errNoDesktop says there is no desktop on this machine to open a link with.
var errNoDesktop = errors.New("no desktop on this machine")

// linkOpenWait is how long an opener is watched for a failure. xdg-open and
// open hand the address over and exit within a moment, so a non-zero exit in
// this window is a failure the user should hear about. An opener that runs
// longer (a browser started directly) is left alone.
const linkOpenWait = 3 * time.Second

// linkOpenFailedMsg reports an opener that exited with an error.
type linkOpenFailedMsg struct {
	URL    string
	Detail string
}

// linkOpenerArgv returns the argv that opens rawURL, or errNoDesktop.
func linkOpenerArgv(setting, rawURL string) ([]string, error) {
	if cmd := strings.TrimSpace(setting); cmd != "" {
		return openerCommand(cmd, rawURL)
	}
	if browser := strings.TrimSpace(os.Getenv("BROWSER")); browser != "" {
		// $BROWSER may list several commands separated by colons. The first
		// one is the user's choice.
		first, _, _ := strings.Cut(browser, ":")
		if strings.TrimSpace(first) != "" {
			return openerCommand(first, rawURL)
		}
	}
	if viewerIsRemote() {
		return nil, errNoDesktop
	}
	switch runtime.GOOS {
	case "darwin":
		return []string{"open", rawURL}, nil
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", rawURL}, nil
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		if _, err := exec.LookPath("wslview"); err == nil {
			return []string{"wslview", rawURL}, nil
		}
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return nil, errNoDesktop
	}
	return []string{"xdg-open", rawURL}, nil
}

// openerCommand splits a command the way a shell splits words, without running
// a shell, and puts the address where %s is, or last when there is no %s.
func openerCommand(cmd, rawURL string) ([]string, error) {
	argv, err := shlex.Split(cmd, true)
	if err != nil {
		return nil, fmt.Errorf("read the link opener %q: %w", cmd, err)
	}
	if len(argv) == 0 {
		return nil, errors.New("the link opener is empty")
	}
	placed := false
	for i, a := range argv {
		if strings.Contains(a, "%s") {
			argv[i] = strings.ReplaceAll(a, "%s", rawURL)
			placed = true
		}
	}
	if !placed {
		argv = append(argv, rawURL)
	}
	return argv, nil
}

// viewerIsRemote reports whether the person is on another machine, reached
// over ssh. tuios run inside an ssh session has no way to start a program
// on the machine the person sits at.
func viewerIsRemote() bool {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

// startLinkOpener starts the opener and returns the command that watches it.
// The start error is returned at once: a missing program is the commonest
// failure and the user hears about it on the same frame.
func startLinkOpener(argv []string, rawURL string) (tea.Cmd, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{buf: &stderr, max: 512}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return func() tea.Msg {
		select {
		case err := <-done:
			if err == nil {
				return nil
			}
			detail := strings.TrimSpace(stderr.String())
			if detail == "" {
				detail = err.Error()
			}
			return linkOpenFailedMsg{URL: rawURL, Detail: detail}
		case <-time.After(linkOpenWait):
			return nil
		}
	}, nil
}

// limitedWriter keeps the first max bytes written to it and drops the rest,
// so an opener that prints a great deal cannot grow the buffer.
type limitedWriter struct {
	buf *bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		w.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// handleLinkOpenFailed tells the user an opener failed after it started.
func (m *OS) handleLinkOpenFailed(msg linkOpenFailedMsg) tea.Cmd {
	m.LogError("The link opener failed for %s: %s", msg.URL, msg.Detail)
	m.ShowNotification("The link did not open. The address is on your clipboard.",
		"error", m.Settings.NotificationDuration)
	return tea.SetClipboard(msg.URL)
}

// ErrNoDesktop says this machine has no desktop to open a web link with: the
// person reached it over ssh, or it has no display.
var ErrNoDesktop = errNoDesktop

// OpenWebLink opens a web link the way a click in tuios does, for a command
// that runs outside the client: setting (appearance.link_opener), then
// $BROWSER, then the desktop's own opener. It waits a moment for the opener to
// fail and returns that failure. It returns ErrNoDesktop when there is no
// desktop here, and refuses an address with a scheme a click would refuse.
func OpenWebLink(setting, rawURL string) error {
	if !linkTextClean(rawURL) || !linkOpenableScheme(rawURL) {
		return errors.New("tuios does not open that kind of address")
	}
	argv, err := linkOpenerArgv(setting, rawURL)
	if err != nil {
		return err
	}
	watch, err := startLinkOpener(argv, rawURL)
	if err != nil {
		return err
	}
	if failed, ok := watch().(linkOpenFailedMsg); ok {
		return fmt.Errorf("%s failed: %s", argv[0], failed.Detail)
	}
	return nil
}
