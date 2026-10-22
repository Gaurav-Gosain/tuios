package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/pelletier/go-toml/v2"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The look list.
//
// The tuios.dev gallery publishes its entries as one JSON feed at
// /gallery/feed: the comments of discussion #849 that the maintainer approved,
// synced hourly. An entry whose config is a look carries a look id, and the
// look itself is served at /r/<id>.toml. `tuios look list` and `tuios look
// pick` read that feed. The CDN keeps it for five minutes, so tuios keeps no
// copy of its own.

// defaultLookGallery is the site the look list and the looks come from.
const defaultLookGallery = "https://tuios.dev"

// lookGalleryEnv moves the gallery to another site, for tests. A plain http
// site is taken only on a loopback address.
const lookGalleryEnv = "TUIOS_LOOK_GALLERY"

// maxLookFeedBytes caps the feed download.
const maxLookFeedBytes = 4 << 20

// maxLookName caps a look's name and author in the list.
const maxLookName = 60

// lookGalleryBase is the gallery site, with no trailing slash.
func lookGalleryBase() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv(lookGalleryEnv)), "/")
	if base == "" {
		return defaultLookGallery, nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s is not a URL", lookGalleryEnv)
	}
	switch u.Scheme {
	case "https":
		return base, nil
	case "http":
		// A literal loopback address only: a name such as localhost goes
		// through the resolver, which can answer anything.
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
			return base, nil
		}
	}
	return "", fmt.Errorf("%s must be an https URL", lookGalleryEnv)
}

// lookGalleryURL is where the gallery serves the look with id.
func lookGalleryURL(id string) string {
	base, err := lookGalleryBase()
	if err != nil {
		base = defaultLookGallery
	}
	return base + "/r/" + id + ".toml"
}

// galleryLook is one look in the gallery list.
type galleryLook struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Author string `json:"author"`
	// Colors are the look's theme colours, for a preview: the background,
	// the foreground, then the accent colours. Empty when the look has no
	// theme tuios can read.
	Colors []string `json:"colors,omitempty"`
}

// errLookList is the one sentence a person sees when the list does not come.
var errLookList = errors.New("tuios cannot get the look list from the gallery")

var (
	lookIDPattern  = regexp.MustCompile(`^\d{1,12}$`)
	hexColor       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	lookFeedClient = &http.Client{Timeout: lookFetchTimeout, CheckRedirect: lookHTTP.CheckRedirect}
)

// fetchLookList downloads the gallery feed and returns its looks, in the
// order the gallery shows them.
func fetchLookList() ([]galleryLook, error) {
	base, err := lookGalleryBase()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, base+"/gallery/feed", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tuios/"+version)
	resp, err := lookFeedClient.Do(req)
	if err != nil {
		return nil, errLookList
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errLookList
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLookFeedBytes+1))
	if err != nil || len(data) > maxLookFeedBytes {
		return nil, errLookList
	}
	looks, err := parseLookFeed(data)
	if err != nil {
		return nil, errLookList
	}
	return looks, nil
}

// lookFeed is the part of the gallery feed tuios reads.
type lookFeed struct {
	Version int `json:"version"`
	Entries []struct {
		Login string `json:"login"`
		Title string `json:"title"`
		Files []struct {
			Label string `json:"label"`
			Text  string `json:"text"`
		} `json:"files"`
		Look *struct {
			ID string `json:"id"`
		} `json:"look"`
	} `json:"entries"`
}

// parseLookFeed reads the looks out of the gallery feed. A stranger writes
// each entry, so every text is cleaned and every colour checked.
func parseLookFeed(data []byte) ([]galleryLook, error) {
	var feed lookFeed
	if err := json.Unmarshal(data, &feed); err != nil {
		return nil, err
	}
	if feed.Version != 1 {
		return nil, fmt.Errorf("the feed is version %d", feed.Version)
	}
	looks := []galleryLook{}
	seen := map[string]bool{}
	for _, e := range feed.Entries {
		if e.Look == nil || !lookIDPattern.MatchString(e.Look.ID) || seen[e.Look.ID] {
			continue
		}
		seen[e.Look.ID] = true
		l := galleryLook{ID: e.Look.ID, Name: cleanLookText(e.Title), Author: cleanLookText(e.Login)}
		for _, f := range e.Files {
			if f.Label == "toml" {
				l.Colors = lookColors(f.Text)
				break
			}
		}
		looks = append(looks, l)
	}
	return looks, nil
}

// cleanLookText is s on one line, cut to maxLookName runes. Every control
// character, format character (bidi controls and zero-width marks are
// format characters), and line or paragraph separator becomes a space, so a
// name cannot reorder or break the list.
func cleanLookText(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if theme.IsUnsafeRune(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' || r == '\u061c' {
			return ' '
		}
		return r
	}, s))
	if r := []rune(s); len(r) > maxLookName {
		s = string(r[:maxLookName])
	}
	return s
}

