//go:build slim

package session

import (
	"context"
	"io"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/edition"
)

// tuios-slim opens no pane on another machine, so every pane is local.
// remotePane is a stub the core code can test a pane against: nothing ever
// makes one, so each type check is false and no method below runs.
type remotePane struct {
	host string
}

func (p *remotePane) Read([]byte) (int, error)  { return 0, io.EOF }
func (p *remotePane) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (p *remotePane) Resize(int, int) error     { return nil }
func (p *remotePane) Close() error              { return nil }
func (p *remotePane) Cwd() (string, bool)       { return "", false }
func (p *remotePane) askCwd()                   {}
func (p *remotePane) linkState() (string, time.Time) {
	return "", time.Time{}
}

func (p *remotePane) pasteImage(context.Context, []byte) (string, error) {
	return "", edition.Missing("A window on another machine")
}

// hostLinkFact is one remote pane's link state for a snapshot.
type hostLinkFact struct {
	state string
	until int64
}

func (s *Session) liveHostLinks() map[string]hostLinkFact { return nil }

func (s *Session) openRemotePaneFor(string, string, int, int, string, []string) (paneIO, error) {
	return nil, edition.Missing("A window on another machine")
}

// DialVerbClientThroughHost: tuios-slim has no links to other machines.
func DialVerbClientThroughHost(string, string) (*VerbClient, HostConnectionInfo, error) {
	return nil, HostConnectionInfo{}, edition.Missing("A connection through another machine")
}

// ConnectThroughHost: tuios-slim has no links to other machines.
func (c *TUIClient) ConnectThroughHost(string, string, int, int, *ClientCapabilities) (HostConnectionInfo, error) {
	return HostConnectionInfo{}, edition.Missing("A session on another machine")
}
