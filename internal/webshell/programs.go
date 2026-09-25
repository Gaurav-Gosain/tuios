package webshell

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"
	"unicode/utf8"
)

var logo = []string{
	`████████╗██╗   ██╗██╗ ██████╗ ███████╗`,
	`╚══██╔══╝██║   ██║██║██╔═══██╗██╔════╝`,
	`   ██║   ██║   ██║██║██║   ██║███████╗`,
	`   ██║   ██║   ██║██║██║   ██║╚════██║`,
	`   ██║   ╚██████╔╝██║╚██████╔╝███████║`,
	`   ╚═╝    ╚═════╝ ╚═╝ ╚═════╝ ╚══════╝`,
}

var started = time.Now()

func cmdNeofetch(t *TTY, _ []string) int {
	cols, _ := t.Size()
	palette := []string{"\x1b[38;5;213m", "\x1b[38;5;177m", "\x1b[38;5;141m", "\x1b[38;5;105m", "\x1b[38;5;69m", "\x1b[38;5;39m"}
	info := []string{
		bold + "guest" + reset + "@" + bold + "tuios" + reset,
		dim + "───────────" + reset,
		cyan + "OS" + reset + "      your browser",
		cyan + "Kernel" + reset + "  WebAssembly",
		cyan + "Uptime" + reset + "  " + time.Since(started).Round(time.Second).String(),
		cyan + "Shell" + reset + "   webshell",
		cyan + "WM" + reset + "      tuios",
	}
	wide := cols >= 64
	for i := 0; i < max(len(logo), len(info)); i++ {
		if i < len(logo) && wide {
			t.Print(palette[i%len(palette)] + logo[i] + reset + "  ")
		} else if wide {
			t.Print(strings.Repeat(" ", 40))
		}
		if i < len(info) {
			t.Print(info[i])
		}
		t.Print("\r\n")
	}
	if !wide {
		t.Print(dim + "(widen the window to see the logo)" + reset + "\r\n")
	}
	var sw strings.Builder
	for c := range 8 {
		sw.WriteString(fmt.Sprintf("\x1b[4%dm   ", c))
	}
	t.Print(sw.String() + reset + "\r\n")
	return 0
}

func cmdColors(t *TTY, _ []string) int {
	for row := range 2 {
		for c := range 8 {
			bg := 40 + c
			if row == 1 {
				bg = 100 + c
			}
			t.Printf("\x1b[%d;97m %2d ", bg, c+8*row)
		}
		t.Print(reset + "\r\n")
	}
	for i := range 36 {
		t.Printf("\x1b[48;5;%dm  ", 16+i*6)
	}
	t.Print(reset + "\r\n")
	return 0
}

// fullscreen runs a program on the alternate screen with the cursor hidden
// and restores both however it exits.
func fullscreen(t *TTY, frame func(cols, rows int, tick int) string, every time.Duration, quitOnAnyKey bool) int {
	t.Print("\x1b[?1049h\x1b[?25l\x1b[H\x1b[2J")
	defer t.Print("\x1b[?25h\x1b[?1049l")
	tick := 0
	draw := func() {
		cols, rows := t.Size()
		t.Print("\x1b[H" + frame(cols, rows, tick))
		tick++
	}
	draw()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case b, ok := <-t.In:
			if !ok {
				return 0
			}
			for _, c := range b {
				if quitOnAnyKey || c == 'q' || c == 0x03 || c == 0x1b {
					return 0
				}
			}
		case <-t.Resized():
			t.Print("\x1b[2J")
			draw()
		case <-ticker.C:
			draw()
		}
	}
}

type fakeProc struct {
	pid  int
	name string
	cpu  float64
	mem  float64
}

