package session

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// A TUI client attached to a session on another machine.
//
// The client dials its own daemon, asks it for a connection to the host, and
// then runs the ordinary binary attach protocol on that connection with the
// host's daemon: hello, attach, PTY streams, keystrokes. Every byte of the
// session's output is decoded by this process's own emulator and drawn by this
// process's own renderer, with this machine's theme, config and prefix key.
// Nothing about the far side is rendered as anything but a session.
//
// The one thing that differs from a local attach is trust. A daemon on another
// machine is another machine's process, possibly older, possibly newer,
// possibly compromised. Its output goes into an emulator, which is the same
// fence a shell's output gets. Its state pushes describe its own session and
// nothing else. The one message a daemon sends that can make a client do
// something on its own machine is a routed command, and that is where the
// fence below is.

// ConnectThroughHost connects to the daemon on host by way of the daemon on
// this machine, and performs the handshake with it.
//
// The local daemon opens a stream on its link to the host and relays; see
// open-host-connection. From the handshake on, the client is an ordinary
// client of the remote daemon, and its errors say which machine they are
// about.
func (c *TUIClient) ConnectThroughHost(host, version string, width, height int, caps *ClientCapabilities) (HostConnectionInfo, error) {
	socketPath, err := GetSocketPath()
	if err != nil {
		return HostConnectionInfo{}, fmt.Errorf("failed to get socket path: %w", err)
	}
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return HostConnectionInfo{}, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	c.conn = conn
	c.br = nil

	info, err := openHostConnectionOn(conn, c.reader(), host)
	if err != nil {
		_ = conn.Close()
		return HostConnectionInfo{}, err
	}
	c.viaHost = host
	if err := c.handshake(version, width, height, caps); err != nil {
		_ = conn.Close()
		return info, &HostHandshakeError{Host: host, Info: info, Err: err}
	}
	return info, nil
}

// Host is the host this client reached its daemon through, or "" for the
// daemon on this machine.
func (c *TUIClient) Host() string { return c.viaHost }

// HostHandshakeError reports a host whose daemon answered the connection and
// then refused the binary handshake, or could not be understood. It is the
// version skew case for an attach: the link is up, the control protocol
// matched, and the attach protocol did not. The message names the machine.
type HostHandshakeError struct {
	Host string
	Info HostConnectionInfo
	Err  error
}

func (e *HostHandshakeError) Error() string {
	var mismatch *ProtocolMismatchError
	if errors.As(e.Err, &mismatch) {
		return fmt.Sprintf("tuios on %s speaks a different attach protocol. Upgrade tuios on %s or on this machine. %v",
			e.Host, e.Host, e.Err)
	}
	if isConnectionGone(e.Err) {
		return fmt.Sprintf("tuios on %s closed the connection before it answered. Its daemon may have stopped. Run 'tuios hosts' to see the link.", e.Host)
	}
	return fmt.Sprintf("tuios on %s did not accept this client. %v", e.Host, e.Err)
}

func (e *HostHandshakeError) Unwrap() error { return e.Err }

// refuseHostCommand decides whether a routed command that arrived through a
// host may run, and says why not when it may not. A command on a local
// connection is always allowed; the daemon on this machine is trusted the way
// it always was.
func (c *TUIClient) refuseHostCommand(p *RemoteCommandPayload) string {
	if c.viaHost == "" {
		return ""
	}
	if hostCommandAllowed(p) {
		return ""
	}
	what := p.CommandType
	if p.CommandType == "tape_command" {
		what = p.TapeCommand
	}
	return fmt.Sprintf("refused: %s came from host %s and would change this machine, not the session", what, c.viaHost)
}

// hostCommandAllowed is the fence for routed commands from another machine's
// daemon.
//
// The rule: a command may drive the session on screen, because that session
// belongs to the machine that sent the command. It may not read or write a
// file, a setting or a theme on this machine, because those belong to the
// person, and a daemon on a build box has no business with them. Everything
// not named here is refused, so a command a newer remote build adds is refused
// until someone reads what it does.
func hostCommandAllowed(p *RemoteCommandPayload) bool {
	switch p.CommandType {
	case "send_keys":
		return true
	case "list_dock_components", "list_hooks", "refresh_dock":
		return true
	case "tape_command":
		return hostTapeCommandAllowed(tape.CommandType(p.TapeCommand))
	}
	return false
}

func hostTapeCommandAllowed(t tape.CommandType) bool {
	switch t {
	case tape.CommandTypeType, tape.CommandTypeSleep, tape.CommandTypeEnter, tape.CommandTypeSpace,
		tape.CommandTypeBackspace, tape.CommandTypeDelete, tape.CommandTypeTab, tape.CommandTypeEscape,
		tape.CommandTypeUp, tape.CommandTypeDown, tape.CommandTypeLeft, tape.CommandTypeRight,
		tape.CommandTypeHome, tape.CommandTypeEnd, tape.CommandTypeKeyCombo,
		tape.CommandTypeTerminalMode, tape.CommandTypeWindowManagementMode,
		tape.CommandTypeNewWindow, tape.CommandTypeCloseWindow, tape.CommandTypeNextWindow,
		tape.CommandTypePrevWindow, tape.CommandTypeFocusWindow, tape.CommandTypeRenameWindow,
		tape.CommandTypeMinimizeWindow, tape.CommandTypeRestoreWindow,
		tape.CommandTypeToggleTiling, tape.CommandTypeEnableTiling, tape.CommandTypeDisableTiling,
		tape.CommandTypeSnapLeft, tape.CommandTypeSnapRight, tape.CommandTypeSnapFullscreen,
		tape.CommandTypeSwitchWS, tape.CommandTypeMoveToWS, tape.CommandTypeMoveAndFollowWS,
		tape.CommandTypeSplit, tape.CommandTypeFocus, tape.CommandTypeRotateSplit,
		tape.CommandTypeEqualizeSplits, tape.CommandTypePreselect,
		tape.CommandTypeWait, tape.CommandTypeWaitUntilRegex,
		tape.CommandTypeShowNotification, tape.CommandTypeFocusDirection, tape.CommandTypeToggleZoom,
		tape.CommandTypeSmartSplit, tape.CommandTypeCommandPalette, tape.CommandTypeComment:
		return true
	case "ListWindows", "GetSessionInfo", "GetWindow":
		// Read-only queries about the session on screen.
		return true
	}
	// Refused, by name, so the list above is read against it: SetConfig,
	// SetTheme, SetDockbarPosition, SetBorderStyle and Set write this machine's
	// settings; Screenshot, Output, SaveLayout and LoadLayout write or read
	// this machine's files; Source runs a tape file from this machine's disk;
	// the animation toggles change this client's own settings.
	return false
}

// hostCommandFenceNames is the list a test reads to prove the fence names
// every refused command it claims to.
func hostCommandFenceNames() string {
	refused := []tape.CommandType{
		tape.CommandTypeSetConfig, tape.CommandTypeSetTheme, tape.CommandTypeSetDockbarPosition,
		tape.CommandTypeSetBorderStyle, tape.CommandTypeSet, tape.CommandTypeScreenshot,
		tape.CommandTypeOutput, tape.CommandTypeSaveLayout, tape.CommandTypeLoadLayout,
		tape.CommandTypeSource,
	}
	names := make([]string, 0, len(refused))
	for _, r := range refused {
		names = append(names, string(r))
	}
	return strings.Join(names, ",")
}
