// Package build tells what build of Kero runs: its version, and whether
// it is the server build (cmd/kero-server), which runs kerod and the CLI
// alone, with no app, and updates itself.
package build

import "github.com/egoist/mygo"

// Server is set by the server build.
var Server bool

// Version is the version of Kero that runs: the app's, or the one the
// server build was made of.
func Version() string { return mygo.App.Version() }

// Daemon is what kerod's code is made of, which the app sets (kerodBuild)
// and kerod tells clients (proto.Welcome.Build): an update that leaves it
// as it was needs no restart of kerod. The server build leaves it empty.
var Daemon string
