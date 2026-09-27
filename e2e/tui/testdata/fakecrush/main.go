// Command fakecrush stands in for Crush in the herdr protocol test. Built as a
// binary named crush, it is started in a pane the way a person starts Crush,
// and reports to herdr's socket exactly the way Crush's own client does
// (internal/herdr/client.go in github.com/charmbracelet/crush): only when
// HERDR_ENV is 1 and HERDR_SOCKET_PATH and HERDR_PANE_ID are set, one JSON-RPC
// request per connection, a line out, the answer drained until the server
// closes, a seq seeded from the clock.
//
// It prints what it was told, sends Crush's first report (idle), and then
// reads one word per line from its terminal and sends that report: working,
// blocked, idle, release, stale (a seq below the last one), foreign (another
// pane's id) and unsupported (a method tuios does not answer). Each answer is
// printed on a line of its own, after REPLY.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

type params struct {
	PaneID         string `json:"pane_id"`
	Source         string `json:"source"`
	Agent          string `json:"agent"`
	State          string `json:"state,omitempty"`
	Seq            uint64 `json:"seq"`
	AgentSessionID string `json:"agent_session_id"`
}

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params params `json:"params"`
}

func main() {
	env, sock, pane := os.Getenv("HERDR_ENV"), os.Getenv("HERDR_SOCKET_PATH"), os.Getenv("HERDR_PANE_ID")
	fmt.Printf("HERDR_ENV=%q PANE_MATCHES=%v SOCKET_SET=%v\n", env, pane != "" && pane == os.Getenv("TUIOS_PANE_ID"), sock != "")
	if env != "1" || sock == "" || pane == "" {
		fmt.Println("NO-HERDR")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	seq := uint64(time.Now().UnixNano())
	send := func(method, state, paneID string, s uint64) {
		req := request{
			ID:     fmt.Sprintf("crush:%s:%d", method, time.Now().UnixNano()),
			Method: method,
			Params: params{PaneID: paneID, Source: "crush", Agent: "crush", State: state, Seq: s, AgentSessionID: "fake-session"},
		}
		fmt.Println("REPLY " + dialSend(sock, req))
	}
	next := func() uint64 { seq++; return seq }
	send("pane.report_agent", "idle", pane, next())
	fmt.Println("FAKE-CRUSH-READY")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		switch word := strings.TrimSpace(in.Text()); word {
		case "working", "blocked", "idle":
			send("pane.report_agent", word, pane, next())
		case "release":
			send("pane.release_agent", "", pane, next())
		case "stale":
			send("pane.report_agent", "working", pane, 1)
		case "foreign":
			send("pane.report_agent", "working", "not-this-pane", next())
		case "unsupported":
			send("pane.send_input", "", pane, next())
		case "quit":
			return
		}
	}
}

// dialSend is Crush's: dial, write one line, read to the end.
func dialSend(socketPath string, req request) string {
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err != nil {
		return "dial error: " + err.Error()
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	data, _ := json.Marshal(req)
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return "write error: " + err.Error()
	}
	out, _ := io.ReadAll(conn)
	return strings.TrimSpace(string(out))
}
