// Command lessonsjson prints the lessons the browser build embeds, as
// tuios.lessons() returns them. build.sh writes it to lessons.json, so the
// docs site's hub can list the tracks without loading the wasm.
package main

import (
	"fmt"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
)

func main() {
	b, err := lessons.JSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lessonsjson:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(b); err != nil {
		fmt.Fprintln(os.Stderr, "lessonsjson:", err)
		os.Exit(1)
	}
}
