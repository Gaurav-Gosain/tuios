package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// lookGallery is where a gallery id resolves: tuios.dev/r/<id> serves the
// look as <id>.toml.
const lookGallery = "https://tuios.dev/r/"

// lookFetchTimeout bounds a look download, start to end.
const lookFetchTimeout = 15 * time.Second

// newLookCommand creates the look command.
func newLookCommand() *cobra.Command {
	lookCmd := &cobra.Command{
		Use:   "look",
		Short: "Apply, show and remove a look",
		Long: `A look is a small TOML file that sets how tuios looks: theme, glyph set,
borders, gaps, dock and rail style. The tuios.dev gallery shares looks.

tuios writes a look to config.d/look.toml, its own file. It does not change
config.toml. "tuios look remove" deletes the look and the theme it installed.
A look may set only look keys. "tuios look keys" lists them.`,
	}

	var applyYes bool
	applyCmd := &cobra.Command{
		Use:   "apply <source>",
		Short: "Apply a look from the gallery, an https URL or a file",
		Long: `Apply a look. The source is a gallery id (tuios.dev/r/ID or ID), an https URL,
or a local file. Each look on tuios.dev/gallery shows the command with its id.

tuios shows the changes and asks first. Use --yes to skip the question. A run
with no terminal needs --yes. tuios leaves out every key that a look cannot set
and names it. A running tuios reloads the config, so you do not restart.`,
		Example: `  tuios look apply tuios.dev/r/<id>
  tuios look apply https://example.com/my.look.toml
  tuios look apply ./my.look.toml --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLookApply(cmd.OutOrStdout(), args[0], lookPrompt{yes: applyYes, tty: term.IsTerminal(int(os.Stdin.Fd())), in: os.Stdin})
		},
	}
	applyCmd.Flags().BoolVar(&applyYes, "yes", false, "Apply the look without the question")

	var removeYes bool
	removeCmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove the look and the theme it installed",
		Long: `Remove the look. tuios deletes config.d/look.toml and the theme and glyph set
that the look installed. Your config is then as it was before the look.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLookRemove(cmd.OutOrStdout(), lookPrompt{yes: removeYes, tty: term.IsTerminal(int(os.Stdin.Fd())), in: os.Stdin})
		},
	}
	removeCmd.Flags().BoolVar(&removeYes, "yes", false, "Remove the look without the question")

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Show the look in use",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLookShow(cmd.OutOrStdout())
		},
	}

	var keysJSON bool
	keysCmd := &cobra.Command{
		Use:   "keys",
		Short: "List the keys a look can set",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if keysJSON {
				data, err := config.LookKeysJSON()
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}
			for _, k := range config.LookKeys() {
				fmt.Fprintln(cmd.OutOrStdout(), k)
			}
			return nil
		},
	}
	keysCmd.Flags().BoolVar(&keysJSON, "json", false, "Print the keys and their types as JSON")

	lookCmd.AddCommand(applyCmd, removeCmd, showCmd, keysCmd)
	return lookCmd
}

// lookPrompt is how a command asks before it writes.
type lookPrompt struct {
	yes bool
	tty bool
	in  io.Reader
}

// confirm asks question and reports a yes. Without a terminal it needs --yes.
func (p lookPrompt) confirm(w io.Writer, question string) (bool, error) {
	if p.yes {
		return true, nil
	}
	if !p.tty {
		return false, errors.New("no terminal to ask in. Run the command again with --yes")
	}
	fmt.Fprintf(w, "%s [y/N] ", question)
	line, _ := bufio.NewReader(p.in).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// runLookApply reads the look at source, shows the plan, asks, and applies it.
func runLookApply(w io.Writer, source string, prompt lookPrompt) error {
	data, shown, err := readLookSource(source)
	if err != nil {
		return err
	}
	lk, err := config.ParseLook(data)
	if err != nil {
		return fmt.Errorf("tuios cannot use the look: %w", err)
	}
	mainPath, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("could not determine config path: %w", err)
	}
	plan, err := config.PlanLook(mainPath, lk, shown)
	if err != nil {
		return err
	}
	printLookPlan(w, plan)
	ok, err := prompt.confirm(w, "Apply this look?")
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "tuios did not change the look.")
		return nil
	}
	if err := plan.Apply(); err != nil {
		return err
	}
	fmt.Fprintln(w, "The look is applied. A running tuios shows it now.")
	fmt.Fprintln(w, `Run "tuios look remove" to remove it.`)
	return nil
}

