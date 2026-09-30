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
// pane's id) and unsupported (pane.zoom, a method tuios does not answer). The words
// charmbracelet/crush#3541 adds are permission and question (blocked with the
// message that Crush sends for each), meta (pane.report_metadata with a title
// and a model token) and notify (notification.show). crash reports working
// and exits at once with no release, the way a killed Crush leaves its pane.
// Each answer is printed on a line of its own, after REPLY.
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
	Message        string `json:"message,omitempty"`
	Seq            uint64 `json:"seq"`
	AgentSessionID string `json:"agent_session_id"`
}

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

func main() {
	env, sock, pane := os.Getenv("HERDR_ENV"), os.Getenv("HERDR_SOCKET_PATH"), os.Getenv("HERDR_PANE_ID")
	fmt.Printf("HERDR_ENV=%q PANE_MATCHES=%v SOCKET_SET=%v\n", env, pane != "" && strings.HasSuffix(pane, ":p"+herdrHex(os.Getenv("TUIOS_PANE_ID"))), sock != "")
	if env != "1" || sock == "" || pane == "" {
		fmt.Println("NO-HERDR")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	seq := uint64(time.Now().UnixNano())
	sendRaw := func(method string, p any) {
		req := request{ID: fmt.Sprintf("crush:%s:%d", method, time.Now().UnixNano()), Method: method, Params: p}
		fmt.Println("REPLY " + dialSend(sock, req))
	}
	sendMsg := func(method, state, message, paneID string, s uint64) {
		sendRaw(method, params{PaneID: paneID, Source: "crush", Agent: "crush", State: state, Message: message, Seq: s, AgentSessionID: "fake-session"})
	}
	send := func(method, state, paneID string, s uint64) { sendMsg(method, state, "", paneID, s) }
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
			send("pane.zoom", "", pane, next())
		case "permission":
			sendMsg("pane.report_agent", "blocked", "Permission: bash - go test ./...", pane, next())
		case "question":
			sendMsg("pane.report_agent", "blocked", "Pick a database", pane, next())
		case "meta":
			sendRaw("pane.report_metadata", map[string]any{
				"pane_id": pane, "source": "crush", "title": "Fix the flaky test",
				"tokens": map[string]any{"session": "fake-session", "model": "fake-model"}, "seq": next(),
			})
		case "notify":
			sendRaw("notification.show", map[string]any{"title": "Crush finished", "body": "All tests pass"})
		case "crash":
			send("pane.report_agent", "working", pane, next())
			os.Exit(3)
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

// herdrHex is the part of a tuios id that tuios puts in a herdr id: the
// first 12 hex digits, dashes dropped. HERDR_PANE_ID is <session>:p<this>.
func herdrHex(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	return id[:min(len(id), 12)]
}
