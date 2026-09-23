package webshell

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"
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
	for c := 0; c < 8; c++ {
		sw.WriteString(fmt.Sprintf("\x1b[4%dm   ", c))
	}
	t.Print(sw.String() + reset + "\r\n")
	return 0
}

func cmdColors(t *TTY, _ []string) int {
	for row := 0; row < 2; row++ {
		for c := 0; c < 8; c++ {
			bg := 40 + c
			if row == 1 {
				bg = 100 + c
			}
			t.Printf("\x1b[%d;97m %2d ", bg, c+8*row)
		}
		t.Print(reset + "\r\n")
	}
	for i := 0; i < 36; i++ {
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
	t.Emit("program.start", map[string]string{"program": "top"})
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
	t.Emit("program.start", map[string]string{"program": "rain"})
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
			for k := 0; k < 8; k++ {
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

// cmdAgent pretends to be a coding agent: it reads, thinks with a spinner,
// streams a plan, and then waits for an answer. A track about agents uses it
// to show what a pane needing attention looks like.
func cmdAgent(t *TTY, args []string) int {
	task := strings.Join(args, " ")
	if task == "" {
		task = "add a dark mode toggle"
	}
	t.Emit("agent.state", map[string]string{"state": "working", "task": task})
	t.Print(purple + bold + "✻ agent" + reset + dim + "  task: " + task + reset + "\r\n\r\n")
	steps := []string{"Reading README.md", "Reading projects/website/index.html", "Reading projects/website/style.css"}
	for _, s := range steps {
		t.Print(green + "● " + reset + s + "\r\n")
		if !sleepOrInterrupt(t, 350*time.Millisecond) {
			return 130
		}
	}
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	for i := 0; i < 24; i++ {
		t.Print("\r" + purple + spinner[i%len(spinner)] + reset + dim + " thinking" + strings.Repeat(".", i/6%4) + "   " + reset)
		if !sleepOrInterrupt(t, 80*time.Millisecond) {
			t.Print("\r\n")
			return 130
		}
	}
	t.Print("\r\x1b[K")
	plan := "I will add a toggle button to index.html, a [data-theme=dark] block to style.css, and ten lines of script to remember the choice."
	for _, w := range strings.Fields(plan) {
		t.Print(w + " ")
		if !sleepOrInterrupt(t, 40*time.Millisecond) {
			return 130
		}
	}
	t.Print("\r\n\r\n" + yellow + bold + "? " + reset + "Apply these changes? " + dim + "[y/n]" + reset + " ")
	t.Emit("agent.state", map[string]string{"state": "waiting", "task": task})
	// A bell is how a real agent asks for attention, and tuios shows it.
	t.Print("\a")
	for b := range t.In {
		for _, c := range b {
			switch c {
			case 'y', 'Y':
				t.Print("y\r\n" + green + "✓ " + reset + "Edited 2 files. Done.\r\n")
				t.Emit("agent.state", map[string]string{"state": "done", "task": task})
				return 0
			case 'n', 'N', 0x03:
				t.Print("n\r\n" + dim + "Left everything as it was." + reset + "\r\n")
				t.Emit("agent.state", map[string]string{"state": "done", "task": task})
				return 1
			}
		}
	}
	return 0
}

// sleepOrInterrupt waits, and reports false when Ctrl+C arrived instead.
func sleepOrInterrupt(t *TTY, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return true
		case b, ok := <-t.In:
			if !ok {
				return false
			}
			for _, c := range b {
				if c == 0x03 {
					t.Print("^C\r\n")
					return false
				}
			}
		}
	}
}
