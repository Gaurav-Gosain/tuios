package guibridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Sessions and settings for the renderer (plan 2.11 and 2.14).
//
// switch-session moves this bridge to another session in place, the way the
// terminal client's session switcher does: one attach, no new process, and
// the panes of the new session arrive as snapshots. It can make the session,
// or restore it from its saved state first.
//
// option and theme with persist write the user's config file. The bridge's
// model never writes the file on its own (its config is read only, so a
// renderer's view never decides the file for the other clients), so these
// commands do it on purpose: the person changed a setting.

// switchSession runs a switch-session command.
func (m *model) switchSession(c Command) error {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		return errors.New("switch-session needs name, the session to show")
	}
	if c.Restore {
		if err := resurrect(m.version, name); err != nil {
			return err
		}
	} else if !c.Create && !m.sessionExists(name) {
		return fmt.Errorf("no session %q", name)
	}
	if name == m.os.SessionName {
		return nil
	}
	return m.os.SwitchToSession(name)
}

// sessionExists says whether the daemon holds a session of that name. It
// asks the daemon, since the client's list of sessions can be old.
func (m *model) sessionExists(name string) bool {
	c, err := session.DialVerbClientAs(m.version)
	if err != nil {
		return false
	}
	defer func() { _ = c.Close() }()
	raw, err := c.Call("list-sessions", nil)
	if err != nil {
		return false
	}
	var out struct {
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return false
	}
	for _, s := range out.Sessions {
		if s.Name == name {
			return true
		}
	}
	return false
}

// resurrect asks the daemon to restore a saved session. It is a no-op for a
// session the daemon holds already.
func resurrect(version, name string) error {
	c := session.NewClient(&session.ClientConfig{Version: version})
	if err := c.Connect(); err != nil {
		return fmt.Errorf("failed to reach the daemon: %w", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.ResurrectSession(name); err != nil {
		return fmt.Errorf("failed to restore %s: %w", name, err)
	}
	return nil
}

// SavedSession is one session saved on disk, as `tuios resurrect --json`
// lists it.
type SavedSession struct {
	Name    string `json:"name"`
	Windows int    `json:"windows"`
	// SavedAt is when the state was written, in Unix seconds.
	SavedAt int64 `json:"saved_at"`
}

// savedSessions lists the sessions saved on disk.
func savedSessions() []SavedSession {
	infos, err := session.ListResurrectableInfos()
	if err != nil {
		return nil
	}
	out := make([]SavedSession, 0, len(infos))
	for _, in := range infos {
		s := SavedSession{Name: in.Name, Windows: in.WindowCount}
		if !in.SavedAt.IsZero() {
			s.SavedAt = in.SavedAt.Unix()
		}
		out = append(out, s)
	}
	return out
}

// writeOption sets one option in the user's config file. The file watcher
// then applies it to this model and to every other client.
func writeOption(key, value string) error {
	if key == "" {
		return errors.New("option needs key, an option path from list-options")
	}
	cfg, err := config.LoadUserConfig()
	if err != nil {
		return fmt.Errorf("failed to read the config: %w", err)
	}
	if err := config.SetOptionValue(cfg, key, value); err != nil {
		return err
	}
	write, err := config.RenderUserConfig(cfg)
	if err != nil {
		return err
	}
	if _, err := write(); err != nil {
		return fmt.Errorf("failed to save the config: %w", err)
	}
	return nil
}

// OptionRow is one row of the options event: an option of list-options and
// the value the user's config file gives it now.
type OptionRow struct {
	config.Option
	// Value is the value in force from the config file, or the default.
	Value string `json:"value"`
}

// OptionList is the options event: every settable option with its value, for
// the renderer's settings page.
type OptionList struct {
	Options  []OptionRow `json:"options"`
	Sections []string    `json:"sections"`
	// Path is the config file the option command writes.
	Path string `json:"path,omitempty"`
}

// sendOptions reads the options and the user's config file off the model's
// goroutine and sends the options event.
func sendOptions(out *frameWriter, req int64) {
	go func() {
		ev := &OptionList{}
		cfg, err := config.LoadUserConfig()
		if err != nil || cfg == nil {
			cfg = config.DefaultConfig()
		}
		seen := map[string]bool{}
		for _, o := range config.Options() {
			if o.Deprecated != "" {
				continue
			}
			v, ok := config.GetOptionValue(cfg, o.Path)
			if !ok {
				v = o.Default
			}
			ev.Options = append(ev.Options, OptionRow{Option: o, Value: v})
			if !seen[o.Section] {
				seen[o.Section] = true
				ev.Sections = append(ev.Sections, o.Section)
			}
		}
		if p, err := config.GetConfigPath(); err == nil {
			ev.Path = p
		}
		out.JSON(Event{Type: "options", Options: ev, Req: req})
	}()
}
