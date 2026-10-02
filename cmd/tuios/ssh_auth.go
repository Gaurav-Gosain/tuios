//go:build !slim

package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/adrg/xdg"

	"github.com/Gaurav-Gosain/tuios/internal/netutil"
	"github.com/Gaurav-Gosain/tuios/internal/server"
)

// checkSSHAuth stops a bind that has no authorized keys and no --no-auth, and
// answers with the commands that fix it. Loopback is refused too: see
// server.PlanSSHAuth.
//
// Nothing here asks a question first: a prompt would make the same command do
// different things depending on whether stdout is a terminal, and a server is
// started by unit files at least as often as by hand.
func checkSSHAuth(w io.Writer, f sshServerFlags) error {
	_, err := server.PlanSSHAuth(f.host, f.authorizedKeys, f.noAuth)
	if err == nil {
		return nil
	}
	// A keys file that cannot be read is already a sentence that says what to
	// do. The advice below answers one thing only: a network bind with nothing
	// configured, which has several right answers.
	if !errors.Is(err, server.ErrNoSSHAuth) {
		return err
	}

	keyFile := filepath.Join(xdg.ConfigHome, server.ConfigAuthorizedKeys)

	// Printed here rather than carried in the error: fang reflows an error into
	// a paragraph, which would run the commands together and leave nothing to
	// copy.
	who := "anyone who reaches port " + f.port
	noAuthNote := "Only on a network you trust."
	if netutil.IsLoopbackHost(f.host) {
		who = "every user on this machine"
		noAuthNote = "Every user on this machine can then open a shell as you."
	}
	fmt.Fprintf(w, `
  TUIOS found no authorized keys file, so it does not know who may connect.
  Every connection gets a shell on this machine. With no authentication
  that shell goes to %s. Pick one:

  1. Add your public key to the TUIOS keys file. This is the normal answer.

       mkdir -p %s
       cat %s >> %s
       tuios ssh --host %s --port %s

  2. Use the keys that sshd accepts.

       tuios ssh --host %s --port %s --authorized-keys ~/.ssh/authorized_keys

     TUIOS does not accept a key with options such as command=, from= or
     restrict. TUIOS does not apply the rules in sshd_config.

  3. Let every connection in. %s

       tuios ssh --host %s --port %s --no-auth

`,
		who,
		filepath.Dir(keyFile), server.PublicKeyHint(), keyFile,
		f.host, f.port,
		f.host, f.port,
		noAuthNote,
		f.host, f.port)

	return err
}
