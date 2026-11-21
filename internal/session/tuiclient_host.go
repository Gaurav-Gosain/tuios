package session

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

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
		return HostConnectionInfo{}, errfmt.Errorf("failed to get socket path: %w", err)
	}
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return HostConnectionInfo{}, errfmt.Errorf("failed to connect to daemon: %w", err)
	}
	c.conn = conn
	c.br = nil

	info, err := openHostConnectionAgent(conn, c.reader(), host, c.SSHAuthSock)
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
	if _, ok := errors.AsType[*ProtocolMismatchError](e.Err); ok {
		return fmt.Sprintf("tuios on %s speaks a different attach protocol. Upgrade tuios on %s or on this machine. %v",
			e.Host, e.Host, e.Err)
	}
	if isConnectionGone(e.Err) {
		return fmt.Sprintf("tuios on %s closed the connection before it answered. Its daemon may have stopped. Run 'tuios hosts' to see the link.", e.Host)
	}
	return fmt.Sprintf("tuios on %s did not accept this client. %v", e.Host, e.Err)
}

func (e *HostHandshakeError) Unwrap() error { return e.Err }

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

func hostCommandAllowed(p *RemoteCommandPayload) bool {
	switch p.CommandType {
	case "send_keys":
		return true
	case "list_dock_components", "list_hooks", "refresh_dock", "notice":
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
		tape.CommandTypeArrangePanes, tape.CommandTypeSetMultifocus,
		tape.CommandTypeWait, tape.CommandTypeWaitUntilRegex,
		tape.CommandTypeShowNotification, tape.CommandTypeFocusDirection, tape.CommandTypeToggleZoom,
		tape.CommandTypeSetMasterPosition, tape.CommandTypeSetMasterCount, tape.CommandTypeCycleMasterPosition,
		tape.CommandTypeAddMaster, tape.CommandTypeRemoveMaster, tape.CommandTypeSwapWithMaster,
		tape.CommandTypeFocusMaster,
		tape.CommandTypeSmartSplit, tape.CommandTypeCommandPalette, tape.CommandTypeComment:
		return true
	case "ListWindows", "GetSessionInfo", "GetWindow":
		return true
	}
	return false
}
