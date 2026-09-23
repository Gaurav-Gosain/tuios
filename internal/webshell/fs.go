package webshell

import (
	"path"
	"sort"
	"strings"
	"sync"
)

// fsMu guards files, fsDirs and the fake git repository. Every pane shares
// the one filesystem.
var fsMu sync.RWMutex

// Home is the fake user's home directory.
const Home = "/home/guest"

// ProjectDir is the tiny Go project, which is also the fake git repository.
const ProjectDir = Home + "/projects/hello"

// The files of the Go project as the last commit left them. The working tree
// starts with a change on top, so git status and git diff have something to
// show straight away.
const (
	helloGoMod = `module example.com/hello

go 1.25
`
	helloMain = `package main

import (
	"fmt"
	"os"
)

func main() {
	name := "world"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	fmt.Println(greet(name))
}
`
	helloGreetHead = `package main

// greet says hello to name.
func greet(name string) string {
	return "hello, " + name
}
`
	helloGreetWork = `package main

import "strings"

// greet says hello to name.
func greet(name string) string {
	return "hello, " + name + "!"
}

// shout says hello, loudly.
func shout(name string) string {
	return strings.ToUpper(greet(name))
}
`
	helloTest = `package main

import "testing"

func TestGreet(t *testing.T) {
	if got := greet("tuios"); got != "hello, tuios" {
		t.Fatalf("greet = %q", got)
	}
}
`
	helloReadme = `# hello

A tiny Go program for the tuios tour.

    go run .          # hello, world
    go run . tuios    # hello, tuios
    go test           # runs greet_test.go

Things to try here:
  git log     the history
  git status  what changed
  git diff    the change itself
  less greet.go
`
)

// files is the whole fake filesystem. Directories are implied by the paths.
// Every pane shares it, so a file one pane creates is visible in the others.
var files = map[string]string{
	Home + "/README.md": `# Welcome to tuios

Everything here runs in your browser: the window manager, this
shell, all of it. Nothing to install, nothing sent anywhere.

Things to try:
  ls                   look around
  cd projects/hello    a tiny Go project with git history
  less README.md       read a file (q to quit)
  claude               a pretend coding agent
  top                  a live process monitor
  tuios tape play demo.tape
                       watch tuios drive itself
  help                 everything this shell knows
`,
	Home + "/notes.txt": `Things to remember
- Ctrl+B is the prefix key. Press it, let go, then press the next key.
- Ctrl+B then Esc leaves typing mode, so you can move windows around.
- i goes back to typing in the window.
- Ctrl+B then ? shows every key.
`,
	Home + "/todo.md": `- [x] open tuios
- [ ] open a second window
- [ ] tile them
- [ ] switch to workspace 2
- [ ] let the agent ask for something
`,
	Home + "/demo.tape": `# A short tour that tuios plays by itself.
# Run it with: tuios tape play demo.tape
Sleep 600ms
WindowManagementMode
EnableTiling
NewWindow
Sleep 700ms
ToggleZoom
Sleep 400ms
TerminalMode
Type "neofetch"
Enter
Sleep 1800ms
WindowManagementMode
ToggleZoom
Sleep 400ms
NewWindow
Sleep 700ms
TerminalMode
Type "cd projects/hello && git log --oneline"
Enter
Sleep 1500ms
WindowManagementMode
ToggleZoom
Sleep 1s
ToggleZoom
Sleep 500ms
NextWindow
Sleep 500ms
TerminalMode
`,
	Home + "/party.tape": `# Three programs, a zoom and a trip to workspace 2, all typed by tuios.
# Run it with: tuios tape play party.tape
WindowManagementMode
EnableTiling
NewWindow
Sleep 600ms
TerminalMode
Type "rain"
Enter
Sleep 1200ms
WindowManagementMode
NewWindow
Sleep 600ms
TerminalMode
Type "top"
Enter
Sleep 1200ms
WindowManagementMode
NewWindow
Sleep 600ms
TerminalMode
Type "cowsay tapes type for you"
Enter
Sleep 1500ms
WindowManagementMode
ToggleZoom
Sleep 1s
ToggleZoom
Sleep 500ms
SwitchWorkspace 2
Sleep 700ms
NewWindow
Sleep 600ms
TerminalMode
Type "neofetch"
Enter
Sleep 300ms
`,
	ProjectDir + "/go.mod":        helloGoMod,
	ProjectDir + "/main.go":       helloMain,
	ProjectDir + "/greet.go":      helloGreetWork,
	ProjectDir + "/greet_test.go": helloTest,
	ProjectDir + "/README.md":     helloReadme,
	ProjectDir + "/TODO.md":       "- shout at people, but kindly\n",
	Home + "/projects/website/index.html": `<!doctype html>
<title>hi</title>
<link rel="stylesheet" href="style.css">
<h1>it works</h1>
`,
	Home + "/projects/website/style.css": `h1 { color: hotpink; }
`,
	ConfigPath:  configText("", "rounded", "default"),
	"/etc/motd": "Be kind to your terminal.\n",
}

