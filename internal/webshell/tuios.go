package webshell

import (
	"path"
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// cmdTuios is the tuios command inside the demo. tape play hands a tape to
// the tuios this shell runs in, tml shows and draws TML surfaces (see
// surfaces.go), and mermaid draws diagrams (see diagrams.go). The rest say
// what they would do on a real machine.
func cmdTuios(s *shell, args []string, _ string) int {
	t := s.t
	sub := argOr(args, 1, "")
	switch sub {
	case "", "help", "--help", "-h":
		intro := strings.Fields("You are already in tuios. Press " + bold + "Ctrl+B" + reset + " then " + bold + "?" + reset + " to see every key.")
		real := []string{"tuios ls", "tuios new", "tuios hosts", "tuios task", "tuios look list", "tuios fan", "tuios worktree", "tuios list-agents", "tuios list-verbs", "tuios list-hooks"}
		var tokens []string
		for i, name := range real {
			tok := green + name + reset
			if i < len(real)-1 {
				tok += ","
			}
			tokens = append(tokens, tok)
		}
		t.Lay(func(cols int) []string {
			lines := wrapTokens(intro, cols)
			lines = append(lines, "", "In this shell:")
			lines = append(lines, table([][2]string{
				{"tuios tape play demo.tape", "watch tuios drive itself"},
				{"tuios tape list", "the tapes here"},
				{"tuios ship", "commit and ship a worktree's change"},
				{"tuios theme share gilt", "a theme as one line to paste"},
				{"tuios tml show deploy.tml", "open a form, drawn by tuios"},
				{"tuios tml render status.tml", "print a surface as text"},
				{"tuios mermaid flow.mmd", "draw a diagram as text"},
				{"tuios version", ""},
			}, 2, green, cols)...)
			lines = append(lines, "")
			for _, l := range wrapWords("These show what they print on a real machine:", cols) {
				lines = append(lines, dim+l+reset)
			}
			for _, l := range wrapTokens(tokens, cols-2) {
				lines = append(lines, "  "+l)
			}
			return lines
		})
		return 0
	case "version", "--version", "-v":
		t.Print(text("tuios (browser demo, the real thing compiled to WebAssembly)") + "\r\n")
		return 0
	case "tape":
		return tuiosTape(s, args[2:])
	case "ship":
		return tuiosShip(s, args[2:])
	case "theme":
		if argOr(args, 2, "") == "share" {
			return tuiosThemeShare(s, args[3:])
		}
	case "tml":
		return cmdTuiosTML(s, args[2:])
	case "mermaid":
		return cmdMermaid(s, args[2:])
	}
	if printSample(t, args) {
		return 0
	}
	t.Print(yellow + "tuios " + sub + reset + " needs a real machine. Install tuios to try it: " + bold + "tuios.dev" + reset + "\r\n")
	return 0
}

// tuiosThemeShare is tuios theme share: the real share line, from the theme
// package the tour runs on. A theme saved on a real machine is not here, so
// only the shipped themes share.
func tuiosThemeShare(s *shell, args []string) int {
	if len(args) != 1 {
		return s.fail("Which theme? Try " + bold + "tuios theme share gilt" + reset)
	}
	line, err := theme.ShareString(args[0])
	if err != nil {
		return s.fail("tuios: " + err.Error())
	}
	s.t.Print(line + "\r\n")
	return 0
}

func tuiosTape(s *shell, args []string) int {
	t := s.t
	sub := argOr(args, 0, "")
	switch sub {
	case "play", "run":
		if len(args) < 2 {
			return s.fail("Which tape? Try " + bold + "tuios tape play demo.tape" + reset)
		}
		p := resolve(s.cwd, args[1])
		script, ok := readFile(p)
		if !ok {
			return s.fail("tuios: no tape at " + args[1] + ". Try " + bold + "tuios tape list" + reset)
		}
		name := path.Base(p)
		t.Print(green + ">" + reset + " Playing " + bold + name + reset + ". Sit back.\r\n")
		t.Emit(EventTapePlay, map[string]any{"name": name, "script": script})
		return 0
	case "list", "ls", "":
		names := tapeFiles()
		if len(names) == 0 {
			t.Print("No tapes here.\r\n")
			return 0
		}
		var lines []string
		for _, n := range names {
			lines = append(lines, "  "+green+prettyPath(n)+reset)
		}
		t.Lay(func(cols int) []string {
			out := fitLines(lines, cols)
			for _, l := range wrapWords("Play one with: tuios tape play <file>", cols) {
				out = append(out, dim+l+reset)
			}
			return out
		})
		return 0
	}
	t.Print(yellow + "tuios tape " + sub + reset + " needs a real machine. In the demo, try " + bold + "tuios tape play demo.tape" + reset + "\r\n")
	return 0
}

// tapeFiles lists every .tape file in the filesystem.
func tapeFiles() []string {
	fsMu.RLock()
	defer fsMu.RUnlock()
	var out []string
	for p := range files {
		if strings.HasSuffix(p, ".tape") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
