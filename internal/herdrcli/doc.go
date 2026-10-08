// Package herdrcli is herdr's command line, answered by tuios.
//
// herdr (github.com/herdrdev/herdr) gives its plugins and tools one
// interface: its own CLI, which they run through $HERDR_BIN_PATH. A tool
// built for herdr (terminal-browser, terminal-code, the Vim navigation
// plugins, Telegram bridges) runs `"$HERDR_BIN_PATH" pane split --pane ID
// --direction right`, reads the JSON line it prints, and acts on it.
//
// In a tuios pane HERDR_BIN_PATH names a link called herdr that points at the
// tuios binary (on Windows, herdr.exe: a hardlink to the binary, or a copy
// of it where a hardlink cannot be made). tuios run under that name is this
// package: it parses herdr's CLI as herdr 0.9.3 does (src/cli in herdr's
// source, flag for flag), sends each call to the herdr socket tuios already
// answers (HERDR_SOCKET_PATH, see internal/session/herdr_api.go), and prints
// what herdr's CLI prints, with herdr's exit codes:
//
//   - a usage error: the message on stderr, exit 2;
//   - an answer with an error: the response line on stderr, exit 1;
//   - an answer: the response line on stdout, exit 0. The few commands herdr
//     runs for their effect alone (send-text, send-keys, run and the reports)
//     print nothing, and the read commands print the text read.
//
// A herdr command that does its work on herdr's own machine and has no tuios
// meaning (status, config, session, terminal, update, plugin, integration,
// server stop) answers herdr's error shape with code unsupported, so a tool
// that runs one fails cleanly and can go on.
//
// The package holds no authority. It runs as the caller and dials the socket
// the caller could dial itself, and the daemon checks the pane's grants for
// every call (herdr_api.go). Parse is pure, so it is fuzzed on its own.
package herdrcli
