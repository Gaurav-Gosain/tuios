// Package fang provides styling for cobra commands.
//
// It is a copy of github.com/charmbracelet/fang v1.0.0 (MIT, LICENSE.md
// here) with two things taken out: the hidden `man` command, which linked
// mango and roff to write a man page, and golang.org/x/text/cases, which
// upper-cased the first word of an error title. Together they were 375 KB
// of the tuios binary. The help and error output is otherwise the same.
package fang

import (
	"context"
	"io"
	"os"
	"os/signal"
	"runtime/debug"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
)

const shaLen = 7

// ErrorHandler handles an error, printing them to the given [io.Writer].
type ErrorHandler = func(w io.Writer, styles Styles, err error)

// ColorSchemeFunc gets a [lipgloss.LightDarkFunc] and returns a [ColorScheme].
type ColorSchemeFunc = func(lipgloss.LightDarkFunc) ColorScheme

type settings struct {
	completions bool
	skipVersion bool
	version     string
	commit      string
	colorscheme ColorSchemeFunc
	errHandler  ErrorHandler
	signals     []os.Signal
}

// Option changes fang settings.
type Option func(*settings)

// WithoutCompletions disables completions.
func WithoutCompletions() Option {
	return func(s *settings) {
		s.completions = false
	}
}

// WithColorSchemeFunc sets a function that return colorscheme.
func WithColorSchemeFunc(cs ColorSchemeFunc) Option {
	return func(s *settings) {
		s.colorscheme = cs
	}
}

// WithTheme sets the colorscheme.
//
// Deprecated: use [WithColorSchemeFunc] instead.
func WithTheme(theme ColorScheme) Option {
	return func(s *settings) {
		s.colorscheme = func(lipgloss.LightDarkFunc) ColorScheme {
			return theme
		}
	}
}

// WithVersion sets the version.
func WithVersion(version string) Option {
	return func(s *settings) {
		s.version = version
	}
}

// WithoutVersion skips the `-v`/`--version` functionality.
func WithoutVersion() Option {
	return func(s *settings) {
		s.skipVersion = true
	}
}

// WithCommit sets the commit SHA.
func WithCommit(commit string) Option {
	return func(s *settings) {
		s.commit = commit
	}
}

// WithErrorHandler sets the error handler.
func WithErrorHandler(handler ErrorHandler) Option {
	return func(s *settings) {
		s.errHandler = handler
	}
}

// WithNotifySignal sets the signals that should interrupt the execution of the
// program.
func WithNotifySignal(signals ...os.Signal) Option {
	return func(s *settings) {
		s.signals = signals
	}
}

// Execute applies fang to the command and executes it.
func Execute(ctx context.Context, root *cobra.Command, options ...Option) error {
	opts := settings{
		completions: true,
		colorscheme: DefaultColorScheme,
		errHandler:  DefaultErrorHandler,
	}

	for _, option := range options {
		option(&opts)
	}

	// Enable VT processing on Windows, otherwise, ANSI escape sequences might not work on older
	// Windows versions. This is a no-op on other platforms.
	for _, w := range []io.Writer{root.OutOrStdout(), root.ErrOrStderr()} {
		// if we can't enable VT processing, just ignore it and continue
		// without it.
		_ = enableVirtualTerminalProcessing(w)
	}

	helpFunc := func(c *cobra.Command, _ []string) {
		w := colorprofile.NewWriter(c.OutOrStdout(), os.Environ())
		helpFn(c, w, makeStyles(mustColorscheme(opts.colorscheme)))
	}

	root.SilenceUsage = true
	root.SilenceErrors = true
	if !opts.skipVersion {
		root.Version = buildVersion(opts)
	}
	root.SetHelpFunc(helpFunc)

	if !opts.completions {
		root.CompletionOptions.DisableDefaultCmd = true
	}

	if len(opts.signals) > 0 {
		var cancel context.CancelFunc
		ctx, cancel = signal.NotifyContext(ctx, opts.signals...)
		defer cancel()
	}

	if err := root.ExecuteContext(ctx); err != nil {
		w := colorprofile.NewWriter(root.ErrOrStderr(), os.Environ())
		opts.errHandler(w, makeStyles(mustColorscheme(opts.colorscheme)), err)
		return err //nolint:wrapcheck
	}
	return nil
}

func buildVersion(opts settings) string {
	commit := opts.commit
	version := opts.version
	if version == "" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Sum != "" {
			version = info.Main.Version
			commit = getKey(info, "vcs.revision")
		} else {
			version = "unknown (built from source)"
		}
	}
	if len(commit) >= shaLen {
		version += " (" + commit[:shaLen] + ")"
	}
	return version
}

func getKey(info *debug.BuildInfo, key string) string {
	if info == nil {
		return ""
	}
	for _, iter := range info.Settings {
		if iter.Key == key {
			return iter.Value
		}
	}
	return ""
}
