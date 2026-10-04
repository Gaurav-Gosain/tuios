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
	"path/filepath"
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
	// PluginConfigDir makes and returns a plugin's config folder, for herdr
	// plugin config-dir. Nil answers that command unsupported.
	PluginConfigDir func(id string) (string, error)
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
	case OutPluginConfigDir:
		if o.PluginConfigDir == nil {
			printJSON(o.Stderr, errorResponse(c.ID, "unsupported", "this herdr front has no plugin folders"))
			return 1
		}
		dir, err := o.PluginConfigDir(c.Text)
		if err != nil {
			fmt.Fprintln(o.Stderr, "herdr: "+err.Error())
			return 1
		}
		fmt.Fprintln(o.Stdout, dir)
		return 0
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
	switch c.Output {
	case OutStatus:
		return printStatus(o, path, c)
	case OutSessions:
		return printSessions(o, path, c)
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
// answer is the start's, with the agent's record as it is now. The server
// holds the wait (agent.wait), so the wait is one request, not a poll.
func waitAgentStart(path string, c *Call, started map[string]any) map[string]any {
	timeout := agentStartDefault
	if n, ok := c.Params["timeout_ms"].(uint64); ok {
		timeout = time.Duration(n) * time.Millisecond
	}
	name, _ := c.Params["name"].(string)
	pane, _ := c.Params["pane_id"].(string)
	timedOut := errorResponse("cli:agent:start", "timeout", "timed out waiting for agent startup")
	for deadline := time.Now().Add(timeout); ; {
		left := time.Until(deadline)
		if left <= 0 {
			return timedOut
		}
		params := map[string]any{"target": pane, "until": []string{"idle", "done", "blocked"}, "timeout_ms": max(left.Milliseconds(), 1)}
		resp, err := send(path, "cli:agent:start", "agent.wait", params, left+requestTimeout)
		if err != nil {
			return map[string]any(err)
		}
		// An error is the wait's timeout, or a pane that went away, which
		// never comes ready either.
		result, ok := resp["result"].(map[string]any)
		if !ok {
			return timedOut
		}
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
		// The agent moved on between the wait and its record: wait again.
	}
}

// printStatus answers herdr status. A ping says whether the server
// answers. The client part describes this front: herdr's version and
// protocol, with +tuios. tuios does not serve herdr's endpoint protocol, so
// endpoint_compatible is false, and no restart changes that.
func printStatus(o Options, path string, c *Call) int {
	asJSON, _ := c.Params["json"].(bool)
	running := false
	var version any
	var protocol any
	if resp, err := send(path, c.ID, "ping", map[string]any{}, requestTimeout); err == nil {
		if r, ok := resp["result"].(map[string]any); ok && r["type"] == "pong" {
			running, version, protocol = true, r["version"], r["protocol"]
		}
	}
	bin, _ := os.Executable()
	client := map[string]any{
		"version": Version + "+tuios", "channel": "stable", "protocol": protocolVersion,
		"endpoint_protocol_generation": 0, "endpoint_capabilities": []string{},
		"remote_host_bridge": false, "binary": bin,
	}
	server := map[string]any{
		"status": "not_running", "running": false, "socket": path,
		"restart_needed": false, "server_binary_stale": false,
	}
	if running {
		server["status"], server["running"] = "running", true
		server["version"], server["protocol"] = version, protocol
		server["compatible"] = fmt.Sprint(protocol) == fmt.Sprint(protocolVersion)
		server["endpoint_compatible"] = false
	}
	if asJSON {
		switch c.Text {
		case "server":
			printJSON(o.Stdout, server)
		case "client":
			printJSON(o.Stdout, client)
		default:
			printJSON(o.Stdout, map[string]any{"client": client, "server": server,
				"update": map[string]any{"restart_needed": false, "server_binary_stale": false}})
		}
		return 0
	}
	serverLines := func(indent string) {
		if !running {
			fmt.Fprintf(o.Stdout, "%sstatus: not running\n%ssocket: %s\n", indent, indent, path)
			return
		}
		compatible := "no"
		if server["compatible"] == true {
			compatible = "yes"
		}
		fmt.Fprintf(o.Stdout, "%sstatus: running\n%sversion: %v\n%sendpoint_compatible: no\n%sprivate_protocol: %v\n%sprivate_protocol_compatible: %s\n%ssocket: %s\n",
			indent, indent, version, indent, indent, protocol, indent, compatible, indent, path)
	}
	clientLines := func(indent string) {
		fmt.Fprintf(o.Stdout, "%sversion: %s+tuios\n%schannel: stable\n%sprotocol: %d\n", indent, Version, indent, indent, protocolVersion)
	}
	switch c.Text {
	case "server":
		serverLines("")
	case "client":
		clientLines("")
		fmt.Fprintf(o.Stdout, "binary: %s\n", bin)
	default:
		fmt.Fprintln(o.Stdout, "client:")
		clientLines("  ")
		fmt.Fprintln(o.Stdout, "\nserver:")
		serverLines("  ")
		fmt.Fprintln(o.Stdout, "\nupdate:\n  restart_needed: no\n  server_binary_stale: no")
	}
	return 0
}

// printSessions answers herdr session list: one session, default, at the
// socket the front talks to, running when it answers a ping.
func printSessions(o Options, path string, c *Call) int {
	running := false
	if resp, err := send(path, c.ID, "ping", map[string]any{}, requestTimeout); err == nil {
		r, _ := resp["result"].(map[string]any)
		running = r["type"] == "pong"
	}
	dir := filepath.Dir(path)
	if asJSON, _ := c.Params["json"].(bool); asJSON {
		printJSON(o.Stdout, map[string]any{"sessions": []map[string]any{{
			"name": "default", "default": true, "running": running, "socket_path": path, "session_dir": dir,
		}}})
		return 0
	}
	status := "stopped"
	if running {
		status = "running"
	}
	fmt.Fprintf(o.Stdout, "%-20s %-8s %-48s socket\n", "name", "status", "directory")
	fmt.Fprintf(o.Stdout, "%-20s %-8s %-48s %s\n", "default", status, dir, path)
	return 0
}

// Request sends one method to the herdr socket at path and returns the
// response line as an object: a result or an error. A socket that cannot
// be reached is an error in herdr's server_not_running shape. tuios's own
// commands use it to reach the plugin host.
func Request(path, method string, params map[string]any, timeout time.Duration) (map[string]any, error) {
	resp, cerr := send(path, "tuios:"+method, method, params, timeout)
	if cerr != nil {
		return nil, cerr
	}
	return resp, nil
}

// NotRunning reports whether err is a socket nobody listens on.
func NotRunning(err error) bool {
	var c connError
	if errors.As(err, &c) {
		if e, ok := c["error"].(map[string]any); ok {
			return e["code"] == "server_not_running"
		}
	}
	return false
}
