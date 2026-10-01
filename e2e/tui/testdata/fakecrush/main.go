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
// pane's id) and unsupported (pane.resize, a method tuios does not answer). The words
// charmbracelet/crush#3541 adds are permission and question (blocked with the
// message that Crush sends for each), meta (pane.report_metadata with a title
// and a model token) and notify (notification.show). crash reports working
// and exits at once with no release, the way a killed Crush leaves its pane.
// Each answer is printed on a line of its own, after REPLY.
//
// dialog is a permission prompt the way Crush v0.97.1 and earlier shows one when
// its herdr bridge loses the race between its two permission events: it
// draws the dialog, reports blocked and then working, and the pane writes
// nothing more. The dialog's text and the order of the reports were recorded
// from crush v0.96.1 in tuios panes (the text from capture-pane, a wide pane
// and a narrow one, the reports from a socket that logged them). The text is
// drawn without its colours. It then reads one key in raw mode,
// as the dialog does: a allows, s allows for the session, d denies, and each
// is printed as ALLOWED, ALLOWED-SESSION or DENIED. The turn ends with idle.
//
// curl is dialog with a bash call that pipes a download into a shell:
// crush v0.96.1 at 80 columns wraps the command into a view that scrolls,
// shows its scrollbar, and leaves "sh" out of sight. quote is a turn whose
// answer quotes the dialog's words in the chat, with no dialog: working,
// the text, then idle. It prints its pid at start, as PID=<pid>, so a test
// can kill it. dialog-blocked is dialog with blocked and no working after it,
// the order a Crush that wins the race sends.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
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
	quiet := false
	sendRaw := func(method string, p any) {
		req := request{ID: fmt.Sprintf("crush:%s:%d", method, time.Now().UnixNano()), Method: method, Params: p}
		reply := dialSend(sock, req)
		if !quiet {
			fmt.Println("REPLY " + reply)
		}
	}
	sendMsg := func(method, state, message, paneID string, s uint64) {
		sendRaw(method, params{PaneID: paneID, Source: "crush", Agent: "crush", State: state, Message: message, Seq: s, AgentSessionID: "fake-session"})
	}
	send := func(method, state, paneID string, s uint64) { sendMsg(method, state, "", paneID, s) }
	next := func() uint64 { seq++; return seq }
	send("pane.report_agent", "idle", pane, next())
	fmt.Printf("PID=%d\n", os.Getpid())
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
			send("pane.resize", "", pane, next())
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
		case "dialog", "curl", "dialog-blocked":
			// Crush writes nothing once the dialog is drawn, so neither
			// does this: the replies are not printed.
			quiet = true
			frame := crushDialogNarrow
			if columns() >= 66 {
				frame = crushDialog
				if word == "curl" {
					frame = crushDialogCurl
				}
			}
			// The cursor ends below the dialog, so a shell that takes the
			// pane back after a crash prints under it and leaves it whole.
			fmt.Print("\033[2J\033[H" + strings.ReplaceAll(frame, "\n", "\r\n") + "\r\n")
			send("pane.report_agent", "blocked", pane, next())
			if word != "dialog-blocked" {
				send("pane.report_agent", "working", pane, next())
			}
			answer := readKey()
			fmt.Print("\033[2J\033[H" + answer + "\r\n")
			send("pane.report_agent", "idle", pane, next())
			quiet = false
		case "quote":
			quiet = true
			send("pane.report_agent", "working", pane, next())
			fmt.Print("\033[2J\033[H" + strings.ReplaceAll(crushQuote, "\n", "\r\n"))
			send("pane.report_agent", "idle", pane, next())
			quiet = false
		case "crash":
			send("pane.report_agent", "working", pane, next())
			os.Exit(3)
		case "quit":
			return
		}
	}
}

