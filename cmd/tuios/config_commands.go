package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newConfigCommand() *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage TUIOS configuration",
		Long:  `Manage TUIOS configuration file and settings`,
	}

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

func newConfigApplyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Apply config.toml to the running daemon and clients now",
		Long: `Apply config.toml to the running daemon and to every tuios client on this machine now, including changes that give panes or other machines more.

The daemon applies a change to the file at once only where it gives less. A change that gives more waits for this command or a daemon restart. Run it from a terminal outside tuios. It is refused from inside a pane.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			raw, err := localVerb("apply-config", nil, 0, false)
			if err != nil {
				return err
			}
			fmt.Print(describeConfigApplied(raw))
			askClientsToApply()
			return nil
		},
	}
}

func askClientsToApply() {
	path, err := config.GetConfigPath()
	if err == nil {
		stamp := filepath.Join(filepath.Dir(path), config.ConfigApplyStamp)
		err = os.WriteFile(stamp, []byte(time.Now().Format(time.RFC3339Nano)), 0o600)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tuios could not tell the clients to apply config.toml: %v. Run this command again, or restart the clients.\n", err)
	}
}

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
	addJSONFlag(configFilesCmd, &configFilesJSON, "Print the files as JSON")
	return configFilesCmd
}

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
	addJSONFlag(configOriginCmd, &configOriginJSON, "Print the keys as JSON")
	return configOriginCmd
}

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

func printConfigPath() error {
	path, err := config.GetConfigPath()
	if err != nil {
		return errfmt.Errorf("could not determine config path: %w", err)
	}
	fmt.Println(path)
	return nil
}

func editConfigFile() error {
	configPath, err := config.GetConfigPath()
	if err != nil {
		return errfmt.Errorf("could not determine config path: %w", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Printf("Config file doesn't exist, creating default at: %s\n", configPath)
		_, err := config.LoadUserConfig()
		if err != nil {
			return errfmt.Errorf("could not create config file: %w", err)
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
		return errfmt.Errorf("no editor found. Please set $EDITOR environment variable")
	}

	cmd := exec.Command(editor, configPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return errfmt.Errorf("failed to open editor: %w", err)
	}
	return nil
}

func resetConfigToDefaults() error {
	configPath, err := config.GetConfigPath()
	if err != nil {
		return errfmt.Errorf("could not determine config path: %w", err)
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

	if err := config.ResetConfig(configPath); err != nil {
		return errfmt.Errorf("failed to write config file: %w", err)
	}
	fmt.Printf("Configuration reset to defaults\n")
	fmt.Printf("  Location: %s\n", configPath)
	fmt.Println("\nYou can customize it with: tuios config edit")
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

func previewThemeColors(themeName string) error {
	if !slices.Contains(theme.AvailableThemes(), themeName) {
		return errfmt.Errorf("no theme named %q. Run 'tuios list-themes' to see the themes", themeName)
	}
	if err := theme.Initialize(themeName); err != nil {
		return errfmt.Errorf("failed to initialize theme: %w", err)
	}

	currentTheme := theme.Current()
	if currentTheme == nil {
		return errfmt.Errorf("no theme named %q. Run 'tuios list-themes' to see the themes", themeName)
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
