# NAME

prise - terminal multiplexer

# SYNOPSIS

**prise** [*command*] [*options*]

# DESCRIPTION

Prise is a terminal multiplexer targeted at modern terminals. It allows multiple
terminal sessions to run within a single window, with support for splitting,
tabs, and session persistence.

Prise uses a client-server architecture. The server manages PTY sessions and
persists them across client connections. Clients connect to render the UI and
handle input.

# COMMANDS

Running **prise** with no command starts the client and connects to an existing
server.

## pty

Manage PTYs.

**prise pty kill** *id*
:   Kill a PTY by its ID.

**prise pty list**
:   List all PTYs with their IDs, working directories, titles, and attached client count.

## serve

Start the server in the foreground. The server must be running before clients
can connect. See **prise**(7) for service configuration.

## session

Manage sessions.

**prise session attach** [*name*]
:   Attach to a session. If no name is given, attaches to the most recently used session.

**prise session delete** *name*
:   Delete a session.

**prise session list**
:   List all sessions.

**prise session rename** *old-name* *new-name*
:   Rename a session.

# OPTIONS

**-h**, **--help**
:   Show help message.

**-v**, **--version**
:   Show version.

# FILES

*/tmp/prise-{uid}.sock*
:   Unix domain socket for client-server communication.

*~/.config/prise/init.lua*
:   User configuration file. See **prise**(5).

*~/.local/share/prise/sessions/*
:   Session state files.

*~/.cache/prise/server.log*
:   Server log file.

# SEE ALSO

[prise(5)](prise.5.html), [prise(7)](prise.7.html)
