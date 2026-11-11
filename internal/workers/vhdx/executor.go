package vhdx

import (
	"fmt"
	"os/exec"
	"sync"
)

// CommandExecutor defines the interface for executing or simulating plans.
type CommandExecutor interface {
	Simulate(plan *ExecutionPlan) error
	Execute(plan *ExecutionPlan) error
}

// DryRunExecutor records execution steps without performing real disk or BCD operations.
type DryRunExecutor struct {
	mu            sync.Mutex
	ExecutedPlans []*ExecutionPlan
	Logs          []string
}

// NewDryRunExecutor creates a new DryRunExecutor.
func NewDryRunExecutor() *DryRunExecutor {
	return &DryRunExecutor{
		ExecutedPlans: make([]*ExecutionPlan, 0),
		Logs:          make([]string, 0),
	}
}

func (e *DryRunExecutor) Simulate(plan *ExecutionPlan) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ExecutedPlans = append(e.ExecutedPlans, plan)
	e.Logs = append(e.Logs, fmt.Sprintf("[DRYRUN] Planned %s on %s with %d steps", plan.Action, plan.TargetPointID, len(plan.Steps)))
	for _, s := range plan.Steps {
		e.Logs = append(e.Logs, fmt.Sprintf("[DRYRUN] Step %d (%s): %s", s.Order, s.Category, s.Command))
	}
	return nil
}

func (e *DryRunExecutor) Execute(plan *ExecutionPlan) error {
	return e.Simulate(plan)
}

// MockExecutor allows injecting errors for testing rollback and failure recovery.
type MockExecutor struct {
	mu           sync.Mutex
	FailStep     int   // If > 0, fails at the specified step order.
	FailError    error // Error to return at FailStep.
	RollbackLogs []string
	ExecutedLogs []string
}

// NewMockExecutor creates a new MockExecutor.
func NewMockExecutor() *MockExecutor {
	return &MockExecutor{
		RollbackLogs: make([]string, 0),
		ExecutedLogs: make([]string, 0),
	}
}

func (m *MockExecutor) Simulate(plan *ExecutionPlan) error {
	return m.Execute(plan)
}

func (m *MockExecutor) Execute(plan *ExecutionPlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, step := range plan.Steps {
		if m.FailStep > 0 && step.Order == m.FailStep {
			// Trigger rollback simulation if BCD backup is present
			if plan.BackupBCDPath != "" && step.Category == "BCD" {
				m.RollbackLogs = append(m.RollbackLogs, fmt.Sprintf("bcdedit /import %s", plan.BackupBCDPath))
			}
			if m.FailError != nil {
				return m.FailError
			}
			return fmt.Errorf("injected failure at step %d: %s", step.Order, step.Command)
		}
		m.ExecutedLogs = append(m.ExecutedLogs, step.Command)
	}
	return nil
}

// RealExecutor executes actual Windows commands with automatic BCD rollback on failure.
type RealExecutor struct{}

func (r *RealExecutor) Simulate(plan *ExecutionPlan) error {
	return nil
}

func (r *RealExecutor) Execute(plan *ExecutionPlan) error {
	for _, step := range plan.Steps {
		cmd := exec.Command("cmd", "/c", step.Command)
		output, err := cmd.CombinedOutput()
		if err != nil {
			// If failure occurred in BCD step and we have a backup, roll back BCD!
			if plan.BackupBCDPath != "" && (step.Category == "BCD" || step.Category == "DISKPART") {
				rollbackCmd := exec.Command("cmd", "/c", fmt.Sprintf("bcdedit /import \"%s\"", plan.BackupBCDPath))
				_ = rollbackCmd.Run()
			}
			return fmt.Errorf("step %d [%s] failed (%v): %s", step.Order, step.Command, err, string(output))
		}
	}
	return nil
}

