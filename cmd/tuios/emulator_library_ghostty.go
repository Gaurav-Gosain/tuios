//go:build ghostty

package main

import gh "go.mitchellh.com/libghostty"

func emulatorLibrary() string { return "libghostty-vt " + gh.Version() }
