package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/adrg/xdg"

	"github.com/Gaurav-Gosain/tuios/internal/server"
)

// checkSSHAuth stops a bind that would hand a shell on this machine to anyone
// who reaches the port, and answers with the commands that fix it.
//
// It is the SSH twin of checkTransportSecurity in cmd/tuios-web, and it is
// deliberately the same shape. That command refuses a non-loopback bind with no
// TLS and names --auto-tls, --cert or --insecure; this one refuses a
// non-loopback bind with no keys and names an authorized_keys file or
// --no-auth. Both share netutil.IsLoopbackHost, so neither can decide an
// address is local while the other decides it is not.
//
// Nothing here asks a question first, for the reason the web command gives: a
// prompt would make the same command do different things depending on whether
// stdout is a terminal, and a server is started by unit files at least as often
// as by hand.
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
	fmt.Fprintf(w, `
  %s is not this machine, and every connection to TUIOS gets a shell on this
  machine. With no authentication that shell goes to anyone who reaches port
  %s. So pick who you want to let in:

  1. The holders of a public key you name. This is the normal answer.

       mkdir -p %s
       cat ~/.ssh/id_ed25519.pub >> %s
       tuios ssh --host %s --port %s

     TUIOS reads %s first. When that file
     is absent it reads ~/.ssh/authorized_keys, so a machine that already
     accepts your key over ssh needs no new file.

  2. Yourself only, over a tunnel. TUIOS stays on this machine and no key
     file is involved.

       tuios ssh --port %s
       ssh -L %s:localhost:%s <this-machine>

     then run ssh -p %s localhost at the far end.

  3. Anyone at all. Only on a network you trust.

       tuios ssh --host %s --port %s --no-auth

`,
		f.host,
		f.port,
		filepath.Dir(keyFile), keyFile,
		f.host, f.port,
		keyFile,
		f.port,
		f.port, f.port,
		f.port,
		f.host, f.port)

	return err
}
