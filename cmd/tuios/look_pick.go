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

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const defaultLookGallery = "https://tuios.dev"

const lookGalleryEnv = "TUIOS_LOOK_GALLERY"

const maxLookFeedBytes = 4 << 20

const maxLookName = 60

func lookGalleryBase() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv(lookGalleryEnv)), "/")
	if base == "" {
		return defaultLookGallery, nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", errfmt.Errorf("%s is not a URL", lookGalleryEnv)
	}
	switch u.Scheme {
	case "https":
		return base, nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
			return base, nil
		}
	}
	return "", errfmt.Errorf("%s must be an https URL", lookGalleryEnv)
}

func lookGalleryURL(id string) string {
	base, err := lookGalleryBase()
	if err != nil {
		base = defaultLookGallery
	}
	return base + "/r/" + id + ".toml"
}

type galleryLook struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Author   string   `json:"author"`
	Colors   []string `json:"colors,omitempty"`
	Requires string   `json:"requires,omitempty"`
}

var errLookList = errors.New("tuios cannot get the look list from the gallery")

var (
	lookIDPattern  = regexp.MustCompile(`^\d{1,12}$`)
	hexColor       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	lookFeedClient = &http.Client{Timeout: lookFetchTimeout, CheckRedirect: lookHTTP.CheckRedirect}
)

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

func parseLookFeed(data []byte) ([]galleryLook, error) {
	var feed lookFeed
	if err := jsonx.Unmarshal(data, &feed); err != nil {
		return nil, err
	}
	if feed.Version != 1 {
		return nil, errfmt.Errorf("the feed is version %d", feed.Version)
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
				l.Requires, _ = config.LookRequires([]byte(f.Text))
				break
			}
		}
		looks = append(looks, l)
	}
	return looks, nil
}

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
		return pick(ap, "desktop_background", "border_focused_color", "border_focused_color_window", "border_focused_color_terminal", "border_unfocused_color")
	}
	return nil
}

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

func lookNameShown(l galleryLook) string {
	if l.Name == "" {
		return "(no name)"
	}
	return l.Name
}

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
	return runLookApply(w, "tuios.dev/r/"+l.ID, newLookPrompt(false, takeOver))
}

func lookTitle(l galleryLook) string {
	if l.Author == "" {
		return lookNameShown(l)
	}
	return lookNameShown(l) + " by " + l.Author
}

type lookPicker struct {
	looks  []galleryLook
	cur    int
	scroll int
	chosen int
	height int
	done   bool
}

func (p *lookPicker) Init() tea.Cmd { return nil }

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
		fmt.Fprintf(&b, "%s%s %s%s\n", marker, text, lookSwatches(l.Colors), lookNeeds(l))
	}
	fmt.Fprintf(&b, "\n%d of %d. Up and down move.", p.cur+1, len(p.looks))
	return tea.NewView(b.String())
}

func lookNeeds(l galleryLook) string {
	if !lookTooNew(l.Requires, version) {
		return ""
	}
	return lipgloss.NewStyle().Faint(true).Render("  Needs tuios " + strings.TrimPrefix(l.Requires, "v") + " or later")
}

func lookSwatches(colors []string) string {
	var b strings.Builder
	for _, c := range colors {
		b.WriteString(lipgloss.NewStyle().Background(lipgloss.Color(c)).Render("  "))
	}
	return b.String()
}
