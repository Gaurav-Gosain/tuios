package guibridge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// Saved layouts: tuios's layout templates (Save layout, Load layout), for
// the renderer's palette. The layouts command answers with the layouts
// event; layout-save, layout-load and layout-delete answer with a result.
// A load builds the panes the template names in the workspace on screen,
// as the terminal client's layout picker does.

// SavedLayout is one layout template.
type SavedLayout struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Panes is how many panes it opens, and SavedAt when it was written, in
	// Unix seconds.
	Panes   int   `json:"panes"`
	SavedAt int64 `json:"saved_at,omitempty"`
	Tiling  bool  `json:"tiling"`
}

// sendLayouts reads the templates off the model's goroutine and sends the
// layouts event.
func sendLayouts(out *frameWriter, req int64) {
	go func() {
		tmpls, err := app.LoadLayoutTemplates()
		list := make([]SavedLayout, 0, len(tmpls))
		if err == nil {
			for _, t := range tmpls {
				n := 0
				for _, w := range t.Windows {
					if !w.Minimized {
						n++
					}
				}
				s := SavedLayout{Name: t.Name, Description: t.Description, Panes: n, Tiling: t.AutoTiling}
				if !t.CreatedAt.IsZero() {
					s.SavedAt = t.CreatedAt.Unix()
				}
				list = append(list, s)
			}
		}
		out.JSON(Event{Type: "layouts", Layouts: list, Req: req})
	}()
}

// layoutTemplate runs layout-save, layout-load or layout-delete.
func (m *model) layoutTemplate(op, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name the layout")
	}
	switch op {
	case "layout-save":
		return app.SaveLayoutTemplate(name, m.os)
	case "layout-delete":
		return app.DeleteLayoutTemplate(name)
	case "layout-load":
		tmpls, err := app.LoadLayoutTemplates()
		if err != nil {
			return err
		}
		for _, t := range tmpls {
			if t.Name == name {
				app.ApplyLayoutTemplate(t, m.os)
				return nil
			}
		}
		return fmt.Errorf("no saved layout %q", name)
	}
	return fmt.Errorf("%s is not a layout command", op)
}
