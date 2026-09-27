// Command fakegoose stands in for goose's CLI in the manifest test. It draws
// the tool approval prompt goose draws (prompt_tool_confirmation in
// crates/goose-cli/src/session/mod.rs, rendered the way the cliclack crate
// renders an open select) when a first line arrives on its terminal, the way
// goose reaches a tool call some time after it starts, and waits. A second
// line answers it: the prompt is redrawn answered, the way cliclack redraws a
// submitted select, and the thinking spinner goose shows during a turn takes
// the bottom line.
package main

import (
	"bufio"
	"fmt"
	"os"
	"time"
)

func main() {
	in := bufio.NewScanner(os.Stdin)
	fmt.Print("fake goose started\r\n")
	if !in.Scan() {
		return
	}
	fmt.Print("─── shell | developer ──────────────────────────\r\n")
	fmt.Print("command: rm -rf build\r\n\r\n")
	fmt.Print("◆  Goose would like to call the above tool, do you allow?\r\n")
	fmt.Print("│  ● Allow (Allow the tool call once)\r\n")
	fmt.Print("│  ○ Always Allow\r\n")
	fmt.Print("│  ○ Deny\r\n")
	fmt.Print("│  ○ Cancel\r\n")
	fmt.Print("└\r\n")
	if !in.Scan() {
		return
	}
	fmt.Print("\x1b[2J\x1b[H")
	fmt.Print("◇  Goose would like to call the above tool, do you allow?\r\n")
	fmt.Print("│  Allow\r\n")
	fmt.Print("◒  Pondering the options...  (Ctrl+C to interrupt)")
	for {
		time.Sleep(time.Hour)
	}
}
