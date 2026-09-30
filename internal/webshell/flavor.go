package webshell

import "strings"

// The shell was written for the browser tour, and a few of its lines say so.
// The SSH tour (cmd/tuios-learn) runs the same shell on a server, where those
// lines would be wrong. SetFlavorSSH swaps them.

var sshText = map[string]string{
	"Nothing leaves this tab.": "Nothing leaves this server.",
	"your browser":             "a tuios server",
	"WebAssembly":              "Linux (no real shell)",
	"js/wasm":                  "linux",
	"A window manager in a browser tab. What a time to be alive.":  "A window manager over ssh. What a time to be alive.",
	"tuios (browser demo, the real thing compiled to WebAssembly)": "tuios (SSH demo, the real thing with a pretend shell)",
}

var flavorSSH bool

// SetFlavorSSH makes the shell describe itself as the SSH demo. Call it once,
// before the first pane starts.
func SetFlavorSSH() {
	flavorSSH = true
	fsMu.Lock()
	defer fsMu.Unlock()
	readme := Home + "/README.md"
	if c, ok := files[readme]; ok {
		n := strings.Replace(c, "Everything here runs in your browser", "Everything here runs on the server you reached with ssh", 1)
		n = strings.Replace(n, "Nothing to install, nothing sent anywhere.", "Nothing to install, and no real shell.", 1)
		filesSize += len(n) - len(c)
		files[readme] = n
	}
}

// text is s, or its SSH wording when the shell runs as the SSH demo.
func text(s string) string {
	if flavorSSH {
		if r, ok := sshText[s]; ok {
			return r
		}
	}
	return s
}
