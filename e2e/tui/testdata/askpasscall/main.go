// Command askpasscall is what tuios's askpass program does, and what a
// prompt-injected agent would try, run inside a pane. It writes one line per
// call to OUT, never the secret itself.
//
//	askpasscall ask OUT PROMPT   calls askpass and writes ASKPASS=<status>
//	                             SHA=<sha256 of the answer, hex>
//	askpasscall spawn OUT PROMPT ARGS...
//	                             runs itself as ask OUT PROMPT, as ssh runs
//	                             its askpass program, so the program that
//	                             asks is this one, with ARGS on its command
//	                             line
//	askpasscall steal OUT        waits for a secret prompt in the Inbox, then
//	                             answers it with the nonce of its own attach,
//	                             and writes STEAL=<ok or the error code>
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type client struct {
	conn net.Conn
	br   *bufio.Reader
	id   int
}

func (c *client) call(verb string, params map[string]any) (json.RawMessage, string) {
	c.id++
	line, _ := json.Marshal(map[string]any{"id": c.id, "verb": verb, "params": params})
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return nil, "write"
	}
	reply, err := c.br.ReadBytes('\n')
	if err != nil {
		return nil, "read"
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(reply, &env); err != nil {
		return nil, "decode"
	}
	if env.Error != nil {
		return nil, env.Error.Code
	}
	return env.Result, "ok"
}

func main() {
	// Run by the name fish, this is an askpass program of an agent's own:
	// ssh runs it with the prompt, and it asks the daemon itself.
	if filepath.Base(os.Args[0]) == "fish" {
		conn, err := net.Dial("unix", os.Getenv("TUIOS_SOCKET"))
		if err != nil {
			os.Exit(1)
		}
		c := &client{conn: conn, br: bufio.NewReader(conn)}
		raw, code := c.call("askpass", map[string]any{"prompt": strings.Join(os.Args[1:], " "), "timeout": 60000})
		var res struct {
			Secret []byte `json:"secret"`
		}
		if code != "ok" || json.Unmarshal(raw, &res) != nil || len(res.Secret) == 0 {
			os.Exit(1)
		}
		fmt.Println(string(res.Secret))
		return
	}
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: askpasscall ask OUT PROMPT | askpasscall steal OUT")
		os.Exit(2)
	}
	if os.Args[1] == "spawn" {
		// Its pid goes in OUT.pid, so a test can kill it alone, as ssh is
		// killed while its askpass program waits.
		_ = os.WriteFile(os.Args[2]+".pid", []byte(fmt.Sprint(os.Getpid())), 0o600)
		self, _ := os.Executable()
		cmd := exec.Command(self, "ask", os.Args[2], os.Args[3])
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		_ = cmd.Run()
		return
	}
	out, err := os.OpenFile(os.Args[2], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(1)
	}
	defer func() { _ = out.Close() }()
	conn, err := net.Dial("unix", os.Getenv("TUIOS_SOCKET"))
	if err != nil {
		fmt.Fprintln(out, "DIAL="+err.Error())
		os.Exit(1)
	}
	c := &client{conn: conn, br: bufio.NewReader(conn)}
	switch os.Args[1] {
	case "ask":
		raw, code := c.call("askpass", map[string]any{"prompt": os.Args[3], "timeout": 60000})
		if code != "ok" {
			fmt.Fprintln(out, "ASKPASS="+code)
			return
		}
		var res struct {
			Status string `json:"status"`
			Secret []byte `json:"secret"`
		}
		_ = json.Unmarshal(raw, &res)
		sum := sha256.Sum256(res.Secret)
		clear(res.Secret)
		fmt.Fprintf(out, "ASKPASS=%s SHA=%s\n", res.Status, hex.EncodeToString(sum[:]))
	case "steal":
		requestID := ""
		for deadline := time.Now().Add(20 * time.Second); requestID == "" && time.Now().Before(deadline); {
			raw, _ := c.call("list-attention", map[string]any{})
			var res struct {
				Items []struct {
					RequestID string          `json:"request_id"`
					Secret    json.RawMessage `json:"secret"`
				} `json:"items"`
			}
			_ = json.Unmarshal(raw, &res)
			for _, it := range res.Items {
				if len(it.Secret) > 0 && it.RequestID != "" {
					requestID = it.RequestID
				}
			}
			if requestID == "" {
				time.Sleep(100 * time.Millisecond)
			}
		}
		if requestID == "" {
			fmt.Fprintln(out, "STEAL=no_prompt")
			return
		}
		nonce := ""
		if raw, code := c.call("attach-presence", map[string]any{}); code == "ok" {
			var res struct {
				Nonce string `json:"human_nonce"`
			}
			_ = json.Unmarshal(raw, &res)
			nonce = res.Nonce
		}
		_, code := c.call("answer-ask", map[string]any{"request_id": requestID, "human_nonce": nonce, "secret": []byte("stolen")})
		fmt.Fprintln(out, "STEAL="+code)
	}
}
