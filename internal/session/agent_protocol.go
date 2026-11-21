package session

import (
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/agentproto"
)

func (d *Daemon) markProtocolPane(windowID, protocol string) {
	d.protocolPanes.Store(windowID, protocol)
}

func (d *Daemon) unmarkProtocolPane(windowID string) {
	d.protocolPanes.Delete(windowID)
}

func (d *Daemon) paneProtocol(windowID string) string {
	if v, ok := d.protocolPanes.Load(windowID); ok {
		p, _ := v.(string)
		return p
	}
	return ""
}

func (d *Daemon) agentProtoExecutable() (string, error) {
	if d.agentProtoExe != nil {
		return d.agentProtoExe()
	}
	return os.Executable()
}

func checkProtocol(protocol string) *verbError {
	if agentproto.CheckProtocol(protocol) != nil {
		return invalidParam("protocol", "protocol: "+echoName(protocol)+" is not a protocol tuios speaks", agentproto.Protocols...)
	}
	return nil
}

func protocolAgentArgv(protocol string, base, extra []string) []string {
	argv := slices.Clone(base)
	if protocol == agentproto.ProtocolCodex && !slices.Contains(argv[1:], "app-server") && !slices.Contains(extra, "app-server") {
		argv = append(argv, "app-server")
	}
	return append(argv, extra...)
}

func (d *Daemon) protocolArgv(protocol, harness string, agentArgv []string) ([]string, *verbError) {
	if verr := checkProtocol(protocol); verr != nil {
		return nil, verr
	}
	exe, err := d.agentProtoExecutable()
	if err != nil {
		return nil, hintedVerbError(ErrVerbInternal, "could not find the tuios binary to run the protocol pane: "+err.Error(), nil)
	}
	argv := agentArgv
	out := []string{exe, "agent-proto", "--protocol", protocol}
	if harness = strings.TrimSpace(harness); harness != "" {
		out = append(out, "--harness", harness)
	}
	if v := d.acpVersion.Load(); protocol == agentproto.ProtocolACP && v > 0 {
		out = append(out, "--acp-version", strconv.Itoa(int(v)))
	}
	out = append(out, "--")
	return append(out, argv...), nil
}
