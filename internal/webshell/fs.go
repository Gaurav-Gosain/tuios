package webshell

import (
	"path"
	"sort"
	"strings"
	"sync"
)

// fsMu guards files and fsDirs. Every pane shares the one filesystem.
var fsMu sync.RWMutex

// Home is the fake user's home directory.
const Home = "/home/guest"

// files is the whole fake filesystem. Directories are implied by the paths.
// Every pane shares it, so a file one pane creates is visible in the others.
var files = map[string]string{
	Home + "/README.md": `# Welcome to tuios

tuios is a terminal window manager. Everything you see here is
running in your browser: the window manager, this shell, all of it.

Try these:
  ls, cd projects, cat notes.txt
  top      a live process monitor
  rain     digital rain
  neofetch system info, the pretty way
  help     everything this shell knows
`,
	Home + "/notes.txt": `Things to remember
- Ctrl+B is the prefix key. Press it, let go, then press the next key.
- Esc leaves terminal mode so you can move windows around.
- i goes back into terminal mode.
`,
	Home + "/todo.md": `- [x] open tuios
- [ ] open a second window
- [ ] tile them
- [ ] switch to workspace 2
`,
	Home + "/projects/hello.go": `package main

import "fmt"

func main() {
	fmt.Println("hello from tuios")
}
`,
	Home + "/projects/website/index.html": `<!doctype html>
<title>hi</title>
<h1>it works</h1>
`,
	Home + "/projects/website/style.css": `h1 { color: hotpink; }
`,
	Home + "/.config/tuios/config.toml": `[appearance]
border_style = "rounded"
theme = "catppuccin-mocha"

[keybindings]
leader_key = "ctrl+b"
`,
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
