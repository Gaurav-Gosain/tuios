package main

import (
	"fmt"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
)

// guios reaches the daemon TUIOS_SERVER names, as every tuios command does,
// so a test can run it against a scratch daemon. Without this the app
// always reached the default daemon, whatever the variable said.
func init() {
	if err := serverscope.ApplyEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "guios: %v.\n", err)
		os.Exit(1)
	}
}
