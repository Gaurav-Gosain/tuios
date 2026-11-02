package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/spf13/cobra"
)

// tuios plugins update: move installed plugins to the newest commit of the
// ref they follow. It shares the install path: a new build folder, so a
// failed build changes nothing that runs, then the state file, the lock file
// and a restart.
//
// The person sees the commits, and answers again, also with --yes, when the
// trust surface changed, the history of the branch was rewritten or a tag
// moved. docs/PLUGINS.md says the same.

// maxUpdateCommits bounds the commits an update shows.
const maxUpdateCommits = 20

func newPluginsUpdateCommand() *cobra.Command {
	var o installOpts
	var check bool
	cmd := &cobra.Command{
		Use:   "update [ID...]",
		Short: "Update installed plugins to the newest commit",
		Long: `Update installed plugins. With no ID, tuios updates each plugin in plugins.lock.

tuios shows the new commits and what the plugin runs, and asks first. --yes skips the question. tuios asks again, also with --yes, when what the plugin runs or asks for changes, when the history of its branch was rewritten or when its tag moved.

--check shows what an update would do and changes nothing. The command exits 3 when tuios cannot reach a source. That plugin stays at its commit.

A plugin installed from a folder has no update. A plugin pinned to a commit stays there. To follow another ref, run tuios plugins install SOURCE@REF.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inPane() {
				return errFromPane("update")
			}
			// An update keeps a plugin as it is: on stays on, off stays off.
			o.noEnable = true
			return updatePlugins(cmd, args, o, check)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&check, "check", false, "Show what an update would do and change nothing")
	f.BoolVarP(&o.yes, "yes", "y", false, "Update without the question, unless the plugin's trust changed")
	f.StringSliceVar(&o.grants, "grant", nil, "Give the admin grant to a plugin that asks for it")
	return cmd
}

// errSourceUnreachable marks a failed fetch, which is not a failed update.
var errSourceUnreachable = errors.New("the source cannot be reached")

// updatePlugins is tuios plugins update.
func updatePlugins(cmd *cobra.Command, ids []string, o installOpts, check bool) error {
	in, release, err := newInstaller(cmd, o)
	if err != nil {
		return err
	}
	defer release()
	lock, err := herdrplugin.ReadLock(in.dirs.LockPath())
	if err != nil {
		return err
	}
	todo := lock.Plugins
	if len(ids) > 0 {
		todo = nil
		for _, id := range ids {
			e := lock.Find(id)
			if e == nil {
				return fmt.Errorf("plugins.lock has no plugin with the id %s. Run tuios plugins list to see the ids", id)
			}
			todo = append(todo, *e)
		}
	}
	if len(todo) == 0 {
		fmt.Fprintln(in.out, "No plugins are installed. Run tuios plugins install SOURCE to install one.")
		return nil
	}
	var failed, unreachable []string
	for _, e := range todo {
		if in.sig.ctx.Err() != nil {
			failed = append(failed, e.ID)
			continue
		}
		switch err := in.updateOne(e, check); {
		case errors.Is(err, errSourceUnreachable):
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: tuios cannot reach %s. It stays at %.12s.\n", e.ID, shownSource(e.Source), e.Commit)
			unreachable = append(unreachable, e.ID)
		case err != nil:
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", e.ID, err)
			failed = append(failed, e.ID)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("these plugins are not updated: %s", strings.Join(failed, ", "))
	}
	if len(unreachable) > 0 {
		return &callError{status: 3, err: fmt.Errorf("tuios could not reach the source of %s. These plugins stay at their commits. Check the network, then try again", strings.Join(unreachable, ", "))}
	}
	return nil
}

// updateOne updates one lock file entry, or shows what that would do.
func (in *installer) updateOne(e herdrplugin.LockEntry, check bool) error {
	if err := herdrplugin.CheckLockEntry(e); err != nil {
		return err
	}
	switch {
	case e.Local():
		fmt.Fprintf(in.out, "%s uses the folder %s. tuios does not update it.\n", e.ID, in.dirs.PluginRoot(e, ""))
		return nil
	case herdrplugin.IsCommit(e.Ref):
		fmt.Fprintf(in.out, "%s is pinned to the commit %.12s.\n", e.ID, e.Commit)
		return nil
	}
	fetched, got, p, err := in.fetchHistory(e)
	defer os.RemoveAll(fetched)
	if err != nil {
		return err
	}
	if got.Tree == e.Tree {
		fmt.Fprintf(in.out, "%s is up to date at %.12s.\n", e.ID, e.Commit)
		return nil
	}
	if p.PluginID != e.ID {
		return fmt.Errorf("the new commit names the plugin %s, not %s. Run tuios plugins uninstall %s, then install it again", p.PluginID, e.ID, e.ID)
	}
	reason := updateReason(fetched, e, got.Commit)
	fmt.Fprintf(in.out, "\n%s: %.12s to %.12s\n", e.ID, e.Commit, got.Commit)
	fmt.Fprint(in.out, commitRange(fetched, e, got.Commit, reason != ""))
	diff := in.trustDiff(e, p)
	fmt.Fprint(in.out, diff)
	if check {
		if reason != "" || diff != "" {
			fmt.Fprintln(in.out, "This update needs a new answer from you, also with --yes.")
		}
		return nil
	}
	next := e
	next.Commit, next.Tree = got.Commit, got.Tree
	in.ask, in.verb = reason, "Updated"
	return in.finish(next, p, fetched)
}

// fetchHistory gets the newest commit of what e follows into a new fetch
// folder, with its history and without the file contents of old commits.
func (in *installer) fetchHistory(e herdrplugin.LockEntry) (string, *herdrplugin.LockEntry, *herdrplugin.Plugin, error) {
	in.history = true
	defer func() { in.history = false }()
	work, got, p, err := in.fetch(e.Source, e.Ref, e.Subdir)
	if errors.Is(err, errFetchFailed) {
		return work, nil, nil, errSourceUnreachable
	}
	return work, got, p, err
}

// updateReason says why this update needs a fresh answer whatever the
// manifest says, "" for an ordinary update.
func updateReason(work string, e herdrplugin.LockEntry, commit string) string {
	if e.Ref != "" {
		if out, err := git(work, "ls-remote", "--tags", "origin", "refs/tags/"+e.Ref); err == nil && out != "" {
			return "The tag " + shownArgv(e.Ref) + " now points to a different commit."
		}
	}
	if _, err := git(work, "merge-base", "--is-ancestor", e.Commit, commit); err != nil {
		return "The history of this source was rewritten, so the pinned commit is not in the new one."
	}
	return ""
}

// commitRange lists the commits of the update, newest first and at most
// maxUpdateCommits, for the plugin's folder only. Each subject is the
// author's text, so a character that does not print is quoted.
func commitRange(work string, e herdrplugin.LockEntry, commit string, rewritten bool) string {
	if rewritten {
		if _, err := git(work, "merge-base", "--is-ancestor", e.Commit, commit); err != nil {
			return ""
		}
	}
	args := []string{"log", "--format=%h %s", e.Commit + ".." + commit}
	if e.Subdir != "" {
		args = append(args, "--", e.Subdir)
	}
	out, err := git(work, args...)
	if err != nil || out == "" {
		return ""
	}
	lines := strings.Split(out, "\n")
	var b strings.Builder
	for _, line := range lines[:min(maxUpdateCommits, len(lines))] {
		fmt.Fprintf(&b, "  %s\n", shownArgv(line))
	}
	if n := len(lines) - maxUpdateCommits; n > 0 {
		fmt.Fprintf(&b, "  and %d more\n", n)
	}
	return b.String()
}

// trustDiff shows what the new manifest runs or asks for that the running
// one does not, and the reverse. It is "" when the approved hash still holds.
func (in *installer) trustDiff(e herdrplugin.LockEntry, p *herdrplugin.Plugin) string {
	if herdrplugin.TrustHash(e.Source, p, p.Grants) == e.Approved {
		return ""
	}
	var was []string
	if rel, _, ok := in.state.Live(e); ok {
		if old, perr := herdrplugin.Load(in.dirs.PluginRoot(e, rel)); perr == nil {
			was = trustLines(old)
		}
	}
	now := trustLines(p)
	var b strings.Builder
	b.WriteString("What the plugin runs or asks for changes:\n")
	for _, line := range was {
		if !slices.Contains(now, line) {
			fmt.Fprintf(&b, "  - %s\n", line)
		}
	}
	for _, line := range now {
		if !slices.Contains(was, line) {
			fmt.Fprintf(&b, "  + %s\n", line)
		}
	}
	return b.String()
}

// trustLines are the parts of a manifest a person approves, one per line.
func trustLines(p *herdrplugin.Plugin) []string {
	var out []string
	for _, g := range p.Grants {
		out = append(out, "grant    "+g)
	}
	for _, b := range p.Build {
		out = append(out, "build    "+shownArgv(b.Command...))
	}
	for _, e := range p.Events {
		out = append(out, "hook     "+shownArgv(e.On)+": "+shownArgv(e.Command...))
	}
	for _, a := range p.Actions {
		out = append(out, "action   "+shownArgv(a.ID)+": "+shownArgv(a.Command...))
	}
	for _, x := range p.Panes {
		out = append(out, "pane     "+shownArgv(x.ID)+": "+shownArgv(x.Command...))
	}
	return append(out, herdrplugin.RunsLines(&herdrplugin.Plugin{Startup: p.Startup})...)
}
