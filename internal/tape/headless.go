package tape

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// HeadlessRunner runs a tape script in headless mode without rendering a TUI
// It executes commands and captures output for logging/export
type HeadlessRunner struct {
	commands   []Command
	outputPath string
	output     strings.Builder
	outputLock sync.Mutex
	verbose    bool
	startTime  time.Time
}

// NewHeadlessRunner creates a new headless script runner
func NewHeadlessRunner(commands []Command, outputPath string) *HeadlessRunner {
	return &HeadlessRunner{
		commands:   commands,
		outputPath: outputPath,
		verbose:    false,
		startTime:  time.Now(),
	}
}

// SetVerbose enables verbose output logging
func (hr *HeadlessRunner) SetVerbose(verbose bool) {
	hr.verbose = verbose
}

// Run executes all commands in the script sequentially
// This is a simplified simulation that doesn't actually interact with a terminal
// In a real implementation, this would drive the tuios application
func (hr *HeadlessRunner) Run(ctx context.Context) error {
	if hr.verbose {
		hr.logf("Starting headless script execution with %d commands\n", len(hr.commands))
	}

	for i, cmd := range hr.commands {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if hr.verbose {
			hr.logf("[%d/%d] Executing: %s\n", i+1, len(hr.commands), cmd.String())
		}

		// Simulate command execution with delay
		if cmd.Delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cmd.Delay):
				// Continue
			}
		}

		// Log command execution
		switch cmd.Type {
		case CommandType_Type:
			if len(cmd.Args) > 0 && hr.verbose {
				hr.logf("  → Typing: %q\n", cmd.Args[0])
			}
		case CommandType_Sleep:
			if hr.verbose && cmd.Delay > 0 {
				hr.logf("  → Sleeping for %v\n", cmd.Delay)
			}
		case CommandType_Enter:
			if hr.verbose {
				hr.logf("  → Pressed Enter\n")
			}
		case CommandType_KeyCombo:
			if len(cmd.Args) > 0 && hr.verbose {
				hr.logf("  → Key combination: %s\n", cmd.Args[0])
			}
		case CommandType_SwitchWS:
			if len(cmd.Args) > 0 && hr.verbose {
				hr.logf("  → Switching to workspace %s\n", cmd.Args[0])
			}
		case CommandType_NewWindow:
			if hr.verbose {
				hr.logf("  → Creating new window\n")
			}
		default:
			if hr.verbose {
				hr.logf("  → Command: %s\n", cmd.Type)
			}
		}
	}

	if hr.verbose {
		elapsed := time.Since(hr.startTime)
		hr.logf("Script execution completed in %v\n", elapsed)
	}

	return nil
}

// GetOutput returns the captured output
func (hr *HeadlessRunner) GetOutput() string {
	hr.outputLock.Lock()
	defer hr.outputLock.Unlock()
	return hr.output.String()
}

// WriteOutput writes the output to a writer
func (hr *HeadlessRunner) WriteOutput(w io.Writer) error {
	hr.outputLock.Lock()
	defer hr.outputLock.Unlock()
	_, err := io.WriteString(w, hr.output.String())
	return err
}

// logf logs a message to the internal output buffer
func (hr *HeadlessRunner) logf(format string, args ...interface{}) {
	hr.outputLock.Lock()
	defer hr.outputLock.Unlock()
	fmt.Fprintf(&hr.output, format, args...)
}

// ScriptExecutionStats contains statistics about a script execution
type ScriptExecutionStats struct {
	TotalCommands  int
	ExecutedCount  int
	ExecutedTime   time.Duration
	AvgTimePerCmd  time.Duration
	StartTime      time.Time
	EndTime        time.Time
	Success        bool
	ErrorMessage   string
}

// ScriptExecutor is a more advanced script executor that tracks statistics
type ScriptExecutor struct {
	runner *HeadlessRunner
	stats  ScriptExecutionStats
}

// NewScriptExecutor creates a new script executor
func NewScriptExecutor(commands []Command, outputPath string) *ScriptExecutor {
	return &ScriptExecutor{
		runner: NewHeadlessRunner(commands, outputPath),
		stats: ScriptExecutionStats{
			TotalCommands: len(commands),
			StartTime:     time.Now(),
		},
	}
}

// Execute runs the script and collects statistics
func (se *ScriptExecutor) Execute(ctx context.Context) ScriptExecutionStats {
	se.runner.SetVerbose(true)
	se.stats.StartTime = time.Now()

	err := se.runner.Run(ctx)
	se.stats.EndTime = time.Now()
	se.stats.ExecutedTime = se.stats.EndTime.Sub(se.stats.StartTime)
	se.stats.ExecutedCount = se.stats.TotalCommands

	if err != nil {
		se.stats.Success = false
		se.stats.ErrorMessage = err.Error()
	} else {
		se.stats.Success = true
	}

	if se.stats.ExecutedCount > 0 {
		se.stats.AvgTimePerCmd = se.stats.ExecutedTime / time.Duration(se.stats.ExecutedCount)
	}

	return se.stats
}

// GetStats returns execution statistics
func (se *ScriptExecutor) GetStats() ScriptExecutionStats {
	return se.stats
}

// GetOutput returns the execution output
func (se *ScriptExecutor) GetOutput() string {
	return se.runner.GetOutput()
}

// WriteOutput writes the execution output to a writer
func (se *ScriptExecutor) WriteOutput(w io.Writer) error {
	return se.runner.WriteOutput(w)
}

// ValidateScript checks if a tape script is valid (parses without errors)
func ValidateScript(content string) (bool, []string) {
	commands, errors := ParseFile(content)
	if len(errors) > 0 {
		return false, errors
	}
	if len(commands) == 0 {
		return false, []string{"no commands found in script"}
	}
	return true, nil
}
