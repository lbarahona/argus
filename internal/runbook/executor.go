package runbook

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// defaultStepTimeout bounds command execution when a step has no timeout.
const defaultStepTimeout = 60 * time.Second

// CommandRunner executes a shell command with a timeout and returns its
// combined output. Injectable so tests never shell out.
type CommandRunner func(ctx context.Context, command string, timeout time.Duration) (string, error)

// ShellRunner runs a command via `sh -c` with a timeout.
func ShellRunner(ctx context.Context, command string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if runCtx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("timed out after %s", timeout)
	}
	return string(out), err
}

// Executor walks a runbook's steps. Commands only run when Execute is true
// AND the operator confirms each step; otherwise steps are shown and skipped.
type Executor struct {
	Out     io.Writer
	In      io.Reader
	Execute bool
	Runner  CommandRunner // nil = ShellRunner
}

func (e *Executor) runner() CommandRunner {
	if e.Runner != nil {
		return e.Runner
	}
	return ShellRunner
}

func stepTimeout(s Step) time.Duration {
	if s.Timeout != "" {
		if d, err := time.ParseDuration(s.Timeout); err == nil && d > 0 {
			return d
		}
	}
	return defaultStepTimeout
}

// Run executes (or walks) all steps and returns the run log.
func (e *Executor) Run(ctx context.Context, rb *Runbook) *RunLog {
	log := &RunLog{
		RunbookID:   rb.ID,
		RunbookName: rb.Name,
		StartedAt:   time.Now(),
		Status:      "running",
	}
	in := bufio.NewScanner(e.In)
	failed := false

	for i, step := range rb.Steps {
		result := StepResult{StepName: step.Name, StartedAt: time.Now()}
		prefix := fmt.Sprintf("[%d/%d]", i+1, len(rb.Steps))
		icon := "⚡"
		if step.Manual {
			icon = "🖐️"
		}
		fmt.Fprintf(e.Out, "  %s %s %s\n", prefix, icon, step.Name)
		if step.Command != "" {
			fmt.Fprintf(e.Out, "       $ %s\n", step.Command)
		}
		if step.Notes != "" {
			fmt.Fprintf(e.Out, "       💡 %s\n", step.Notes)
		}

		switch {
		case !e.Execute:
			result.Status = "skipped"
			fmt.Fprintln(e.Out, "       (dry-run: skipped)")

		case step.Manual:
			result.Status = e.confirm(in, "Done? (y/n/skip): ")
			if result.Status == "failed" {
				result.Error = "step declined by operator"
			}

		case step.Command != "":
			answer := e.confirm(in, "Run? (y/n/skip): ")
			if answer != "passed" {
				result.Status = answer // skipped or failed (declined)
				if answer == "failed" {
					result.Error = "step declined by operator"
				}
				break
			}
			start := time.Now()
			out, err := e.runner()(ctx, step.Command, stepTimeout(step))
			result.Output = strings.TrimSpace(out)
			result.Duration = time.Since(start).Round(time.Millisecond).String()
			if err != nil {
				result.Status = "failed"
				result.Error = err.Error()
				break
			}
			if step.Check != "" {
				checkOut, checkErr := e.runner()(ctx, step.Check, stepTimeout(step))
				if checkErr != nil {
					result.Status = "failed"
					result.Error = fmt.Sprintf("check failed: %v: %s", checkErr, strings.TrimSpace(checkOut))
					break
				}
			}
			result.Status = "passed"

		default:
			// No command, not manual (check-only steps still require
			// per-step confirmation before their check runs).
			if step.Check != "" {
				answer := e.confirm(in, "Check? (y/n/skip): ")
				if answer != "passed" {
					result.Status = answer
					if answer == "failed" {
						result.Error = "step declined by operator"
					}
					break
				}
				start := time.Now()
				out, err := e.runner()(ctx, step.Check, stepTimeout(step))
				result.Output = strings.TrimSpace(out)
				result.Duration = time.Since(start).Round(time.Millisecond).String()
				if err != nil {
					result.Status = "failed"
					result.Error = err.Error()
				} else {
					result.Status = "passed"
				}
			} else {
				result.Status = "skipped"
			}
		}

		log.StepResults = append(log.StepResults, result)

		if result.Status == "failed" {
			failed = true
			switch rb.OnFailure {
			case "rollback":
				if step.Rollback != "" {
					rollbackResult := StepResult{StepName: step.Name + " (rollback)", StartedAt: time.Now()}
					fmt.Fprintf(e.Out, "       $ %s\n", step.Rollback)
					answer := e.confirm(in, "Rollback? (y/n/skip): ")
					if answer != "passed" {
						rollbackResult.Status = answer
						if answer == "failed" {
							rollbackResult.Error = "step declined by operator"
						}
					} else {
						fmt.Fprintf(e.Out, "  ↩️  on_failure=rollback — running rollback for %q\n", step.Name)
						start := time.Now()
						out, err := e.runner()(ctx, step.Rollback, stepTimeout(step))
						rollbackResult.Output = strings.TrimSpace(out)
						rollbackResult.Duration = time.Since(start).Round(time.Millisecond).String()
						if err != nil {
							rollbackResult.Status = "failed"
							rollbackResult.Error = err.Error()
							fmt.Fprintf(e.Out, "       rollback failed: %v: %s\n", err, rollbackResult.Output)
						} else {
							rollbackResult.Status = "passed"
						}
					}
					log.StepResults = append(log.StepResults, rollbackResult)
				}
				fmt.Fprintln(e.Out, "  ⚠️  stopping after rollback")
			case "continue":
				fmt.Fprintln(e.Out, "  ⚠️  step failed — on_failure=continue, moving on")
				continue
			default: // escalate or unset
				fmt.Fprintln(e.Out, "  ⚠️  step failed — stopping execution")
			}
			break
		}
		fmt.Fprintln(e.Out)
	}

	log.CompletedAt = time.Now()
	if failed {
		log.Status = "failed"
	} else {
		log.Status = "completed"
	}
	return log
}

// confirm prompts and maps the answer to a step status.
func (e *Executor) confirm(in *bufio.Scanner, prompt string) string {
	fmt.Fprintf(e.Out, "       %s", prompt)
	if !in.Scan() {
		return "skipped" // EOF: never execute without an explicit yes
	}
	switch strings.ToLower(strings.TrimSpace(in.Text())) {
	case "y", "yes":
		return "passed"
	case "skip", "s":
		return "skipped"
	default:
		return "failed"
	}
}
