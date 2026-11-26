// Command resume-wrap runs a command so that the terminal can bring it back
// after a restart (OSC 7577, the Program Resume Protocol).
//
//	resume-wrap ssh devbox
//
// When the terminal gave a token, resume-wrap reports itself, the command and
// the folder it runs in, then becomes the command, so the pid in the report
// is the command's. The report names resume-wrap too, so the command that
// comes back reports again. The command never gets the token. Without a
// token, resume-wrap only runs the command.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"tuios.dev/progresume"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "Usage: resume-wrap COMMAND [ARG...]")
		fmt.Fprintln(os.Stderr, "Runs COMMAND so that a terminal with OSC 7577 can start it again after a restart.")
		os.Exit(2)
	}
	cwd, _ := os.Getwd()
	err := progresume.Report(append([]string{"resume-wrap"}, args...), cwd)
	// Only a report that breaks a rule is worth a word. No token or no
	// terminal means there is nothing to report to.
	if errors.Is(err, progresume.ErrInvalid) {
		fmt.Fprintf(os.Stderr, "resume-wrap: the terminal cannot resume %s: %v\n", args[0], err)
	}
	// Report took the token out of the environment, also when it sent
	// nothing.
	progresume.TakeToken()
	bin, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "resume-wrap: %v\n", err)
		os.Exit(127)
	}
	if err := run(bin, args); err != nil {
		fmt.Fprintf(os.Stderr, "resume-wrap: %v\n", err)
		os.Exit(126)
	}
}
