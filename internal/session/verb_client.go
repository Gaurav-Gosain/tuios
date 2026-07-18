package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// VerbClient is a minimal client for the line-delimited JSON verb protocol. It
// is the counterpart the tuios CLI uses to drive the daemon's control surface.
// One call is in flight at a time; it is safe for sequential use from a single
// goroutine (the callMu guards against accidental concurrent Call).
type VerbClient struct {
	conn   net.Conn
	r      *bufio.Reader
	nextID int
	callMu sync.Mutex

	// daemon is what the daemon reported during the hello handshake, or nil
	// when no handshake was performed.
	daemon *DaemonHandshake
}

// VerbCallError is returned by VerbClient.Call when the daemon answers with an
// error envelope. It exposes the stable string code and the structured hint
// alongside the message, so a caller can render the remedy the daemon named.
type VerbCallError struct {
	Code    string
	Message string
	Hint    *VerbHint
}

func (e *VerbCallError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s (%s)", e.Message, e.Code)
}

// DialVerbClient connects to the running daemon's socket for JSON verb calls,
// without announcing a client version.
func DialVerbClient() (*VerbClient, error) {
	return DialVerbClientAs("")
}

// DialVerbClientAs connects to the running daemon's socket and performs the
// hello handshake, announcing clientVersion.
//
// A daemon too old to speak the JSON protocol fails here with a
// *ProtocolMismatchError naming both versions and the command that fixes it,
// rather than surfacing later as an unexplained connection reset. A daemon that
// speaks the protocol but predates the handshake verb connects normally.
func DialVerbClientAs(clientVersion string) (*VerbClient, error) {
	socketPath, err := GetSocketPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get socket path: %w", err)
	}
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	c := &VerbClient{conn: conn, r: bufio.NewReader(conn)}

	hs, err := c.handshake(clientVersion)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	c.daemon = hs
	return c, nil
}

// Daemon returns what the daemon reported during the handshake. Its Protocol is
// zero when the daemon predates the handshake verb. It is nil only for a client
// built without dialing.
func (c *VerbClient) Daemon() *DaemonHandshake { return c.daemon }

// Close closes the underlying connection.
func (c *VerbClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Call sends a verb request with the given params (may be nil) and returns the
// raw result object. A daemon error envelope is returned as a *VerbCallError.
func (c *VerbClient) Call(verb string, params any) (json.RawMessage, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()

	c.nextID++
	id := c.nextID

	var rawParams json.RawMessage
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("failed to encode params: %w", err)
		}
		rawParams = p
	}

	req := verbRequest{
		ID:     json.RawMessage(fmt.Sprintf("%d", id)),
		Verb:   verb,
		Params: rawParams,
	}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}
	line = append(line, '\n')

	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.conn.Write(line); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	respLine, err := c.r.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var resp verbResponse
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if resp.Error != nil {
		return nil, &VerbCallError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
			Hint:    resp.Error.Hint,
		}
	}

	rawResult, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to re-encode result: %w", err)
	}
	return rawResult, nil
}
