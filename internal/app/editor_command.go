package app

import "os/exec"

func (m *OS) editorCommand(c *exec.Cmd) *exec.Cmd {
	if prw := m.PostRenderWriter; prw != nil && prw.File() != nil {
		c.Stdout = prw.File()
	}
	return c
}
