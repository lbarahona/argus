package runbook

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeRunner records commands and returns scripted results.
type fakeRunner struct {
	commands []string
	fail     map[string]bool // command -> should fail
}

func (f *fakeRunner) run(ctx context.Context, command string, timeout time.Duration) (string, error) {
	f.commands = append(f.commands, command)
	if f.fail[command] {
		return "boom", fmt.Errorf("exit status 1")
	}
	return "ok-output", nil
}

func testRunbook() *Runbook {
	return &Runbook{
		ID:   "test-rb",
		Name: "Test Runbook",
		Steps: []Step{
			{Name: "restart", Command: "kubectl rollout restart deploy/api"},
			{Name: "verify", Command: "curl -sf http://api/health", Check: "curl -sf http://api/ready"},
		},
	}
}

func TestExecutorDryRunExecutesNothing(t *testing.T) {
	f := &fakeRunner{}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader(""), Execute: false, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if len(f.commands) != 0 {
		t.Fatalf("dry-run must execute nothing, ran %v", f.commands)
	}
	for _, r := range log.StepResults {
		if r.Status != "skipped" {
			t.Errorf("dry-run step %q status = %q, want skipped", r.StepName, r.Status)
		}
	}
	if log.Status != "completed" {
		t.Errorf("dry-run log status = %q, want completed", log.Status)
	}
}

func TestExecutorRunsConfirmedCommands(t *testing.T) {
	f := &fakeRunner{}
	var out strings.Builder
	// Confirm both steps.
	e := &Executor{Out: &out, In: strings.NewReader("y\ny\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if len(f.commands) != 3 { // step1 command, step2 command, step2 check
		t.Fatalf("expected 3 executed commands (2 commands + 1 check), got %v", f.commands)
	}
	if log.StepResults[0].Status != "passed" || log.StepResults[1].Status != "passed" {
		t.Errorf("confirmed successful steps should pass: %+v", log.StepResults)
	}
	if log.StepResults[0].Output != "ok-output" {
		t.Errorf("step output not captured: %+v", log.StepResults[0])
	}
	if log.StepResults[0].Duration == "" {
		t.Errorf("step duration not recorded")
	}
	if log.Status != "completed" {
		t.Errorf("log status = %q, want completed", log.Status)
	}
}

func TestExecutorDeclinedStepFailsWithoutExecuting(t *testing.T) {
	f := &fakeRunner{}
	var out strings.Builder
	// "n" = the operator says the state is wrong: the step fails (and the
	// default on_failure stops the run). "skip" is the way to pass over a step.
	e := &Executor{Out: &out, In: strings.NewReader("n\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if log.StepResults[0].Status != "failed" {
		t.Errorf("declined step status = %q, want failed", log.StepResults[0].Status)
	}
	if len(f.commands) != 0 {
		t.Errorf("declined command must not execute, ran %v", f.commands)
	}
	if log.Status != "failed" {
		t.Errorf("log status = %q, want failed", log.Status)
	}
}

func TestExecutorSkippedStepContinues(t *testing.T) {
	f := &fakeRunner{}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("skip\ny\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if log.StepResults[0].Status != "skipped" {
		t.Errorf("skipped step status = %q, want skipped", log.StepResults[0].Status)
	}
	if len(log.StepResults) != 2 {
		t.Errorf("skip must continue to the next step, got %d results", len(log.StepResults))
	}
	if log.StepResults[1].Status != "passed" {
		t.Errorf("second step should run and pass, got %q", log.StepResults[1].Status)
	}
}

func TestExecutorFailedCommandMarksStepFailed(t *testing.T) {
	f := &fakeRunner{fail: map[string]bool{"kubectl rollout restart deploy/api": true}}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("y\ny\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if log.StepResults[0].Status != "failed" {
		t.Errorf("failed command step status = %q, want failed", log.StepResults[0].Status)
	}
	if log.StepResults[0].Error == "" {
		t.Errorf("failure must record the error")
	}
	if log.Status != "failed" {
		t.Errorf("log status = %q, want failed", log.Status)
	}
}

func TestExecutorFailedCheckFailsStep(t *testing.T) {
	f := &fakeRunner{fail: map[string]bool{"curl -sf http://api/ready": true}}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("y\ny\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if log.StepResults[1].Status != "failed" {
		t.Errorf("step with failing check = %q, want failed", log.StepResults[1].Status)
	}
}

func TestExecutorOnFailureEscalateStops(t *testing.T) {
	rb := testRunbook()
	rb.OnFailure = "escalate"
	f := &fakeRunner{fail: map[string]bool{"kubectl rollout restart deploy/api": true}}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("y\ny\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), rb)

	if len(log.StepResults) != 1 {
		t.Errorf("escalate must stop after the failed step, got %d results", len(log.StepResults))
	}
	if log.Status != "failed" {
		t.Errorf("log status = %q, want failed", log.Status)
	}
}

func TestExecutorOnFailureRollbackRunsRollbackCommand(t *testing.T) {
	rb := testRunbook()
	rb.OnFailure = "rollback"
	rb.Steps[0].Rollback = "kubectl rollout undo deploy/api"
	f := &fakeRunner{fail: map[string]bool{"kubectl rollout restart deploy/api": true}}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("y\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), rb)

	ranRollback := false
	for _, c := range f.commands {
		if c == "kubectl rollout undo deploy/api" {
			ranRollback = true
		}
	}
	if !ranRollback {
		t.Errorf("on_failure=rollback must run the failed step's rollback command, ran %v", f.commands)
	}
	if log.Status != "failed" {
		t.Errorf("log status = %q, want failed", log.Status)
	}
}

func TestExecutorManualStepPrompts(t *testing.T) {
	rb := &Runbook{ID: "m", Name: "Manual", Steps: []Step{{Name: "check dashboards", Manual: true}}}
	f := &fakeRunner{}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("y\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), rb)

	if log.StepResults[0].Status != "passed" {
		t.Errorf("confirmed manual step = %q, want passed", log.StepResults[0].Status)
	}
	if len(f.commands) != 0 {
		t.Errorf("manual steps must not execute commands")
	}
}

func TestShellRunnerRealCommand(t *testing.T) {
	out, err := ShellRunner(context.Background(), "echo hello", 5*time.Second)
	if err != nil {
		t.Fatalf("echo failed: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("output = %q, want hello", out)
	}
}

func TestShellRunnerTimeout(t *testing.T) {
	_, err := ShellRunner(context.Background(), "sleep 5", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got %v", err)
	}
}

func TestSaveRunLogWritesAtomically(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	store := NewStore()

	log := &RunLog{RunbookID: "rb-1", RunbookName: "RB", StartedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC), Status: "completed"}
	path, err := store.SaveRunLog(log)
	if err != nil {
		t.Fatalf("SaveRunLog: %v", err)
	}
	if !strings.Contains(path, "rb-1-20260701-120000.yaml") {
		t.Errorf("unexpected run log path %q", path)
	}
	loaded, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(loaded), "rb-1") {
		t.Errorf("run log not readable: %v", err)
	}
}
