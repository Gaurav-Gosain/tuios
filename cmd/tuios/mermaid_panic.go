//go:build mermaidpanic && !windows && !js

package main

// framePanicAt makes the live view of tuios mermaid panic on this frame,
// inside the frame encoder. Only a build with -tags mermaidpanic has it: the
// end-to-end test TestMermaidLiveGivesTheTerminalBack builds one to prove the
// terminal comes back after a crash. A release build has no way to set it.
const framePanicAt = 12
