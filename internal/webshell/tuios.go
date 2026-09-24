package webshell

import (
	"path"
	"sort"
	"strings"
)

// cmdTuios is the tuios command inside the demo. The one subcommand that does
// something is tape play, which hands a tape to the tuios this shell runs in.
// The rest say what they would do on a real machine.
func cmdTuios(s *shell, args []string, _ string) int {
	t := s.t
	sub := argOr(args, 1, "")
	switch sub {
	case "", "help", "--help", "-h":
		t.Print("You are already in tuios. Press " + bold + "Ctrl+B" + reset + " then " + bold + "?" + reset + " to see every key.\r\n\r\n")
		t.Print("In this shell:\r\n")
		t.Print("  " + green + "tuios tape play demo.tape" + reset + "  watch tuios drive itself\r\n")
		t.Print("  " + green + "tuios tape list" + reset + "            the tapes here\r\n")
		t.Print("  " + green + "tuios version" + reset + "\r\n")
		t.Print("\r\n" + dim + "These show what they print on a real machine:" + reset + "\r\n")
		t.Print("  " + green + "tuios ls" + reset + ", " + green + "tuios fan" + reset + ", " + green + "tuios worktree" + reset + ", " +
			green + "tuios list-agents" + reset + ", " + green + "tuios list-verbs" + reset + ", " + green + "tuios list-hooks" + reset + "\r\n")
		return 0
	case "version", "--version", "-v":
		t.Print("tuios (browser demo, the real thing compiled to WebAssembly)\r\n")
		return 0
	case "tape":
		return tuiosTape(s, args[2:])
	}
	if printSample(t, sub) {
		return 0
	}
	t.Print(yellow + "tuios " + sub + reset + " needs a real machine. Install tuios to try it: " + bold + "tuios.dev" + reset + "\r\n")
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
		t.Print(green + "▶" + reset + " Playing " + bold + name + reset + ". Sit back.\r\n")
		t.Emit(EventTapePlay, map[string]any{"name": name, "script": script})
		return 0
	case "list", "ls", "":
		names := tapeFiles()
		if len(names) == 0 {
			t.Print("No tapes here.\r\n")
			return 0
		}
		for _, n := range names {
			t.Print("  " + green + prettyPath(n) + reset + "\r\n")
		}
		t.Print(dim + "Play one with: tuios tape play <file>" + reset + "\r\n")
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
