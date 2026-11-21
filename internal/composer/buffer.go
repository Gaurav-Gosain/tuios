package composer

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"tuios.dev/vt"
)

const tabWidth = 4

type cell struct {
	s     string
	w     int
	space bool
	block *block
}

func (c *cell) isSpace() bool { return c.space }

func (c cell) bytes() int {
	if c.block != nil {
		return len(c.block.text)
	}
	return len(c.s)
}

func (c cell) weight() int {
	if c.block != nil && c.block.img != nil {
		return len(c.block.img.Data)
	}
	return c.bytes()
}

type block struct {
	n            int
	text         string
	img          *Image
	lines, chars int
	label        string
}

func segment(dst []cell, text string) []cell {
	for text != "" {
		switch text[0] {
		case '\n':
			dst = append(dst, cell{s: "\n", space: true})
			text = text[1:]
			continue
		case '\t':
			dst = append(dst, cell{s: "\t", w: tabWidth, space: true})
			text = text[1:]
			continue
		}
		cluster, w := ansi.FirstGraphemeCluster(text, ansi.GraphemeWidth)
		if cluster == "" {
			break
		}
		dst = append(dst, cell{s: cluster, w: w, space: cluster == " "})
		text = text[len(cluster):]
	}
	return dst
}

func clean(text string) string {
	if strings.IndexByte(text, '\r') >= 0 {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
	}
	return vt.SanitizePaste(text)
}
