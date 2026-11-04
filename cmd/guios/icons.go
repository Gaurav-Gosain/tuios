package main

import (
	"embed"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// The icons are Framework7's, drawn after SF Symbols so that they feel at
// home on macOS, a few of other sets where it has none, and programs'
// logos from Simple Icons, as Iconify serves them: see icons/README.md.
//
//go:embed icons/*.svg
var iconFiles embed.FS

// glyph returns an icon of the set, as "f7-gear-alt".
func glyph(name string) *ui.SVG {
	b, err := iconFiles.ReadFile("icons/" + name + ".svg")
	if err != nil {
		panic("kero: no icon " + name)
	}
	return ui.MustParseSVG(b)
}

// icon draws an icon of kero's own, on 16, as a status glyph.
func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.35" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

var (
	sidebarIcon      = glyph("f7-sidebar-left")
	sidebarRightIcon = glyph("f7-sidebar-right")
	fileIcon         = glyph("f7-doc")
	newFileIcon      = glyph("f7-doc-badge-plus")
	newFolderIcon    = glyph("f7-folder-badge-plus")
	plusIcon         = glyph("f7-plus")
	chevronIcon      = glyph("f7-chevron-right")
	chevronUpIcon    = glyph("f7-chevron-up")
	chevronDownIcon  = glyph("f7-chevron-down")
	folderIcon       = glyph("f7-folder")
	searchIcon       = glyph("f7-search")
	terminalIcon     = glyph("meteor-terminal")
	returnIcon       = glyph("f7-return")
	gearIcon         = glyph("f7-gear-alt")
	splitRightIcon   = glyph("f7-square-split-2x1")
	splitDownIcon    = glyph("f7-square-split-1x2")
	zoomIcon         = glyph("f7-arrow-up-left-arrow-down-right")
	unzoomIcon       = glyph("f7-arrow-down-right-arrow-up-left")
	closeIcon        = glyph("f7-xmark")
	swapIcon         = glyph("f7-arrow-right-arrow-left")
	usageIcon        = glyph("usage-wave")
	branchIcon       = glyph("f7-arrow-branch")
	diffIcon         = glyph("octicon-file-diff-16")
	collapseIcon     = glyph("f7-rectangle-compress-vertical")
	expandIcon       = glyph("f7-rectangle-expand-vertical")
	revertIcon       = glyph("f7-arrow-uturn-left")
	openFileIcon     = glyph("f7-arrow-up-right-square")
	globeIcon        = glyph("f7-globe")
	chevronLeftIcon  = glyph("f7-chevron-left")
	reloadIcon       = glyph("f7-arrow-clockwise")
	copyIcon         = glyph("f7-doc-on-doc")
	linkIcon         = glyph("f7-arrow-up-right")
	editIcon         = glyph("f7-pencil")
	agentsIcon       = glyph("f7-sparkles")
	archiveIcon      = icon(`<rect x="2" y="3" width="12" height="3" rx="1"/><path d="M3 6v6.5a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V6M6.5 9h3"/>`)
)

// programIcons are the logos of programs a pane may run, by the name kerod
// gives the foreground program, its agent or its script's runtime.
var programIcons = map[string]*ui.SVG{}

func init() {
	for name, logo := range map[string]string{
		"claude": "claude", "codex": "openai", "gemini": "googlegemini", "opencode": "opencode",
		"amp": "amp", "cursor-agent": "cursor", "copilot": "githubcopilot", "qwen": "qwen",
		"kimi": "kimi", "cline": "cline",
		"node": "nodedotjs", "bun": "bun", "deno": "deno", "python": "python", "python3": "python",
		"vim": "vim", "nvim": "neovim", "git": "git", "docker": "docker",
	} {
		programIcons[name] = glyph("si-" + logo)
	}
}

// programIcon is the logo of the pane's foreground program, as Claude's
// for Claude Code and Node.js's for a script node runs; the terminal's
// while the shell is in the foreground or the program has none; a globe
// for a browser.
func programIcon(p *proto.Pane) *ui.SVG {
	if p.Browser() {
		return globeIcon
	}
	for _, name := range []string{p.Agent, p.Process, p.Runtime} {
		if i := programIcons[name]; i != nil {
			return i
		}
	}
	return terminalIcon
}
