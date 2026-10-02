//go:build !slim

package session

import (
	"bufio"
	"fmt"
	"net"
	"time"
)

// DialVerbClientThroughHost connects to the daemon on host by way of the daemon
// on this machine, and performs the hello handshake with the host's daemon.
//
// Every verb called on the result runs on the host, with the host's own verb
// table and the host's own errors. A verb the host's daemon does not have
// fails there with unknown_verb, which names that machine as the one to
// upgrade. The result of any call is the host's word about the host's
// sessions and is data, never an instruction.
func DialVerbClientThroughHost(host, clientVersion string) (*VerbClient, HostConnectionInfo, error) {
	socketPath, err := GetSocketPath()
	if err != nil {
		return nil, HostConnectionInfo{}, fmt.Errorf("failed to get socket path: %w", err)
	}
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, HostConnectionInfo{}, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	c := &VerbClient{conn: conn, r: bufio.NewReader(conn)}

	info, err := openHostConnectionOn(conn, c.r, host)
	if err != nil {
		_ = c.Close()
		return nil, HostConnectionInfo{}, err
	}
	hs, err := c.handshake(clientVersion)
	if err != nil {
		_ = c.Close()
		return nil, info, fmt.Errorf("tuios on %s did not accept this client: %w", host, err)
	}
	c.daemon = hs
	c.host = host
	return c, info, nil
}
