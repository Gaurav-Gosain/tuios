package main

import (
	"embed"
	"strings"

	"github.com/egoist/mygo/ui"
)

// The trees of Files and Changes show each file's and folder's icon as
// VS Code does with an icon theme: Pierre's (icons/files, made with
// scripts/fileicons), in their own colors, for the light appearance or the
// dark one. A file's is that of its name, else of its longest extension,
// else the theme's file; a folder's that of its name, else the theme's
// folder, open or not.

//go:embed icons/files/*.svg
var fileIconFiles embed.FS

// iconTheme is an icon theme's icons, by the names, extensions and
// folders they are for, lowercase (fileicons_gen.go).
type iconTheme struct {
	file, folder, folderOpen, symlink string
	names, extensions, folders        map[string]string
}

// fileIconOf is the icon of a file of the theme.
func (t *iconTheme) fileIconOf(name string) string {
	name = strings.ToLower(name)
	if icon, ok := t.names[name]; ok {
		return icon
	}
	// The longest extension first: "d.ts" before "ts".
	for i := strings.IndexByte(name, '.'); i >= 0; {
		if icon, ok := t.extensions[name[i+1:]]; ok {
			return icon
		}
		j := strings.IndexByte(name[i+1:], '.')
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return t.file
}

// folderIconOf is the icon of a folder of the theme, open or not.
func (t *iconTheme) folderIconOf(name string, open bool) string {
	if icon, ok := t.folders[strings.ToLower(name)]; ok {
		return icon
	}
	if open {
		return t.folderOpen
	}
	return t.folder
}

// fileIcons are the theme's icons parsed, by name and appearance.
var fileIcons = map[string]*ui.SVG{}

// fileIconSVG is an icon of the theme, for the dark appearance or the
// light one.
func fileIconSVG(icon string, dark bool) *ui.SVG {
	key := icon
	if !dark {
		key += "_light"
	}
	if svg := fileIcons[key]; svg != nil {
		return svg
	}
	b, err := fileIconFiles.ReadFile("icons/files/" + key + ".svg")
	if err != nil {
		b, err = fileIconFiles.ReadFile("icons/files/" + fileIconTheme.file + ".svg")
		if err != nil {
			panic("kero: no file icon " + key)
		}
	}
	svg := ui.MustParseSVG(b)
	fileIcons[key] = svg
	return svg
}

// fileIconImage shows a file's icon, or a folder's, open or not, in a
// tree's row.
func fileIconImage(c *ui.Context, pal *palette, name string, dir, open bool) ui.Element {
	icon := fileIconTheme.fileIconOf(name)
	if dir {
		icon = fileIconTheme.folderIconOf(name, open)
	}
	return ui.Image(c, fileIconSVG(icon, pal.dark)).Size(16, 16).Shrink(0)
}