// runLookRemove shows what removing the look changes, asks, and removes it.
func runLookRemove(w io.Writer, prompt lookPrompt) error {
	mainPath, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("could not determine config path: %w", err)
	}
	plan, err := config.PlanLookRemove(mainPath)
	if err != nil {
		return err
	}
	if plan.Current == nil {
		fmt.Fprintln(w, "No look is applied.")
		return nil
	}
	printLookPlan(w, plan)
	ok, err := prompt.confirm(w, "Remove this look?")
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "tuios did not change the look.")
		return nil
	}
	if err := plan.Apply(); err != nil {
		return err
	}
	fmt.Fprintln(w, "The look is removed.")
	return nil
}

// runLookShow prints the installed look.
func runLookShow(w io.Writer) error {
	mainPath, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("could not determine config path: %w", err)
	}
	il, err := config.ReadInstalledLook(mainPath)
	if err != nil {
		return err
	}
	if il == nil {
		fmt.Fprintln(w, "No look is applied.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(tw, "%s\t%s\n", k, v)
		}
	}
	row("look", il.Name)
	src := il.Source
	if !strings.HasPrefix(src, "https://") {
		src = homeShort(src)
	}
	row("source", src)
	row("file", homeShort(il.Path))
	row("theme", il.Theme)
	row("glyphs", il.Glyphs)
	_ = tw.Flush()
	if len(il.Settings) > 0 {
		fmt.Fprintln(w)
		for _, s := range il.Settings {
			fmt.Fprintf(tw, "  %s\t%s\n", s.Path, s.Text())
		}
		_ = tw.Flush()
	}
	return nil
}

// printLookPlan prints what an apply or a remove changes.
func printLookPlan(w io.Writer, p *config.LookPlan) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if lk := p.Look; lk != nil {
		name := lk.Name
		if name == "" {
			name = "(no name)"
		}
		if lk.Author != "" {
			name += " by " + lk.Author
		}
		fmt.Fprintf(tw, "Look\t%s\n", name)
		src := p.Source
		if !strings.HasPrefix(src, "https://") {
			src = homeShort(src)
		}
		fmt.Fprintf(tw, "Source\t%s\n", src)
	} else if c := p.Current; c != nil && c.Name != "" {
		fmt.Fprintf(tw, "Look\t%s\n", c.Name)
	}
	fmt.Fprintf(tw, "File\t%s\n", homeShort(config.LookPath(p.Main)))
	_ = tw.Flush()

	fmt.Fprintln(w)
	if len(p.Changes) == 0 {
		fmt.Fprintln(w, "No setting changes.")
	} else {
		fmt.Fprintln(w, "Changes:")
		for _, c := range p.Changes {
			fmt.Fprintf(tw, "  %s\t%s\t->\t%s\n", c.Path, lookShown(c.From), lookShown(c.To))
		}
		_ = tw.Flush()
	}

	if lk := p.Look; lk != nil {
		if lk.Theme != nil || lk.Glyphs != nil || len(lk.Notes) > 0 {
			fmt.Fprintln(w)
		}
		if lk.Theme != nil {
			fmt.Fprintf(w, "Installs the theme %s.\n", lk.Theme.ID)
		}
		if lk.Glyphs != nil {
			fmt.Fprintf(w, "Installs the glyph set %s.\n", lk.Glyphs.ID)
		}
		for _, n := range lk.Notes {
			fmt.Fprintf(w, "Note: %s.\n", n)
		}
		if len(lk.Left) > 0 {
			fmt.Fprintf(w, "\nA look cannot set these keys. tuios leaves them out:\n  %s\n", strings.Join(lk.Left, ", "))
		}
	} else if c := p.Current; c != nil {
		if c.Theme != "" || c.Glyphs != "" {
			fmt.Fprintln(w)
		}
		if c.Theme != "" {
			fmt.Fprintf(w, "Deletes the theme %s.\n", c.Theme)
		}
		if c.Glyphs != "" {
			fmt.Fprintf(w, "Deletes the glyph set %s.\n", c.Glyphs)
		}
	}
	if len(p.Shadowed) > 0 {
		fmt.Fprintln(w, "\nThese keys are set in a later file, so the look does not change them:")
		for _, s := range p.Shadowed {
			fmt.Fprintf(tw, "  %s\t%s\n", s.Path, s.File)
		}
		_ = tw.Flush()
		fmt.Fprintln(w, "Remove a key from that file to use the look's value.")
	}
	fmt.Fprintln(w)
}

