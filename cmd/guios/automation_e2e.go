//go:build guiose2e

package main

// The automation socket for scripted checks and frames: MYGO_AUTOMATION
// names it. Only a build with -tags guiose2e has it.
import _ "github.com/egoist/mygo/plugins/automation"
