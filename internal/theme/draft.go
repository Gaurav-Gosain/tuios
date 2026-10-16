package theme

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"charm.land/tone"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	tint "github.com/lrstanley/bubbletint/v2"
)

// A Draft is a theme file being edited: the fields the file itself sets, over
// its base. A field left empty comes from the base, so a draft saves as the
// sparse file a person would write, and resetting a field is emptying it.
//
// The editor previews a draft by registering it under a scratch id and
// selecting that, which is the same pointer swap a theme switch is: the UI
// memo is keyed on the theme's pointers, so the next frame repaints. A client
// that must not change the process-wide theme (a served one) measures the
// draft with PaletteOf instead, which registers nothing.

// Draft is a theme file being edited.
type Draft struct {
	// ID is the id the file is saved under. Base is the theme it starts from:
	// a built-in or another file. A draft whose ID is a built-in's and whose
	// Base is the same id is an edited built-in.
	ID, Base string
	// Name is the display name the file gives, empty for none.
	Name string
	// Dark is the file's own dark flag, nil to take the base's.
	Dark *bool
	// Colours are the file's own pane colours as #rrggbb, by ColourKeys, empty
	// to take the base's.
	Colours [len(colourKeys)]string
	// Chrome holds the file's own chrome fields by role, as the file writes
	// them: hex, a slot or another role. Empty takes the base's.
	Chrome [roleLastSettable + 1]string
	// src is the file the draft was read from and the bytes it held. Clones
	// share it, so a save seen by one draft is seen by the undo stack too.
	src *draftSource
}

// draftSource is where a draft saves and what that file held when it was
// last read or written. A new theme has no path yet.
type draftSource struct {
	path string
	disk []byte
}

// ErrChangedOnDisk is Save's error when the file changed after the draft read
// it: by hand, by another client or by a tool. SaveOver writes anyway.
var ErrChangedOnDisk = errors.New("the theme file changed on disk after the editor read it")

// ColourKeys are the names of a theme's pane colours, in Draft.Colours order.
func ColourKeys() []string { return colourKeys[:] }

// ColourOf is t's pane colour i, in ColourKeys order.
func ColourOf(t *tint.Tint, i int) *tint.Color { return *themeColours(t)[i] }

// SettableRoles are the chrome roles a theme file may set, in file order.
func SettableRoles() []Role {
	out := make([]Role, 0, roleLastSettable)
	for r := RoleAccent; r <= roleLastSettable; r++ {
		out = append(out, r)
	}
	return out
}

