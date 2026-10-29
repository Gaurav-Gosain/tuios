package app

import "os/exec"

// editorCommand readies c, the person's own terminal editor, to run on this
// client's terminal through tea.ExecProcess, and returns c. Every editor tuios
// hands to ExecProcess goes through it.
//
// ExecProcess gives a command the program's output unless the command already
// has a Stdout. On a local client that output is the post-render wrapper, not
// an *os.File, so os/exec makes a pipe for the editor's stdout. An editor on a
// pipe cannot put the terminal in raw mode: it runs with echo on and reads by
// the line, so the host's replies to its startup queries print on the screen
// as ^[]11;rgb:... text. Setting Stdout to the wrapper's file gives the editor
// the terminal itself.
//
// Nothing draws a frame while the editor runs, so the order the wrapper keeps
// between frames and graphics does not matter here. A client with no wrapper
// (SSH, whose output is the SSH session) keeps Bubble Tea's default, since
// os.Stdout there is the server's.
func (m *OS) editorCommand(c *exec.Cmd) *exec.Cmd {
	if prw := m.PostRenderWriter; prw != nil {
		c.Stdout = prw.File
	}
	return c
}
