// Package plugins holds the first-party plugins of tuios. The binary embeds
// only the template, which tuios plugins new copies.
package plugins

import "embed"

// Template is plugins/template, with its .github folder.
//
//go:embed all:template
var Template embed.FS

// TemplateID and TemplateName are what tuios plugins new replaces in the
// template's files.
const (
	TemplateID   = "example.hello"
	TemplateName = "Hello"
)
