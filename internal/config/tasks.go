package config

import (
	"fmt"
	"slices"
	"strings"
)

// The task stages, in the order a card usually moves through them. They are
// wire values; internal/session re-exports them.
const (
	TaskStageTodo    = "todo"
	TaskStagePlan    = "plan"
	TaskStageDoing   = "doing"
	TaskStageVerify  = "verify"
	TaskStageReview  = "review"
	TaskStageShipped = "shipped"
	TaskStageDone    = "done"
)

// TaskStages is every stage, in order.
var TaskStages = []string{TaskStageTodo, TaskStagePlan, TaskStageDoing, TaskStageVerify, TaskStageReview, TaskStageShipped, TaskStageDone}

// TasksConfig is the [tasks] table: how the task board in the agents view
// looks, and whether the daemon moves cards on its own.
type TasksConfig struct {
	// Stages are the stages the board shows, always in TaskStages order.
	// Empty shows every stage.
	Stages []string `toml:"stages,omitempty"`
	// AutoStage moves a card when the daemon records a fact about its work:
	// a plan held, its agent working, a fan check, a pull request. Nil means
	// on.
	AutoStage *bool `toml:"auto_stage,omitempty"`
	// MachineCap is how many agents may work on this machine before "Any
	// machine" places a new one elsewhere: working agents, agents still
	// starting and picks not yet started count. Zero or less is no cap.
	MachineCap int `toml:"machine_cap,omitempty"`
}

// AutoStageOn reports whether the daemon moves cards on its own.
func (c TasksConfig) AutoStageOn() bool { return c.AutoStage == nil || *c.AutoStage }

// LifecycleOn reports whether an agent's finished card goes to verify and
// review rather than to done: cards move on their own, and the board shows
// review.
func (c TasksConfig) LifecycleOn() bool { return c.AutoStageOn() && c.Shows(TaskStageReview) }

// Shows reports whether the board shows stage. A list that names no known
// stage shows every one.
func (c TasksConfig) Shows(stage string) bool {
	if !slices.ContainsFunc(c.Stages, func(s string) bool { return slices.Contains(TaskStages, s) }) {
		return true
	}
	return slices.Contains(c.Stages, stage)
}

func validateTasks(cfg *UserConfig, result *ValidationResult) {
	for _, s := range cfg.Tasks.Stages {
		if !slices.Contains(TaskStages, s) {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "tasks", Key: "stages",
				Message: fmt.Sprintf("%q is not a task stage; ignored. The stages are %s", s, strings.Join(TaskStages, ", ")),
			})
		}
	}
}
