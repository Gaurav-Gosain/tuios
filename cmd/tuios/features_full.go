//go:build !slim

package main

import (
	"github.com/Gaurav-Gosain/tuios/skills"
	"github.com/spf13/cobra"
)

// addFeatureCommands adds the commands tuios-slim leaves out. Each one is
// defined in a file tagged !slim, so the slim build links none of them.
// features_slim.go adds a stub under each top-level name instead.
func addFeatureCommands(root *cobra.Command) {
	root.AddCommand(newSSHCommand(), newTapeCommand(), newUpdateCommand(), newScreenshotCommand())
	root.AddCommand(newAgentStateCommands()...)
	root.AddCommand(newAgentMessageCommands()...)
	root.AddCommand(newHostsCommands()...)
	root.AddCommand(newResumeAgentCommand(), newListAttentionCommand(), newPeekPromptCommand(),
		newRespondCommand(), newQueueCommand(), newReviewCommand())
	root.AddCommand(newSubscribeCommand(), newRunCommand(), newAskHumanCommand(), newStashCommand())
	root.AddCommand(newWorktreeCommand(), newFanCommand(), newStartAgentCommand())
	root.AddCommand(newAgentHookCommand(), newAgentStatusLineCommand(), newIntegrationCommand(), newDoctorCommand(), newMCPCommand())
	root.AddCommand(newTmuxCommand(), newTmuxShimCommand(), newTmuxPaneCommand())
	root.AddCommand(newAgentProtoCommand(), newAgentLogCommand())
	root.AddCommand(newHerdrGroupCommand("pane"), newHerdrGroupCommand("notification"))
}

// runAsLinkedProgram runs this binary as the program its name says, when it
// was started through a link with another program's name. Through the tmux
// link that tuios tmux-shim installs, it is tmux: the shim answers, or hands
// the call to the real tmux. Through the herdr link the daemon makes, it is
// herdr's command line, as tools built for herdr call it. ok is false for
// any other name.
func runAsLinkedProgram(arg0 string, args []string) (code int, ok bool) {
	switch {
	case isTmuxName(arg0):
		return runAsTmux(args), true
	case isHerdrName(arg0):
		return runAsHerdr(args), true
	}
	return 0, false
}

// capturePaneHostHelp and capturePaneHostExample are the parts of the
// capture-pane help about panes on other machines. tuios-slim has no links
// to other machines, so its help leaves them out. See host_slim.go.
const (
	capturePaneHostHelp = `

A capture from a session on another machine (-s host:session) is fenced as
untrusted content. With --ansi or --resolved, only colour and style codes are
kept from it. With --json the result carries host and "untrusted": true.`
	capturePaneHostExample = `

  # Read pane 0 of session api on host build, as JSON
  tuios capture-pane -w build:api:0 --json`
)

// hideHostFlags does nothing in the full build, which has every host flag.
func hideHostFlags(...*cobra.Command) {}

// skillDocument is what tuios --skill prints for topic.
func skillDocument(topic string) (string, error) { return skills.Lookup(topic) }