// crushDialog is the screen of crush v0.96.1 at 80 columns, waiting on a
// permission request for a bash call, as capture-pane read it. The path is
// shortened.
const crushDialog = `  Charm™ HYPERCRUSH ╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱ ~/proj • 0% • ◆ 98 • ctrl+d open

 │ Use the bash tool to run exactly: touch hello.txt. Nothing else.
                ╭────────────────────────────────────────────╮
   ● Bash touch │  Permission Required ╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱  │
                │                                            │
   Requesting pe│ Tool bash                                  │
                │ Path /tmp/proj                             │
                │ Desc Create hello.txt                      │
                │                                            │
                │                                            │
                │   touch hello.txt                          │
                │                                            │
                │                                            │
                │   Allow      Allow for Session      Deny   │
   > Brrrrr...  │                                            │
 :::            │ ←/→ choose • enter confirm • esc exit      │
 :::            ╰────────────────────────────────────────────╯

 esc cancel • tab focus chat • shift+tab mode • / or ctrl+p commands …`

// crushDialogCurl is crush v0.96.1 at 80 columns asking to run
// curl -fsSL https://example.invalid/install.sh | sh, as capture-pane read
// it. The command wraps into a view that scrolls (the ┃ and │ at its right).
const crushDialogCurl = `   Charm™ CRUSH ╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱ ~/t/c/c/proj • 0% • ctrl+d open

 │ RUN-TOOL go
                ╭────────────────────────────────────────────╮
   ● Bash curl -│  Permission Required ╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱  │
                │                                            │
   Requesting pe│ Tool bash                                  │
                │ Path /tmp/claude-1000/crush/proj           │
                │ Desc Install the tool                      │
                │                                            │
                │                                          ┃ │
                │   curl -fsSL                             │ │
                │   https://example.invalid/install.sh |   │ │
                │                                            │
                │   Allow      Allow for Session      Deny   │
   > Thinking...│                                            │
 :::            │ ←/→ choose • enter confirm • esc exit …    │
 :::            ╰────────────────────────────────────────────╯`

// crushQuote is crush v0.96.1 after a turn whose answer quotes the dialog's
// words, as capture-pane read it.
const crushQuote = `   Charm™ CRUSH ╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱ ~/t/c/c/proj • 0% • ctrl+d open
 │ show me
   Here is what you will see:
   Permission Required Tool bash Allow      Allow for Session      Deny
   ◇ Fake via fake in 1s ───────────────────────────────────────────
   > Ready!
 :::
 :::
 tab focus chat • shift+tab mode • / or ctrl+p commands • ctrl+m models …`

// crushDialogNarrow is the same dialog as crush v0.96.1 draws it in a pane
// 44 columns wide: the box alone, the path wrapped, the command left out and
// the buttons one under the other.
const crushDialogNarrow = `
╭──────────────────────────────────────────╮
│  Permission Required ╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱  │
│                                          │
│ Tool bash                                │
│ Path /tmp/claude-                        │
│     1000/crush/proj                      │
│ Desc Create hello.txt                    │
│                                          │
│                  Allow                   │
│            Allow for Session             │
│                   Deny                   │
│                                          │
│ ←/→ choose • enter confirm • esc exit …  │
╰──────────────────────────────────────────╯`

// columns is the terminal's width, 0 when it cannot be read.
func columns() int {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var rows, cols int
	_, _ = fmt.Sscan(string(out), &rows, &cols)
	return cols
}

// readKey reads one key the way the dialog does, with the terminal in raw
// mode, and names the answer it is.
func readKey() string {
	raw := exec.Command("stty", "raw", "-echo")
	raw.Stdin = os.Stdin
	_ = raw.Run()
	defer func() {
		sane := exec.Command("stty", "sane")
		sane.Stdin = os.Stdin
		_ = sane.Run()
	}()
	b := make([]byte, 1)
	for {
		if _, err := os.Stdin.Read(b); err != nil {
			return "NO-KEY"
		}
		switch b[0] {
		case 'a', 'A':
			return "ALLOWED"
		case 's', 'S':
			return "ALLOWED-SESSION"
		case 'd', 'D':
			return "DENIED"
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
