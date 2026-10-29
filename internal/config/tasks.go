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
}

// AutoStageOn reports whether the daemon moves cards on its own.
func (c TasksConfig) AutoStageOn() bool { return c.AutoStage == nil || *c.AutoStage }

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
