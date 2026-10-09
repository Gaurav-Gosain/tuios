// Command humancall is what an agent in a pane would run to read the person's
// conversation: it opens the daemon socket, asks for a presence, and calls
// agent-transcript with the nonce it got, or with one it was handed. It prints
// one line per call, PRESENCE=<code> and READ=<code>, where the code is ok or
// the error code the daemon answered.
//
// Usage: humancall SOCKET SESSION WINDOW [NONCE]
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: humancall SOCKET SESSION WINDOW [NONCE]")
		os.Exit(2)
	}
	conn, err := net.Dial("unix", os.Args[1])
	if err != nil {
		fmt.Println("DIAL=" + err.Error())
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	id := 0
	call := func(verb string, params map[string]any) (map[string]any, string) {
		id++
		line, _ := json.Marshal(map[string]any{"id": id, "verb": verb, "params": params})
		if _, err := conn.Write(append(line, '\n')); err != nil {
			return nil, "write: " + err.Error()
		}
		reply, err := br.ReadBytes('\n')
		if err != nil {
			return nil, "read: " + err.Error()
		}
		var env struct {
			Result map[string]any `json:"result"`
			Error  *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(reply, &env); err != nil {
			return nil, "decode: " + err.Error()
		}
		if env.Error != nil {
			return nil, env.Error.Code
		}
		return env.Result, "ok"
	}
	res, code := call("attach-presence", map[string]any{})
	fmt.Println("PRESENCE=" + code)
	nonce := ""
	if len(os.Args) > 4 {
		nonce = os.Args[4]
	} else if res != nil {
		nonce, _ = res["human_nonce"].(string)
	}
	_, code = call("agent-transcript", map[string]any{"session": os.Args[2], "window": os.Args[3], "human_nonce": nonce})
	fmt.Println("READ=" + code)
}