// lookShown is a value for the change list. An empty value is shown as such.
func lookShown(v string) string {
	if v == "" {
		return `""`
	}
	return v
}

// homeShort writes a path under the home directory with ~.
func homeShort(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}

// readLookSource returns the look at source and the source as the look file
// records it.
func readLookSource(source string) ([]byte, string, error) {
	src, err := resolveLookSource(source, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
	if err != nil {
		return nil, "", err
	}
	if !src.remote {
		where := src.where
		if abs, err := filepath.Abs(where); err == nil {
			where = abs
		}
		data, err := readLookFile(where)
		return data, where, err
	}
	data, err := fetchLook(src.where)
	return data, src.where, err
}

// lookSource is where a look comes from.
type lookSource struct {
	remote bool
	where  string // an https URL, or a file path
}

// resolveLookSource sorts a source into a URL or a file. exists reports
// whether a local file is there.
func resolveLookSource(source string, exists func(string) bool) (lookSource, error) {
	s := strings.TrimSpace(source)
	switch {
	case s == "":
		return lookSource{}, errors.New("give a look: a gallery id, an https URL or a file")
	case strings.HasPrefix(s, "http://"):
		return lookSource{}, errors.New("tuios downloads a look only over https. Use an https URL")
	case strings.HasPrefix(s, "https://tuios.dev/r/"), strings.HasPrefix(s, "tuios.dev/r/"):
		id := s[strings.Index(s, "/r/")+3:]
		id = strings.TrimSuffix(id, ".toml")
		if !validGalleryID(id) {
			return lookSource{}, fmt.Errorf("%q is not a gallery id", id)
		}
		return lookSource{remote: true, where: lookGallery + id + ".toml"}, nil
	case strings.HasPrefix(s, "https://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return lookSource{}, fmt.Errorf("%q is not a URL", s)
		}
		return lookSource{remote: true, where: u.String()}, nil
	case strings.Contains(s, "://"):
		return lookSource{}, errors.New("tuios downloads a look only over https. Use an https URL")
	case exists(s):
		return lookSource{where: s}, nil
	case validGalleryID(s):
		return lookSource{remote: true, where: lookGallery + s + ".toml"}, nil
	}
	return lookSource{}, fmt.Errorf("%s is not a file, an https URL or a gallery id", s)
}

// validGalleryID reports whether id can be a gallery id: 1 to 64 of a-z, A-Z,
// 0-9, '-' and '_'.
func validGalleryID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// readLookFile reads a local look, with the size cap.
func readLookFile(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // the file the person named
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readCapped(f)
}

// readCapped reads at most config.MaxLookBytes and fails past it.
func readCapped(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, config.MaxLookBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read the look: %w", err)
	}
	if len(data) > config.MaxLookBytes {
		return nil, fmt.Errorf("the look is larger than %d KiB", config.MaxLookBytes>>10)
	}
	return data, nil
}

// lookHTTP downloads looks: a timeout for the whole request, and a redirect
// only to another https URL.
var lookHTTP = &http.Client{
	Timeout: lookFetchTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return errors.New("the look redirects to a URL that is not https")
		}
		return nil
	},
}

// fetchLook downloads the look at an https URL.
func fetchLook(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/toml, text/plain")
	req.Header.Set("User-Agent", "tuios/"+version)
	resp, err := lookHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download the look: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s has no look", u)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download the look: %s", resp.Status)
	}
	return readCapped(resp.Body)
}
