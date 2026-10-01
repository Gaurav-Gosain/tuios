package herdrcli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// Timeouts for one request. A report must never hold its reporter up, which
// is what herdr's agent hooks rely on. A wait runs as long as its own
// timeout says, and the daemon bounds that.
const (
	reportTimeout  = 2 * time.Second
	requestTimeout = 30 * time.Second
	// agentStartDefault is how long agent start waits for the agent, as in
	// herdr, when --timeout is not given.
	agentStartDefault = 30 * time.Second
	agentStartPoll    = 100 * time.Millisecond
)

// Options are what Main needs from its process.
type Options struct {
	Stdout, Stderr io.Writer
	Getenv         Env
	// Cwd is the directory relative worktree paths are taken from.
	Cwd string
	// Socket is the herdr socket to dial when HERDR_SOCKET_PATH is not set:
	// tuios's own, beside the daemon socket.
	Socket func() (string, error)
}

// Main runs one herdr command line, without the program name, and returns
// the exit code herdr would.
func Main(args []string, o Options) int {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Cwd == "" {
		o.Cwd, _ = os.Getwd()
	}
	c, uerr := Parse(args, o.Getenv, o.Cwd)
	if uerr != nil {
		if uerr.Msg != "" {
			fmt.Fprintln(o.Stderr, uerr.Msg)
		}
		return uerr.Code
	}
	switch c.Output {
	case OutText:
		fmt.Fprintln(o.Stdout, c.Text)
		return 0
	case OutLocal:
		printJSON(o.Stderr, errorResponse(c.ID, "unsupported", c.Text))
		return 1
	}
	path := o.Getenv("HERDR_SOCKET_PATH")
	if path == "" && o.Socket != nil {
		p, err := o.Socket()
		if err != nil {
			fmt.Fprintln(o.Stderr, "herdr: "+err.Error())
			return 1
		}
		path = p
	}
	resp, err := request(path, c)
	if err != nil {
		printJSON(o.Stderr, err)
		return 1
	}
	if c.Output == OutAgentStart && resp["error"] == nil {
		resp = waitAgentStart(path, c, resp)
	}
	if e := resp["error"]; e != nil {
		printJSON(o.Stderr, resp)
		return 1
	}
	switch c.Output {
	case OutOK:
	case OutRead:
		if r, ok := resp["result"].(map[string]any); ok {
			if read, ok := r["read"].(map[string]any); ok {
				if text, ok := read["text"].(string); ok {
					fmt.Fprint(o.Stdout, text)
				}
			}
		}
	default:
		printJSON(o.Stdout, resp)
	}
	return 0
}

func errorResponse(id, code, msg string) map[string]any {
	return map[string]any{"id": id, "error": map[string]any{"code": code, "message": msg}}
}

// printJSON writes v on one line the way herdr's CLI does: serde_json, whose
// maps are sorted by key and which escapes no HTML.
func printJSON(w io.Writer, v any) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return
	}
	_, _ = w.Write(b.Bytes())
}

// connError is a request that could not reach the socket, in herdr's
// server_not_running shape.
type connError map[string]any

func (e connError) Error() string { return fmt.Sprint(map[string]any(e)) }

// request sends one call and reads its answer as a JSON object.
func request(path string, c *Call) (map[string]any, connError) {
	return send(path, c.ID, c.Method, c.Params, deadlineFor(c))
}

func deadlineFor(c *Call) time.Duration {
	switch {
	case c.Report:
		return reportTimeout
	case c.Wait:
		return 0
	}
	return requestTimeout
}

func send(path, id, method string, params map[string]any, timeout time.Duration) (map[string]any, connError) {
	notRunning := func() connError {
		return connError(errorResponse(id, "server_not_running", "no herdr server is running at "+path+"; run `tuios` to start or attach it"))
	}
	if path == "" {
		return nil, notRunning()
	}
	dialTimeout := timeout
	if dialTimeout == 0 {
		dialTimeout = requestTimeout
	}
	conn, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return nil, notRunning()
	}
	defer func() { _ = conn.Close() }()
	if timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	line, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, connError(errorResponse(id, "invalid_request", err.Error()))
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return nil, connError(errorResponse(id, "transport_failed", err.Error()))
	}
	reply, err := bufio.NewReaderSize(conn, 64<<10).ReadBytes('\n')
	if err != nil && (len(reply) == 0 || !errors.Is(err, io.EOF)) {
		return nil, connError(errorResponse(id, "transport_failed", "the herdr socket sent no answer: "+err.Error()))
	}
	dec := json.NewDecoder(bytes.NewReader(reply))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, connError(errorResponse(id, "transport_failed", "the herdr socket sent an answer that is not JSON: "+err.Error()))
	}
	return out, nil
}

// waitAgentStart waits, after agent.start, for the agent in the pane to be
// ready for a prompt, as herdr's agent start does: at rest (idle or done)
// it is ready, blocked is an error, and the wait ends at the timeout. The
// answer is the start's, with the agent's record as it is now.
func waitAgentStart(path string, c *Call, started map[string]any) map[string]any {
	timeout := agentStartDefault
	if n, ok := c.Params["timeout_ms"].(uint64); ok {
		timeout = time.Duration(n) * time.Millisecond
	}
	name, _ := c.Params["name"].(string)
	pane, _ := c.Params["pane_id"].(string)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := send(path, "cli:agent:start", "agent.get", map[string]any{"target": pane}, requestTimeout)
		if err != nil {
			return map[string]any(err)
		}
		if result, ok := resp["result"].(map[string]any); ok {
			agent, _ := result["agent"].(map[string]any)
			switch agent["agent_status"] {
			case "idle", "done":
				if r, ok := started["result"].(map[string]any); ok {
					r["agent"] = agent
				}
				return started
			case "blocked":
				return errorResponse("cli:agent:start", "agent_not_ready", "agent "+name+" is blocked during startup and is not ready for prompts")
			}
		}
		time.Sleep(agentStartPoll)
	}
	return errorResponse("cli:agent:start", "timeout", "timed out waiting for agent startup")
}
