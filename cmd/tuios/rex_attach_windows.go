//go:build windows

package main

import (
	"errors"

	"github.com/spf13/cobra"
)

func newRexAttachCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "rex-attach [TARGET]",
		Short:  "Run a Rex shell as the program of this pane",
		Hidden: true,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("tuios does not offer Rex panes on Windows")
		},
	}
}
