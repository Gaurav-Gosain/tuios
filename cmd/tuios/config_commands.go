package main

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newConfigCommand creates the config command for managing TUIOS configuration.
func newConfigCommand() *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage TUIOS configuration",
		Long:  `Manage TUIOS configuration file and settings`,
	}

	// Register the config subcommands.
	configCmd.AddCommand(
		newConfigPathCommand(),
		newConfigEditCommand(),
		newConfigResetCommand(),
		newConfigApplyCommand(),
		newConfigFilesCommand(),
		newConfigOriginCommand(),
		newConfigPruneCommand(),
	)

	return configCmd
}

// newConfigPathCommand creates the command that prints the path to the
// TUIOS configuration file.
func newConfigPathCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print configuration file path",
		Long:  `Print the path to the TUIOS configuration file`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return printConfigPath()
		},
	}
}

// newConfigEditCommand creates the command that opens the TUIOS
// configuration file in the user's preferred editor.
func newConfigEditCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Edit configuration in $EDITOR",
		Long: `Open the TUIOS configuration file in your default editor

The editor is determined by checking $EDITOR, $VISUAL, or common editors
like vim, vi, nano, and emacs in that order.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return editConfigFile()
		},
	}
}

// newConfigResetCommand creates the command that resets the main TUIOS
// configuration file to its default state while preserving its include list.
func newConfigResetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Reset configuration to defaults",
		Long: `Reset the TUIOS configuration file to default settings

This will overwrite your existing configuration after confirmation.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return resetConfigToDefaults()
		},
	}
}

// newConfigApplyCommand creates the command that asks the running TUIOS
// daemon to apply configuration changes that require explicit activation.
func newConfigApplyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Apply config.toml to the running daemon now",
		Long: `Apply config.toml to the running daemon now, including changes that give panes or other machines more.

The daemon applies a change to the file at once only where it gives less. A change that gives more waits for this command or a daemon restart. Run it from a terminal outside tuios. It is refused from inside a pane.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			client, err := dialVerb()
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			raw, err := client.Call("apply-config", nil)
			if err != nil {
				return explainVerbError("apply-config", err)
			}
			fmt.Print(describeConfigApplied(raw))
			return nil
		},
	}
}

// newConfigFilesCommand creates the command that lists the configuration
// files loaded by TUIOS in precedence order, with an optional JSON output.
func newConfigFilesCommand() *cobra.Command {
	var configFilesJSON bool
	configFilesCmd := &cobra.Command{
		Use:   "files",
		Short: "List the files the config is read from",
		Long: `List the files the config is read from, from lowest to highest precedence.

config.toml can name more files in a top-level include list. Every *.toml file
in the config.d directory next to config.toml is read too. A later file wins
over an earlier one, and config.toml wins over all of them. The list marks a
file that tuios cannot write as read-only, and shows a warning for an include
that names a missing file or makes a cycle.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigFiles(cmd.OutOrStdout(), configFilesJSON)
		},
	}
	configFilesCmd.Flags().BoolVar(&configFilesJSON, "json", false, "Print the files as JSON")
	return configFilesCmd
}

// newConfigOriginCommand creates the command that reports which
// configuration files define each effective setting, optionally filtered
// by key and formatted as JSON.
func newConfigOriginCommand() *cobra.Command {
	var configOriginJSON bool
	configOriginCmd := &cobra.Command{
		Use:   "origin [key]",
		Short: "Show which config file sets each key",
		Long: `Show which config file sets each key.

Each line gives a key, the file whose value is in force, and the other files
that set the same key. Give a key, such as appearance.theme or hosts, to show
that key and the keys under it only. A key that no file sets has its default
value.`,
		Example: `  tuios config origin
  tuios config origin appearance.theme
  tuios config origin hosts`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := ""
			if len(args) == 1 {
				key = args[0]
			}
			return runConfigOrigin(cmd.OutOrStdout(), key, configOriginJSON)
		},
	}
	configOriginCmd.Flags().BoolVar(&configOriginJSON, "json", false, "Print the keys as JSON")
	return configOriginCmd
}