func cmdTop(t *TTY, _ []string) int {
	procs := []fakeProc{
		{1, "init", 0.1, 0.2}, {42, "tuios", 3.5, 2.1}, {101, "webshell", 0.4, 0.3},
		{137, "top", 1.2, 0.2}, {256, "gopls", 8.0, 6.3}, {512, "node", 12.0, 9.8},
		{777, "cargo", 25.0, 4.4}, {1024, "postgres", 2.0, 5.1}, {2048, "redis", 0.7, 1.0},
		{4096, "nginx", 0.3, 0.6}, {8192, "agent", 6.0, 3.3},
	}
	cpus := make([]float64, 8)
	return fullscreen(t, func(cols, rows, tick int) string {
		var b strings.Builder
		up := time.Since(started).Round(time.Second)
		b.WriteString(bold + "top" + reset + dim + " - up " + up.String() + ", 1 user, load average: 0.42 0.37 0.31" + reset + "\x1b[K\r\n")
		// Each bar is its width plus 13 cells of label, brackets and percent,
		// and two sit on a row with a two-cell gap.
		barW := max((cols-28)/2, 4)
		for i := range cpus {
			target := 15 + 60*math.Abs(math.Sin(float64(tick+i*3)/7)) + rand.Float64()*10
			cpus[i] += (target - cpus[i]) * 0.5
		}
		for i := 0; i < len(cpus); i += 2 {
			b.WriteString(bar(fmt.Sprintf("%d", i), cpus[i], barW))
			b.WriteString("  ")
			b.WriteString(bar(fmt.Sprintf("%d", i+1), cpus[i+1], barW))
			b.WriteString("\x1b[K\r\n")
		}
		mem := 42 + 6*math.Sin(float64(tick)/11)
		b.WriteString(bar("Mem", mem, barW*2+15) + "\x1b[K\r\n\x1b[K\r\n")
		b.WriteString("\x1b[30;42m" + padRight("  PID USER      CPU%  MEM%  COMMAND", cols) + reset + "\r\n")
		for i := range procs {
			procs[i].cpu = max(0, procs[i].cpu+(rand.Float64()-0.5)*4)
		}
		for i, p := range procs {
			if 8+i >= rows {
				break
			}
			colour := ""
			if p.cpu > 20 {
				colour = red
			} else if p.cpu > 8 {
				colour = yellow
			}
			b.WriteString(fmt.Sprintf("%5d guest   %s%5.1f%s %5.1f  %s\x1b[K\r\n", p.pid, colour, p.cpu, reset, p.mem, p.name))
		}
		b.WriteString("\x1b[J")
		b.WriteString(fmt.Sprintf("\x1b[%d;1H%s q to quit %s", rows, "\x1b[7m", reset))
		return b.String()
	}, 500*time.Millisecond, false)
}

func bar(label string, pct float64, width int) string {
	pct = min(max(pct, 0), 100)
	fill := int(pct / 100 * float64(width))
	colour := green
	if pct > 80 {
		colour = red
	} else if pct > 50 {
		colour = yellow
	}
	return fmt.Sprintf("%s%3s%s[%s%s%s%s] %5.1f%%", cyan, label, reset, colour, strings.Repeat("|", fill), reset, strings.Repeat(" ", width-fill), pct)
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s[:w]
	}
	return s + strings.Repeat(" ", w-len(s))
}

