// Command gen writes docs/ENVIRONMENT.md from the table in internal/envvars.
package main

import (
	"fmt"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/envvars"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen FILE")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[1], []byte(envvars.Page()), 0o644); err != nil { //nolint:gosec // a docs page
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