var fsDirs = map[string]bool{}

func init() {
	for p := range files {
		addParents(p)
	}
}

func addParents(p string) {
	for d := path.Dir(p); ; d = path.Dir(d) {
		fsDirs[d] = true
		if d == "/" {
			break
		}
	}
}

func isDir(p string) bool {
	fsMu.RLock()
	defer fsMu.RUnlock()
	return fsDirs[p]
}

func readFile(p string) (string, bool) {
	fsMu.RLock()
	defer fsMu.RUnlock()
	s, ok := files[p]
	return s, ok
}

// writeFile creates or replaces a file. The parent has to exist.
func writeFile(p, content string, appendTo bool) bool {
	fsMu.Lock()
	defer fsMu.Unlock()
	if !fsDirs[path.Dir(p)] || fsDirs[p] {
		return false
	}
	if appendTo {
		content = files[p] + content
	}
	files[p] = content
	return true
}

// ConfigPath is the fake tuios config file.
const ConfigPath = Home + "/.config/tuios/config.toml"

// configText is the config file for these looks. An empty theme is tuios's
// own colours, and "default" glyphs are the shipped set, so neither is
// written.
func configText(theme, border, glyphs string) string {
	var b strings.Builder
	b.WriteString("[appearance]\n")
	if theme != "" {
		b.WriteString(`theme = "` + theme + "\"\n")
	}
	b.WriteString(`border_style = "` + border + "\"\n")
	if glyphs != "" && glyphs != "default" {
		b.WriteString(`glyphs = "` + glyphs + "\"\n")
	}
	b.WriteString("\n[keybindings]\nleader_key = \"ctrl+b\"\n")
	return b.String()
}

// WriteConfig rewrites the config file to match the looks the tour is
// showing, so cat config.toml shows what the reader picked.
func WriteConfig(theme, border, glyphs string) {
	if border == "" {
		border = "rounded"
	}
	fsMu.Lock()
	defer fsMu.Unlock()
	files[ConfigPath] = configText(theme, border, glyphs)
}

// ReadFile returns a file of the fake filesystem, for tests outside the
// package.
func ReadFile(p string) (string, bool) { return readFile(p) }

func mkdir(p string) bool {
	fsMu.Lock()
	defer fsMu.Unlock()
	if !fsDirs[path.Dir(p)] || fsDirs[p] {
		return false
	}
	if _, ok := files[p]; ok {
		return false
	}
	fsDirs[p] = true
	return true
}

func remove(p string) bool {
	fsMu.Lock()
	defer fsMu.Unlock()
	if _, ok := files[p]; ok {
		delete(files, p)
		return true
	}
	return false
}

// resolve turns a path typed at the prompt into an absolute path.
func resolve(cwd, p string) string {
	if p == "" || p == "~" {
		return Home
	}
	if strings.HasPrefix(p, "~/") {
		p = Home + p[1:]
	}
	if !strings.HasPrefix(p, "/") {
		p = cwd + "/" + p
	}
	return path.Clean(p)
}

// entry is one name in a directory listing.
type entry struct {
	name string
	dir  bool
}

func list(dir string) []entry {
	fsMu.RLock()
	defer fsMu.RUnlock()
	seen := map[string]bool{}
	var out []entry
	prefix := strings.TrimSuffix(dir, "/") + "/"
	add := func(p string, isDir bool) {
		if !strings.HasPrefix(p, prefix) || p == dir {
			return
		}
		rest := p[len(prefix):]
		name, _, more := strings.Cut(rest, "/")
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, entry{name: name, dir: isDir || more})
	}
	for p := range files {
		add(p, false)
	}
	for d := range fsDirs {
		add(d, true)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func prettyPath(p string) string {
	if p == Home {
		return "~"
	}
	if strings.HasPrefix(p, Home+"/") {
		return "~" + p[len(Home):]
	}
	return p
}