func cmdRain(t *TTY, _ []string) int {
	glyphs := []rune("ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄ0123456789$+-*/=<>")
	var drops []float64
	var speeds []float64
	return fullscreen(t, func(cols, rows, tick int) string {
		if len(drops) != cols {
			drops = make([]float64, cols)
			speeds = make([]float64, cols)
			for i := range drops {
				drops[i] = -rand.Float64() * float64(rows)
				speeds[i] = 0.3 + rand.Float64()*0.9
			}
		}
		var b strings.Builder
		for x := 0; x < cols; x += 2 {
			head := int(drops[x])
			for k := range 8 {
				y := head - k
				if y < 0 || y >= rows {
					continue
				}
				g := glyphs[rand.IntN(len(glyphs))]
				switch {
				case k == 0:
					b.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[1;97m%c", y+1, x+1, g))
				case k < 4:
					b.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[0;92m%c", y+1, x+1, g))
				default:
					b.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[0;32m%c", y+1, x+1, g))
				}
			}
			if y := head - 8; y >= 0 && y < rows {
				b.WriteString(fmt.Sprintf("\x1b[%d;%dH  ", y+1, x+1))
			}
			drops[x] += speeds[x]
			if head-8 > rows {
				drops[x] = -rand.Float64() * 10
			}
		}
		b.WriteString(reset)
		return b.String()
	}, 60*time.Millisecond, true)
}

var fortunes = []string{
	"A window manager in a browser tab. What a time to be alive.",
	"Ctrl+B is not a bookmark here. It is a way of life.",
	"The best terminal is the one you have open.",
	"You will tile many windows. Some of them on purpose.",
	"There is no place like ~.",
	"Real programmers read the docs. After trying everything else.",
	"Today is a good day to split a pane.",
	"An agent that asks first is an agent worth keeping.",
}

func cmdFortune(t *TTY, _ []string) int {
	t.Print(fortunes[rand.IntN(len(fortunes))] + "\r\n")
	return 0
}

func cmdCowsay(t *TTY, args []string) int {
	text := strings.Join(args, " ")
	if text == "" {
		text = "moo. try cowsay hello"
	}
	n := utf8.RuneCountInString(text)
	t.Print(" " + strings.Repeat("_", n+2) + "\r\n")
	t.Print("< " + text + " >\r\n")
	t.Print(" " + strings.Repeat("-", n+2) + "\r\n")
	for _, l := range []string{
		`        \   ^__^`,
		`         \  (oo)\_______`,
		`            (__)\       )\/\`,
		`                ||----w |`,
		`                ||     ||`,
	} {
		t.Print(l + "\r\n")
	}
	return 0
}

func cmdTree(s *shell, args []string, _ string) int {
	root := s.cwd
	if len(args) > 1 {
		root = resolve(s.cwd, args[1])
	}
	if !isDir(root) {
		return s.fail("tree: " + argOr(args, 1, "") + " is not a directory")
	}
	s.t.Print(blue + bold + prettyPath(root) + reset + "\r\n")
	dirs, nfiles := 0, 0
	var walk func(dir, indent string)
	walk = func(dir, indent string) {
		entries := list(dir)
		var shown []entry
		for _, e := range entries {
			if !strings.HasPrefix(e.name, ".") {
				shown = append(shown, e)
			}
		}
		for i, e := range shown {
			branch, next := "├── ", "│   "
			if i == len(shown)-1 {
				branch, next = "└── ", "    "
			}
			s.t.Print(dim + indent + branch + reset + colourName(e) + "\r\n")
			if e.dir {
				dirs++
				walk(dir+"/"+e.name, indent+next)
			} else {
				nfiles++
			}
		}
	}
	walk(strings.TrimSuffix(root, "/"), "")
	s.t.Printf("\r\n%d directories, %d files\r\n", dirs, nfiles)
	return 0
}

// cmdGo runs the tiny Go project the only ways worth faking: run, test,
// build and version. It reads greet.go, so a change there shows up here.
func cmdGo(s *shell, args []string, _ string) int {
	sub := argOr(args, 1, "")
	if sub == "version" {
		s.t.Print("go version go1.25 js/wasm\r\n")
		return 0
	}
	if sub == "" || sub == "help" {
		s.t.Print("Try " + bold + "go run ." + reset + ", " + bold + "go test" + reset + " or " + bold + "go build" + reset + " in ~/projects/hello.\r\n")
		return 0
	}
	if !inRepo(s.cwd) {
		return s.fail("go: no go.mod here. The Go project is in " + bold + "~/projects/hello" + reset)
	}
	greet, _ := readFile(ProjectDir + "/greet.go")
	bang := strings.Contains(greet, `+ "!"`)
	switch sub {
	case "run":
		name := "world"
		for _, a := range args[2:] {
			if a != "." && !strings.HasSuffix(a, ".go") {
				name = a
				break
			}
		}
		out := "hello, " + name
		if bang {
			out += "!"
		}
		s.t.Print(out + "\r\n")
		return 0
	case "build", "vet":
		return 0
	case "test":
		if bang {
			s.t.Print("--- FAIL: TestGreet (0.00s)\r\n")
			s.t.Print(`    greet_test.go:7: greet = "hello, tuios!"` + "\r\n")
			s.t.Print(red + "FAIL" + reset + "\r\n")
			s.t.Print("FAIL\texample.com/hello\t0.004s\r\n")
			s.t.Print(dim + "(the change in greet.go broke the test. git diff shows it, git restore greet.go undoes it)" + reset + "\r\n")
			return 1
		}
		s.t.Print(green + "ok" + reset + "  \texample.com/hello\t0.004s\r\n")
		return 0
	}
	return s.fail("go " + sub + ": not in the demo. Try go run . or go test")
}
