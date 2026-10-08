package main

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/skills"
	tint "github.com/lrstanley/bubbletint/v2"
	"github.com/spf13/cobra"
)

// newRootCommandBase initializes the root command of this CLI application (tuios) and configures its flags and subcommands.
func newRootCommandBase() *cobra.Command {

	rootCmd := &cobra.Command{
		Use:   "tuios",
		Short: "Terminal UI Operating System",
		Long: `TUIOS: Terminal UI Operating System

A terminal-based window manager that provides a modern interface for managing
multiple terminal sessions with workspace support, tiling modes, and
comprehensive keyboard/mouse interactions.`,
		Example: `  # Run TUIOS
  tuios

  # Run with debug logging
  tuios --debug

  # Run with ASCII-only mode (no Nerd Font icons)
  tuios --ascii-only

  # Run with CPU profiling
  tuios --cpuprofile cpu.prof

  # Run with a specific theme
  tuios --theme dracula

  # List all available themes
  tuios --list-themes

  # Preview a theme's colors
  tuios --preview-theme dracula

  # Interactively select theme with fzf and preview
  tuios --theme $(tuios --list-themes | fzf --preview 'tuios --preview-theme {}')

  # Run as SSH server
  tuios ssh --port 2222

  # Edit configuration
  tuios config edit

  # List all keybindings
  tuios keybinds list

  # Print the agent skill for driving tuios from a pane
  tuios --skill

  # Print one topic of it, or all of it
  tuios --skill fleet
  tuios --skill all`,
		Version: version,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The skill is printed before anything else can decide to draw: it is
			// a document, and a caller asking for it never wants the interface.
			if cmd.Flags().Changed("skill") {
				text, err := skills.Lookup(skillTopic)
				if err != nil {
					return err
				}
				fmt.Print(text)
				return nil
			}

			if previewTheme != "" {
				return previewThemeColors(previewTheme)
			}

			if listThemes {
				theme.EnsureRegistry()
				themes := tint.TintIDs()
				for _, t := range themes {
					fmt.Println(t)
				}
				return nil
			}
			return runLocal()
		},
		SilenceUsage: true,
	}
	rootCmd.PersistentFlags().BoolVar(&debugMode, "debug", false, "Enable debug logging")
	rootCmd.PersistentFlags().StringVar(&cpuProfile, "cpuprofile", "", "Write CPU profile to file")
	rootCmd.PersistentFlags().StringVar(&pprofAddr, "pprof", "", "Serve /debug/pprof profiles on this address, for example :6060. With no host, it listens on 127.0.0.1 only. The profiles have no password")

	// Local to the root command: the skill describes tuios as a whole, and the
	// theme listing and preview are root-level actions that print and exit, so
	// offering them on every subcommand would only add noise to their help.
	//
	// --skill takes an optional topic. A bare --skill prints the core, and
	// skillArgs turns "--skill TOPIC" into "--skill=TOPIC" before cobra sees
	// it, because an optional value only binds with "=", and a topic such as
	// mcp or hosts is also the name of a subcommand.
	rootCmd.Flags().StringVar(&skillTopic, "skill", "", "Print the agent skill for driving tuios from a pane and exit. --skill TOPIC prints one topic, --skill all prints every topic")
	rootCmd.Flags().Lookup("skill").NoOptDefVal = "core"
	// The way out of startup.daemon for one run. It is on the root command
	// because that is the only command the setting changes.
	rootCmd.Flags().BoolVar(&standaloneMode, "standalone", false, "Run a standalone session without the daemon, overriding startup.daemon (TUIOS_NO_DAEMON=1 does the same for a whole shell)")
	rootCmd.Flags().BoolVar(&listThemes, "list-themes", false, "List all available themes and exit")
	rootCmd.Flags().StringVar(&previewTheme, "preview-theme", "", "Preview a theme's 16 ANSI colors")

	return rootCmd
}
