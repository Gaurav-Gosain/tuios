// Ported from pele src/diagrams/treeview/icons.ts at 8aff183be57021b533f319a29341a529be934b08.

package treeview

import (
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// BUILTIN_PACK is Mermaid's name for its built-in pack. Only `file` and `folder` are built in; every other icon
// name is offered to the host's icon resolver.
const BUILTIN_PACK = "mermaid-treeview"

// glyph is an outline on a 24 by 24 grid, or "" for a name with none.
func glyph(name string) string {
	switch name {
	case "folder":
		return "M3 6.5A1.5 1.5 0 0 1 4.5 5h4.6l2.4 2.5h8A1.5 1.5 0 0 1 21 9v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 18z"
	case "file":
		return "M6.5 3h7L19 8.5v11a1.5 1.5 0 0 1-1.5 1.5h-11A1.5 1.5 0 0 1 5 19.5v-15A1.5 1.5 0 0 1 6.5 3zM13.5 3v5.5H19"
	}
	return ""
}

type IconMap = map[string]any

type IconConfig struct {
	showIcons       bool
	defaultIconPack string
	filenameIcons   IconMap
	extensionIcons  IconMap
}

func own(m IconMap, key string) (string, bool) {
	value, ok := m[key].(string)
	return value, ok
}

// detectIcon is a file's icon from the configured maps. A file name wins over an extension, and extensions
// match in lower case with or without the leading dot.
func detectIcon(name string, config IconConfig) (string, bool) {
	if byName, ok := own(config.filenameIcons, name); ok && byName != "" {
		return byName, true
	}
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		ext := strings.ToLower(name[dot:])
		if icon, ok := own(config.extensionIcons, ext); ok {
			return icon, true
		}
		return own(config.extensionIcons, ext[1:])
	}
	return "", false
}

func qualifyIcon(icon, defaultIconPack string) string {
	if strings.Contains(icon, ":") {
		return icon
	}
	if glyph(icon) != "" || defaultIconPack == "" {
		return BUILTIN_PACK + ":" + icon
	}
	return defaultIconPack + ":" + icon
}

// getNodeIcon is the qualified icon name for a node, or "" when it shows none. An `icon()` annotation
// always wins; otherwise icons appear only with `showIcons`.
func getNodeIcon(node *TreeNode, config IconConfig) string {
	if node.icon == "none" {
		return ""
	}
	if node.icon != "" {
		return qualifyIcon(node.icon, config.defaultIconPack)
	}
	if !config.showIcons {
		return ""
	}
	if node.nodeType == "file" {
		detected, _ := detectIcon(node.name, config)
		if detected == "none" {
			return ""
		}
		if detected != "" {
			return qualifyIcon(detected, config.defaultIconPack)
		}
	}
	if node.nodeType == "directory" {
		return BUILTIN_PACK + ":folder"
	}
	return BUILTIN_PACK + ":file"
}

func iconConfig(config types.Config) IconConfig {
	c, _ := config["treeView"].(map[string]any)
	pack, _ := c["defaultIconPack"].(string)
	filenameIcons, _ := c["filenameIcons"].(map[string]any)
	extensionIcons, _ := c["extensionIcons"].(map[string]any)
	return IconConfig{
		showIcons:       js.Truthy(c["showIcons"]),
		defaultIconPack: pack,
		filenameIcons:   filenameIcons,
		extensionIcons:  extensionIcons,
	}
}
