package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/herdrcli"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

func isHerdrName(arg0 string) bool {
	base := strings.ToLower(filepath.Base(arg0))
	return base == "herdr" || base == "herdr.exe"
}

func runAsHerdr(args []string) int {
	return herdrcli.Main(args, herdrcli.Options{Socket: herdrSocketPath, PluginConfigDir: pluginConfigDir})
}

func herdrSocketPath() (string, error) {
	sock, err := session.GetSocketPath()
	if err != nil {
		return "", err
	}
	return session.HerdrSocketPath(sock), nil
}

func newHerdrGroupCommand(group string) *cobra.Command {
	return &cobra.Command{
		Use:                group + " <herdr arguments>",
		Short:              "herdr's " + group + " commands, for tools built for herdr",
		Long:               "herdr's " + group + " commands, for tools built for herdr. Run herdr " + group + " help, through $HERDR_BIN_PATH, for the subcommands.",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			args, err := applyLeadingServerFlag(args)
			if err != nil {
				return err
			}
			if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
				return cmd.Help()
			}
			code := herdrcli.Main(append([]string{group}, args...), herdrcli.Options{
				Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), Socket: herdrSocketPath, PluginConfigDir: pluginConfigDir,
			})
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}
