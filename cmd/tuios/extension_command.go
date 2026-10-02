//go:build slim

package main

import (
	"github.com/Gaurav-Gosain/tuios/internal/edition"
	"github.com/Gaurav-Gosain/tuios/internal/extension"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// runExtensionCommand runs a command this build does not have through its
// extension binary, tuios-<name>, when one is installed. See
// internal/extension for the lookup, the version handshake and what the
// extension is given. With no extension it returns the one line that says
// the command is not in this build and how to get it.
func runExtensionCommand(name string, args []string) error {
	path, err := extension.Lookup(name)
	if err != nil {
		return edition.MissingCommand(name)
	}
	host := version
	if err := extension.Check(path, name, host); err != nil {
		return err
	}
	// An extension reaches the daemon this command would reach. The path is
	// passed for the extension's convenience; GetSocketPath fails only when
	// TUIOS_SOCKET already names a socket nobody listens on, and the
	// extension then reads that same refusal itself.
	socket, _ := session.GetSocketPath()
	code, err := extension.Run(path, args, host, socket)
	if err != nil {
		return err
	}
	if code != 0 {
		return extensionExit(code)
	}
	return nil
}

// extensionExit carries an extension's exit status out of main. Its text is
// empty because the extension has already said what went wrong.
type extensionExit int

func (extensionExit) Error() string     { return "" }
func (e extensionExit) ExitStatus() int { return int(e) }
