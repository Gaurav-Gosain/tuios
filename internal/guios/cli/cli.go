// Package cli is what the app needs of a command line: the agent
// integrations and stopping the daemon. tuios has the command line, so this
// package runs the tuios binary (Tuios) rather than carrying one of its own.
package cli

import (
	"encoding/json"
	"errors"
	"os/exec"
)

// Tuios is the tuios binary the app found, set at start.
var Tuios = "tuios"

// IsCommand reports whether args name a command guios runs itself. tuios
// is the command line, so none.
func IsCommand(string) bool { return false }

// Run runs a command. Unused: see IsCommand.
func Run([]string) int { return 2 }

// SyncSkill keeps the agents' integrations current. tuios updates its own
// hooks with `tuios integration install`, so nothing runs at start.
func SyncSkill() {}

// StopDaemon stops the tuios daemon and every session in it.
func StopDaemon() error { return exec.Command(Tuios, "kill-server").Run() }

// SkillState is one harness's integration: Agent its id, Name what it is
// called, Dir the file tuios writes, State current, outdated or missing.
type SkillState struct {
	Agent string
	Name  string
	Dir   string
	State string
}

// SkillStates lists the integrations of the harnesses installed on this
// machine, from `tuios integration status`.
func SkillStates() []SkillState {
	out, err := exec.Command(Tuios, "integration", "status", "--json").Output()
	if err != nil {
		return nil
	}
	var rows []struct {
		Harness   string `json:"harness"`
		Name      string `json:"name"`
		Path      string `json:"path"`
		Exists    bool   `json:"config_dir_exists"`
		Installed bool   `json:"installed"`
		Current   bool   `json:"current"`
	}
	if json.Unmarshal(out, &rows) != nil {
		return nil
	}
	var states []SkillState
	for _, r := range rows {
		if !r.Exists {
			continue
		}
		st := "missing"
		switch {
		case r.Installed && r.Current:
			st = "current"
		case r.Installed:
			st = "outdated"
		}
		states = append(states, SkillState{Agent: r.Harness, Name: r.Name, Dir: r.Path, State: st})
	}
	return states
}

// InstallSkill installs or updates one harness's integration.
func InstallSkill(agent string) error { return run("integration", "install", agent) }

// RemoveSkill removes one harness's integration.
func RemoveSkill(agent string) error { return run("integration", "uninstall", agent) }

func run(args ...string) error {
	out, err := exec.Command(Tuios, args...).CombinedOutput()
	if err != nil && len(out) > 0 {
		return errors.New(string(out))
	}
	return err
}