// lookColors are the preview colours of a look file: the theme's background,
// foreground and accents, or the border colours of a look with no theme.
func lookColors(text string) []string {
	var raw map[string]any
	if toml.Unmarshal([]byte(text), &raw) != nil {
		return nil
	}
	pick := func(m map[string]any, keys ...string) []string {
		var out []string
		for _, k := range keys {
			if s, ok := m[k].(string); ok && hexColor.MatchString(s) {
				out = append(out, strings.ToLower(s))
			}
		}
		return out
	}
	if th, ok := raw["theme"].(map[string]any); ok {
		if out := pick(th, "bg", "fg", "red", "green", "yellow", "blue", "purple", "cyan"); len(out) > 0 {
			return out
		}
	}
	if ap, ok := raw["appearance"].(map[string]any); ok {
		return pick(ap, "desktop_background", "border_focused_color", "border_unfocused_color")
	}
	return nil
}

// printLookList prints the looks as a table, or as JSON.
func printLookList(w io.Writer, looks []galleryLook, asJSON bool) error {
	if asJSON {
		data, err := json.MarshalIndent(looks, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}
	if len(looks) == 0 {
		fmt.Fprintln(w, "The gallery has no looks.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tAUTHOR")
	for _, l := range looks {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", l.ID, lookNameShown(l), l.Author)
	}
	return tw.Flush()
}

// lookNameShown is the look's name, or a stand-in for a look with none.
func lookNameShown(l galleryLook) string {
	if l.Name == "" {
		return "(no name)"
	}
	return l.Name
}

// runLookPick shows the gallery looks, and applies the one the person
// chooses through the same path as `tuios look apply`.
func runLookPick(w io.Writer, takeOver bool) error {
	looks, err := fetchLookList()
	if err != nil {
		return err
	}
	if len(looks) == 0 {
		fmt.Fprintln(w, "The gallery has no looks.")
		return nil
	}
	pick := &lookPicker{looks: looks, chosen: -1, height: 24}
	if _, err := tea.NewProgram(pick, tea.WithOutput(w)).Run(); err != nil {
		return err
	}
	if pick.chosen < 0 {
		fmt.Fprintln(w, "tuios did not change the look.")
		return nil
	}
	l := looks[pick.chosen]
	fmt.Fprintf(w, "You chose %s.\n\n", lookTitle(l))
	// The prompt reads stdin only now, after the list gave the terminal back.
	return runLookApply(w, "tuios.dev/r/"+l.ID, newLookPrompt(false, takeOver))
}

// lookTitle is "Name by author".
func lookTitle(l galleryLook) string {
	if l.Author == "" {
		return lookNameShown(l)
	}
	return lookNameShown(l) + " by " + l.Author
}

// lookPicker is the list `tuios look pick` shows.
type lookPicker struct {
	looks  []galleryLook
	cur    int
	scroll int
	chosen int
	height int
	done   bool
}

func (p *lookPicker) Init() tea.Cmd { return nil }

// visible is how many rows of looks fit.
func (p *lookPicker) visible() int { return max(p.height-4, 3) }

func (p *lookPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.height = msg.Height
	case tea.KeyPressMsg:
		switch key := msg.String(); key {
		case "enter":
			p.chosen, p.done = p.cur, true
			return p, tea.Quit
		case "esc", "q", "ctrl+c":
			p.done = true
			return p, tea.Quit
		default:
			if d := listnav.Delta(listnav.Keys(key, true), p.visible()); d != 0 {
				p.cur = listnav.Step(p.cur, d, len(p.looks), true)
			}
		}
	}
	p.scroll = listnav.Scroll(p.scroll, p.cur, len(p.looks), p.visible())
	return p, nil
}

func (p *lookPicker) View() tea.View {
	if p.done {
		return tea.NewView("")
	}
	nameW, authorW := 0, 0
	for _, l := range p.looks {
		nameW = max(nameW, ansi.StringWidth(lookNameShown(l)))
		authorW = max(authorW, ansi.StringWidth(l.Author))
	}
	var b strings.Builder
	b.WriteString("Choose a look. Enter applies it. Esc closes the list.\n\n")
	end := min(p.scroll+p.visible(), len(p.looks))
	for i := p.scroll; i < end; i++ {
		l := p.looks[i]
		text := fmt.Sprintf(" %-*s  %-*s ", nameW, lookNameShown(l), authorW, l.Author)
		marker := "  "
		if i == p.cur {
			marker = "> "
			text = lipgloss.NewStyle().Reverse(true).Bold(true).Render(text)
		}
		b.WriteString(marker + text + " " + lookSwatches(l.Colors) + "\n")
	}
	fmt.Fprintf(&b, "\n%d of %d. Up and down move.", p.cur+1, len(p.looks))
	return tea.NewView(b.String())
}

// lookSwatches draws each colour as two cells of that background.
func lookSwatches(colors []string) string {
	var b strings.Builder
	for _, c := range colors {
		b.WriteString(lipgloss.NewStyle().Background(lipgloss.Color(c)).Render("  "))
	}
	return b.String()
}
