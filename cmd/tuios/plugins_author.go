package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/plugins"
	"github.com/spf13/cobra"
)

// The tools of a plugin author: new makes a folder from plugins/template,
// check reads a manifest as tuios would and says what is wrong, and dev runs
// a folder and reloads it when a file changes.

func newPluginsNewCommand() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "new NAME",
		Short: "Make a plugin folder that works, with a manifest, a script, a README and CI",
		Long:  "Make the folder NAME with a plugin that works: a manifest, a shell script, a README and a GitHub workflow that checks the manifest and attaches the plugin to a release when you push a tag. --id sets the plugin id. It is you.NAME by default.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := filepath.Base(args[0])
			if id == "" {
				id = "you." + strings.ToLower(name)
			}
			if n, ok := herdrplugin.NormalizeID(id); !ok || n != id {
				return fmt.Errorf("%q is not a plugin id. Use letters, digits, ., - and _, such as you.%s", id, strings.ToLower(name))
			}
			if _, err := os.Stat(args[0]); err == nil {
				return fmt.Errorf("%s exists. Nothing was changed. Give a new folder", args[0])
			}
			if err := writeTemplate(args[0], id, name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Made the plugin %s in %s.\nTry it: tuios plugins dev %s\n", id, args[0], args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "The plugin id, such as you.name")
	return cmd
}

// writeTemplate copies the template to dir with the id and name put in.
func writeTemplate(dir, id, name string) error {
	return fs.WalkDir(plugins.Template, "template", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "template"), "/")
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755) //nolint:gosec // a source folder the person shares, as git makes it
		}
		data, err := plugins.Template.ReadFile(p)
		if err != nil {
			return err
		}
		text := strings.ReplaceAll(string(data), plugins.TemplateID, id)
		text = strings.ReplaceAll(text, "# "+plugins.TemplateName+"\n", "# "+name+"\n")
		text = strings.ReplaceAll(text, `name = "`+plugins.TemplateName+`"`, `name = "`+name+`"`)
		text = strings.ReplaceAll(text, plugins.TemplateName+": ", name+": ")
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, ".sh") {
			mode = 0o755
		}
		return os.WriteFile(target, []byte(text), mode) //nolint:gosec // the person's own new folder
	})
}

func newPluginsCheckCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "check [PATH]",
		Short: "Read a plugin folder as tuios would and say what is wrong",
		Long:  "Read the manifest in PATH, or in this folder, as tuios would, and check that each program it names with a / is in the folder. It prints each problem and each warning, and exits 1 when the plugin cannot load. It runs nothing and needs no daemon, so a CI job can run it.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return checkPlugin(cmd.OutOrStdout(), dir)
		},
	}
}

// checkPlugin is tuios plugins check.
func checkPlugin(out io.Writer, dir string) error {
	p, perr := herdrplugin.Load(dir)
	if perr != nil {
		return &callError{status: 1, err: fmt.Errorf("the plugin cannot load: %s", perr.Msg)}
	}
	var problems []string
	for _, argv := range pluginCommands(p) {
		prog := argv[0]
		if strings.ContainsAny(prog, `/\`) && !filepath.IsAbs(prog) {
			if _, err := os.Stat(filepath.Join(p.PluginRoot, prog)); err != nil {
				problems = append(problems, fmt.Sprintf("the program %s is not in the folder", prog))
			}
		}
		// sh script.sh: the script must be there.
		if (prog == "sh" || prog == "bash") && len(argv) > 1 && !strings.HasPrefix(argv[1], "-") {
			if _, err := os.Stat(filepath.Join(p.PluginRoot, argv[1])); err != nil {
				problems = append(problems, fmt.Sprintf("the script %s is not in the folder", argv[1]))
			}
		}
	}
	fmt.Fprintf(out, "%s %s (%s plugin)\n", p.PluginID, shownArgv(p.Version), p.Format)
	for _, line := range herdrplugin.RunsLines(p) {
		fmt.Fprintf(out, "  %s\n", line)
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	for _, pr := range problems {
		fmt.Fprintf(out, "problem: %s\n", pr)
	}
	if len(problems) > 0 {
		return &callError{status: 1, err: errors.New("the plugin has problems. Fix them, then run tuios plugins check again")}
	}
	fmt.Fprintln(out, "The plugin loads.")
	return nil
}

// pluginCommands are the argv of every command p runs.
func pluginCommands(p *herdrplugin.Plugin) [][]string {
	var out [][]string
	for _, b := range p.Build {
		out = append(out, b.Command)
	}
	for _, s := range p.Startup {
		out = append(out, s.Command)
	}
	for _, e := range p.Events {
		out = append(out, e.Command)
	}
	for _, a := range p.Actions {
		out = append(out, a.Command)
	}
	for _, x := range p.Panes {
		out = append(out, x.Command)
	}
	return out
}

func newPluginsDevCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dev PATH",
		Short: "Run a plugin folder and reload it when a file changes",
		Long: `Install the plugin folder PATH in place, and reload it each time a file in it changes, until you press Ctrl-C.

A changed manifest needs a new approval. dev gives it, for the folder you named and nothing else, so it asks once when it starts. Run it from a terminal outside tuios.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inPane() {
				return errFromPane("dev")
			}
			dir, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			if err := checkPlugin(cmd.OutOrStdout(), dir); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if err := installPlugin(cmd, dir, installOpts{}); err != nil {
				return err
			}
			fmt.Fprintln(out, "Watching the folder. Press Ctrl-C to stop. The plugin stays installed.")
			stop := make(chan os.Signal, 1)
			signal.Notify(stop, os.Interrupt)
			seen := folderStamp(dir)
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-stop:
					return nil
				case <-tick.C:
				}
				now := folderStamp(dir)
				if now == seen {
					continue
				}
				seen = now
				fmt.Fprintf(out, "%s A file changed. Reloading.\n", time.Now().Format("15:04:05"))
				if err := checkPlugin(io.Discard, dir); err != nil {
					// The folder is used in place, so the daemon reads the
					// broken manifest too, and the plugin stops until it loads.
					fmt.Fprintf(out, "The plugin cannot load, and it does not run: %v. Fix the folder. dev reloads it when it loads. Run tuios plugins check %s to see why\n", err, args[0])
					continue
				}
				if err := installPlugin(cmd, dir, installOpts{yes: true}); err != nil {
					fmt.Fprintf(out, "The reload failed: %v\n", err)
				}
			}
		},
	}
}

// folderStamp is the newest change time and the count of the files in dir,
// outside .git. It changes when a file is written, added or removed.
func folderStamp(dir string) string {
	var newest time.Time
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if info, err := d.Info(); err == nil && !d.IsDir() {
			n++
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
		return nil
	})
	return fmt.Sprintf("%d %d", newest.UnixNano(), n)
}