// newConfigPruneCommand creates the command that removes default-valued
// keys from the main configuration file so that included configuration
// files can take effect, with optional dry-run and confirmation bypass.
func newConfigPruneCommand() *cobra.Command {
	var configPruneDryRun, configPruneYes bool
	configPruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove the keys of config.toml that have their default value",
		Long: `Remove the keys of config.toml that have their default value.

An older tuios wrote every key into config.toml. config.toml wins over the
files it includes and the files in config.d, so those keys hide the same keys
in the other files. This command removes each key that has its default value.
A key that another file sets then takes the value of that file. The command
lists those keys and asks first. Use --yes to skip the question. A run with no
terminal needs --yes. The [startup] keys stay, because a config.toml without
them means the old floating session. The command keeps comments and the
include list. Use --dry-run to see the keys first.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigPrune(cmd.OutOrStdout(), pruneOptions{
				dryRun: configPruneDryRun, yes: configPruneYes,
				tty: term.IsTerminal(int(os.Stdin.Fd())), in: os.Stdin,
			})
		},
	}
	configPruneCmd.Flags().BoolVar(&configPruneDryRun, "dry-run", false, "Show the keys and change nothing")
	configPruneCmd.Flags().BoolVar(&configPruneYes, "yes", false, "Do not ask when another file then sets a key")
	return configPruneCmd
}

// printConfigPath prints the path to the TUIOS configuration file.
func printConfigPath() error {
	path, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("could not determine config path: %w", err)
	}
	fmt.Println(path)
	return nil
}

// editConfigFile opens the TUIOS configuration file in the user's preferred
// editor, creating a default configuration file if one does not exist.
func editConfigFile() error {
	configPath, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("could not determine config path: %w", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Printf("Config file doesn't exist, creating default at: %s\n", configPath)
		_, err := config.LoadUserConfig()
		if err != nil {
			return fmt.Errorf("could not create config file: %w", err)
		}
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		for _, e := range []string{"vim", "vi", "nano", "emacs"} {
			if _, err := exec.LookPath(e); err == nil {
				editor = e
				break
			}
		}
	}
	if editor == "" {
		return fmt.Errorf("no editor found. Please set $EDITOR environment variable")
	}

	cmd := exec.Command(editor, configPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to open editor: %w", err)
	}
	return nil
}

// resetConfigToDefaults resets the main configuration file to its default
// state after confirmation, preserving its include list and reporting any
// other configuration files that continue to apply.
func resetConfigToDefaults() error {
	configPath, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("could not determine config path: %w", err)
	}

	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Warning: This will overwrite your existing configuration at:\n")
		fmt.Printf("  %s\n\n", configPath)
		fmt.Printf("Are you sure you want to reset to defaults? (yes/no): ")

		var response string
		_, _ = fmt.Scanln(&response)
		response = strings.ToLower(strings.TrimSpace(response))

		if response != "yes" && response != "y" {
			fmt.Println("Reset cancelled.")
			return nil
		}
	}

	// The reset file holds no settings, so every key has its default and
	// every file the config includes applies. The include list stays: it is
	// where the rest of the config is, not a setting.
	if err := config.ResetConfig(configPath); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	fmt.Printf("Configuration reset to defaults\n")
	fmt.Printf("  Location: %s\n", configPath)
	fmt.Println("\nYou can customize it with: tuios config edit")
	// The reset is of config.toml alone. Say which other files still set
	// keys, so a setting that did not go back is not a surprise.
	if lc, err := config.LoadLayered(configPath); err == nil {
		var others []string
		for _, l := range lc.Layers {
			if l.Kind != config.LayerMain {
				others = append(others, lc.DisplayPath(l.Path))
			}
		}
		if len(others) > 0 {
			fmt.Println("These files still apply:")
			for _, o := range others {
				fmt.Println("  " + o)
			}
		}
	}

	return nil
}

// previewThemeColors initializes the named theme and prints its normal and
// bright ANSI colors with their RGB values and hexadecimal representations.
func previewThemeColors(themeName string) error {
	if !slices.Contains(theme.AvailableThemes(), themeName) {
		return fmt.Errorf("no theme named %q. Run 'tuios list-themes' to see the themes", themeName)
	}
	if err := theme.Initialize(themeName); err != nil {
		return fmt.Errorf("failed to initialize theme: %w", err)
	}

	currentTheme := theme.Current()
	if currentTheme == nil {
		return fmt.Errorf("no theme named %q. Run 'tuios list-themes' to see the themes", themeName)
	}

	fmt.Printf("Theme: %s\n\n", themeName)

	palette := theme.GetANSIPalette()

	colorNames := []string{
		"Black", "Red", "Green", "Yellow",
		"Blue", "Magenta", "Cyan", "White",
		"Bright Black", "Bright Red", "Bright Green", "Bright Yellow",
		"Bright Blue", "Bright Magenta", "Bright Cyan", "Bright White",
	}

	fmt.Println("Normal Colors (0-7):")
	for i := range 8 {
		c := palette[i]
		r, g, b, _ := c.RGBA()
		r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
		fmt.Printf("  \033[48;2;%d;%d;%dm    \033[0m  %-14s #%02x%02x%02x\n", r8, g8, b8, colorNames[i], r8, g8, b8)
	}

	fmt.Println()

	fmt.Println("Bright Colors (8-15):")
	for i := 8; i < 16; i++ {
		c := palette[i]
		r, g, b, _ := c.RGBA()
		r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
		fmt.Printf("  \033[48;2;%d;%d;%dm    \033[0m  %-14s #%02x%02x%02x\n", r8, g8, b8, colorNames[i], r8, g8, b8)
	}

	return nil
}
