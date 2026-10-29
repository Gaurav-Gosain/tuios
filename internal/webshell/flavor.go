package webshell

import "strings"

// The shell was written for the browser tour, and a few of its lines say so.
// tuios tutor runs the same shell in a real terminal, where those lines are
// wrong. SetFlavorTerminal swaps them for the terminal's words.

// terminalText is filled by SetFlavorTerminal, so a process that never
// runs the tutor builds no table.
var terminalText map[string]string

func terminalWords() map[string]string {
	return map[string]string{
		"Nothing leaves this tab.": "Nothing leaves this tutor.",
		"your browser":             "tuios tutor",
		"WebAssembly":              "none, this shell is pretend",
		"js/wasm":                  "tutor",
		"A window manager in a browser tab. What a time to be alive.":  "A window manager that teaches itself. What a time to be alive.",
		"tuios (browser demo, the real thing compiled to WebAssembly)": "tuios (the tutor, the real thing with a pretend shell)",
		"browser demo": "tutor",
		"web shell.":   "practice shell.",
		"the browser draws text only. Drop the format and out flags.":                                                                  "the tutor draws text only. Drop the format and out flags.",
		"push and pr show what they send and ask first. In this demo nothing leaves the tab.":                                          "push and pr show what they send and ask first. In the tutor nothing leaves this shell.",
		"This is a demo, so nothing leaves the tab. On a real machine, you say yes, and it prints:":                                    "This is the tutor, so nothing leaves this shell. On a real machine, you say yes, and it prints:",
		"Everything here runs in your browser: the window manager, this\nshell, all of it. Nothing to install, nothing sent anywhere.": "This is the tutor: a real tuios with a pretend shell. The files\nare a practice copy. Nothing on your computer changes.",
	}
}

// flavorTerminal is set once, before the first pane starts, and only read
// after that.
var flavorTerminal bool

// SetFlavorTerminal makes the shell describe itself as tuios tutor. Call it
// once, before the first pane starts.
func SetFlavorTerminal() {
	ready()
	terminalText = terminalWords()
	flavorTerminal = true
	fsMu.Lock()
	defer fsMu.Unlock()
	readme := Home + "/README.md"
	if c, ok := files[readme]; ok {
		for from, to := range terminalText {
			if strings.Contains(from, "\n") {
				c = strings.Replace(c, from, to, 1)
			}
		}
		files[readme] = c
	}
}

// text is s, or its terminal wording when the shell runs as the tutor.
func text(s string) string {
	if flavorTerminal {
		if r, ok := terminalText[s]; ok {
			return r
		}
	}
	return s
}
