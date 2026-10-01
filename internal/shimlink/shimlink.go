// Package shimlink makes the link that runs tuios as another program: a
// link named tmux or herdr, in a bin directory a pane puts on its PATH,
// that points at the tuios binary. tuios reads the name it was run by and
// answers as that program.
package shimlink
