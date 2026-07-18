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
}

// VerbCallError is returned by VerbClient.Call when the daemon answers with an
// error envelope. It exposes the stable string code alongside the message.
type VerbCallError struct {
	Code    string
	Message string
}

func (e *VerbCallError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s (%s)", e.Message, e.Code)
}

// DialVerbClient connects to the running daemon's socket for JSON verb calls.
func DialVerbClient() (*VerbClient, error) {
	socketPath, err := GetSocketPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get socket path: %w", err)
	}
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	return &VerbClient{conn: conn, r: bufio.NewReader(conn)}, nil
}

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
		return nil, &VerbCallError{Code: resp.Error.Code, Message: resp.Error.Message}
	}

	rawResult, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to re-encode result: %w", err)
	}
	return rawResult, nil
}