// ValidThemeID reports whether id can name a theme file: 1 to 64 of a-z,
// 0-9, '_', '.' and '-', starting with a letter or a digit. A loop, not a
// regexp: a package-level pattern compiles at init in every tuios process.
func ValidThemeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case i > 0 && (c == '_' || c == '.' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// DraftOf starts a draft of the theme id. A theme file of yours is read as it
// is written. A built-in starts as an edited copy under its own id.
func DraftOf(id string) (*Draft, error) {
	EnsureRegistry()
	themeBases.RLock()
	r, custom := themeBases.customs[id]
	themeBases.RUnlock()
	if !custom {
		if _, ok := tint.GetTint(id); !ok {
			return nil, fmt.Errorf("%q is not a theme", id)
		}
		return &Draft{ID: id, Base: id, src: &draftSource{}}, nil
	}
	// #nosec G304 -- the path is the themes directory's own file for id
	data, err := os.ReadFile(r.path)
	if err != nil {
		return nil, err
	}
	f, err := readThemeFile(r.path, data)
	if err != nil {
		return nil, err
	}
	d := &Draft{ID: id, Base: f.Base, Dark: f.Dark, src: &draftSource{path: r.path, disk: data}}
	if f.DisplayName != f.ID {
		d.Name = f.DisplayName
	}
	for i, c := range themeColours(&f.Tint) {
		d.Colours[i] = tintHex(*c)
	}
	raw := chromeOf(f.Chrome)
	copy(d.Chrome[:], raw[:])
	return d, nil
}

// NewDraft starts a theme of its own on base: no fields of its own, under the
// first free id made from base.
func NewDraft(base string) (*Draft, error) {
	EnsureRegistry()
	if _, ok := tint.GetTint(base); !ok {
		return nil, fmt.Errorf("%q is not a theme", base)
	}
	stem := strings.TrimSuffix(base, "-custom")
	id := stem + "-custom"
	for n := 2; ; n++ {
		if _, taken := tint.GetTint(id); !taken {
			break
		}
		id = fmt.Sprintf("%s-custom-%d", stem, n)
	}
	return &Draft{ID: id, Base: base, src: &draftSource{}}, nil
}

// Clone copies the draft.
func (d *Draft) Clone() *Draft {
	c := *d
	if d.Dark != nil {
		dark := *d.Dark
		c.Dark = &dark
	}
	return &c
}

// file is the draft as a theme file, for the merge.
func (d *Draft) file() (*themeFile, error) {
	f := &themeFile{Base: d.Base, Dark: d.Dark, Chrome: map[string]any{}}
	f.ID, f.DisplayName = d.ID, d.ID
	if d.Name != "" {
		f.DisplayName = d.Name
	}
	for i, c := range themeColours(&f.Tint) {
		if v := d.Colours[i]; v != "" {
			col := tint.FromHex(v)
			if col == nil {
				return nil, fmt.Errorf("%s: %q is not a #rrggbb colour", colourKeys[i], v)
			}
			*c = col
		}
	}
	for r := RoleAccent; r <= roleLastSettable; r++ {
		if v := d.Chrome[r]; v != "" {
			group, field, nested := strings.Cut(r.String(), ".")
			if !nested {
				f.Chrome[r.String()] = v
				continue
			}
			obj, _ := f.Chrome[group].(map[string]any)
			if obj == nil {
				obj = map[string]any{}
				f.Chrome[group] = obj
			}
			obj[field] = v
		}
	}
	return f, nil
}

// Resolve merges the draft over its base: the colours a frame would draw and
// the chrome they build. Nothing is registered.
func (d *Draft) Resolve() (*tint.Tint, *Chrome, error) {
	f, err := d.file()
	if err != nil {
		return nil, nil, err
	}
	r, err := mergeBase(f)
	if err != nil {
		return nil, nil, err
	}
	c, err := buildChrome(r.chrome, r.tint)
	if err != nil {
		return nil, nil, err
	}
	return r.tint, c, nil
}

// ScratchID is the id a draft of id previews under.
func ScratchID(id string) string { return id + "~edit" }

// Preview registers the draft under its scratch id and puts it in force. It
// changes the process-wide theme: only a client alone in its process calls it.
func (d *Draft) Preview() error {
	t, c, err := d.Resolve()
	if err != nil {
		return err
	}
	scratch := *t
	scratch.ID = ScratchID(d.ID)
	tint.Register(&scratch)
	registerChrome(scratch.ID, c)
	tint.SetTintID(scratch.ID)
	enabled = true
	matchHost.Store(false)
	generation.Add(1)
	return nil
}

// DropScratch forgets the scratch theme of id. The caller selects the theme
// to go back to first.
func DropScratch(id string) {
	scratch := ScratchID(id)
	registerChrome(scratch, nil)
	tint.UnregisterIDs(scratch)
}

// Save writes the draft as a sparse file and reads the themes directory
// again, so the theme is selectable at once. It returns the file. A theme read
// from a file is written back to that file, whatever its name. A new one is
// written as its id in the themes directory.
//
// A file that changed after the draft read it is not replaced: Save returns
// ErrChangedOnDisk, and SaveOver replaces it.
func (d *Draft) Save() (string, error) { return d.save(false) }

// SaveOver is Save that replaces the file even when it changed on disk.
func (d *Draft) SaveOver() (string, error) { return d.save(true) }

func (d *Draft) save(force bool) (string, error) {
	if !ValidThemeID(d.ID) {
		return "", fmt.Errorf("%q cannot name a theme file. Use lower-case letters, digits, dots, dashes and underscores", d.ID)
	}
	if _, _, err := d.Resolve(); err != nil {
		return "", err
	}
	if d.src == nil {
		d.src = &draftSource{}
	}
	path := d.src.path
	if path == "" {
		dir, err := GetThemesDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(dir, d.ID+".json")
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New(filepath.Base(path) + " is a link. tuios does not write through it")
	}
	if !force {
		// #nosec G304 -- the path is the draft's own file in the themes directory
		now, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if !bytes.Equal(now, d.src.disk) {
			return "", ErrChangedOnDisk
		}
	}
	doc := themeDoc{ID: d.ID, DisplayName: d.Name, Base: d.Base, Dark: d.Dark, Colours: d.Colours}
	copy(doc.Chrome[:], d.Chrome[:])
	body := doc.encode(true)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	d.src.path, d.src.disk = path, body
	ReloadCustomThemes()
	return path, nil
}

// PaletteOf is the dialog palette the theme t and its chrome c draw, in the
// theme's own colours: at truecolor, whatever the depth in force. The editor
// shows and saves these values, and the frame writer steps them down when it
// draws them. Built at 256 or 16 colours, a role the theme leaves to a slot
// showed the palette entry's hex, not the theme's. It reads no global theme
// state, so a client measures a draft without putting it in force.
func PaletteOf(t *tint.Tint, c *Chrome) overlay.Palette {
	return buildUIWith(overlay.DepthTrueColor, t, c)
}

// RoleColour is the colour role r draws for the theme t and chrome c, whose
// dialog palette is pal: the colour the theme names, or what the role falls
// back to when it names none. A ground the theme does not name is nil.
func RoleColour(r Role, t *tint.Tint, c *Chrome, pal overlay.Palette) color.Color {
	if f := c.Furniture(r); f != nil {
		return f
	}
	if r > RoleCard && r <= roleLastSettable {
		return furnitureFallback(r, t, pal)
	}
	return roleOn(r, &pal)
}

// furnitureFallback is what a role after RoleCard draws when the theme names
// none, for the theme t: the same answers BorderUnfocused, DockColorCopy and
// the others give with no chrome.
func furnitureFallback(r Role, t *tint.Tint, pal overlay.Palette) color.Color {
	var bg color.Color = uiCanvas
	if t != nil {
		bg = AsColor(t.Bg)
	}
	switch r {
	case RoleBorderUnfocused:
		if t != nil {
			return ReadableAt(AsColor(t.Red), bg, MarkFloor)
		}
	case RoleBorderCopy:
		return pal.Warning
	case RoleBorderMultifocus:
		if t != nil {
			return AsColor(t.Yellow)
		}
		return overlay.Slot(3)
	case RoleRule:
		return overlay.Structure(bg)
	case RoleAttention:
		return pal.Attention
	case RoleBusy:
		return pal.Busy
	case RoleDotClose:
		return tokDotClose
	case RoleDotMinimize:
		return tokDotMinimise
	case RoleDotZoom:
		return tokDotZoom
	}
	return nil
}

// FloorOf is the contrast role r has to clear and the ground it is measured
// on, for the theme t with dialog palette pal: text on the dialogs clears
// the text floor, a pill, a mark or a border the mark floor. A ground has no
// floor (0).
func FloorOf(r Role, t *tint.Tint, pal overlay.Palette) (floor float64, ground color.Color) {
	var bg color.Color = uiCanvas
	if t != nil {
		bg = AsColor(t.Bg)
	}
	switch r {
	case RoleAccentBright, RoleText, RoleDim:
		return ContrastFloor, pal.Surface
	case RoleAccent, RoleSuccess, RoleWarning, RoleError, RoleInfo, RoleMuted:
		return MarkFloor, pal.Surface
	case RoleBorderUnfocused, RoleBorderCopy, RoleBorderMultifocus,
		RoleDotClose, RoleDotMinimize, RoleDotZoom, RoleAttention, RoleBusy:
		return MarkFloor, bg
	}
	return 0, nil
}

// ColourNames lists the names a colour value can take, for completion: the
// roles, the slots and the CharmTone names.
func ColourNames() []string {
	var out []string
	for r := RoleAccent; r <= RoleMuted; r++ {
		out = append(out, r.String())
	}
	for _, bright := range []string{"", "bright_"} {
		for _, n := range slotNames {
			out = append(out, bright+n)
		}
	}
	out = append(out, "fg", "bg")
	for _, k := range toneKeys() {
		out = append(out, strings.ToLower(k))
	}
	return out
}

// toneKeys are the CharmTone colour names.
func toneKeys() []string {
	keys := tone.Keys()
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.String())
	}
	return out
}
