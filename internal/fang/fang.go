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
	"runtime/debug"

	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
)

const shaLen = 7

// ErrorHandler handles an error, printing them to the given [io.Writer].
type ErrorHandler = func(w io.Writer, styles Styles, err error)

type settings struct {
	version    string
	errHandler ErrorHandler
}

// Option changes fang settings.
type Option func(*settings)

// WithVersion sets the version.
func WithVersion(version string) Option {
	return func(s *settings) {
		s.version = version
	}
}

// WithErrorHandler sets the error handler.
func WithErrorHandler(handler ErrorHandler) Option {
	return func(s *settings) {
		s.errHandler = handler
	}
}

// Execute applies fang to the command and executes it.
func Execute(ctx context.Context, root *cobra.Command, options ...Option) error {
	opts := settings{errHandler: DefaultErrorHandler}

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
		helpFn(c, w, makeStyles(mustColorscheme(DefaultColorScheme)))
	}

	root.SilenceUsage = true
	root.SilenceErrors = true
	root.Version = buildVersion(opts)
	root.SetHelpFunc(helpFunc)

	if err := root.ExecuteContext(ctx); err != nil {
		w := colorprofile.NewWriter(root.ErrOrStderr(), os.Environ())
		opts.errHandler(w, makeStyles(mustColorscheme(DefaultColorScheme)), err)
		return err //nolint:wrapcheck
	}
	return nil
}

func buildVersion(opts settings) string {
	var commit string
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
