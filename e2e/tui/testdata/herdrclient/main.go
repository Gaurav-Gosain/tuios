// Command herdrclient sends one request to herdr's socket API, the way a tool
// built for herdr does: one JSON line to HERDR_SOCKET_PATH, one line back.
//
//	herdrclient METHOD [PARAMS]
//
// PARAMS is a JSON object, {} when it is left out. In it, $HERDR_PANE_ID is
// the caller's own pane. The reply is printed after REPLY on one line, and
// the caller's pane id after SELF, so a test can compare the two.
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: herdrclient METHOD [PARAMS]")
		os.Exit(2)
	}
	method, params := os.Args[1], "{}"
	if len(os.Args) > 2 {
		params = os.Args[2]
	}
	self := os.Getenv("HERDR_PANE_ID")
	params = strings.ReplaceAll(params, "$HERDR_PANE_ID", self)
	conn, err := net.Dial("unix", os.Getenv("HERDR_SOCKET_PATH"))
	if err != nil {
		fmt.Println("REPLY-ERROR", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, `{"id":"e2e","method":%q,"params":%s}`+"\n", method, params)
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		fmt.Println("REPLY-ERROR", err)
		os.Exit(1)
	}
	fmt.Println("SELF " + self)
	fmt.Print("REPLY " + line)
}
