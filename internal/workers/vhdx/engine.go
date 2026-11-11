package vhdx

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/owner/back-ez/internal/protocol"
)

// Standard max depth defined in SPEC §2.2.
const MaxChainDepth = 5

// Config defines the configuration for the VHDX differential restore point engine.
type Config struct {
	DryRun           bool   // When true, generate executable scripts and logs without touching real disk/BCD.
	AllowDestructive bool   // When DryRun is false, must be explicitly true with risk acknowledged.
	VolumeGUID       string // Volume GUID of the reserve partition (e.g. `\\?\Volume{12345678-1234-1234-1234-123456789abc}\`).
	ReserveRoot      string // Relative path or directory under the volume (e.g. "EZRestore").
	BCDEntryID       string // Target BCD boot entry ID, default "{default}".
	DataDir          string // Directory where metadata (chain.json) is persisted.
}

// Engine manages the differential VHDX chain, BCD boot indicators, and DryRun scripts.
type Engine struct {
	mu       sync.Mutex
	cfg      Config
	chain    *Chain
	executor CommandExecutor
}

// VHDXError wraps error with protocol-compliant error code.
type VHDXError struct {
	Code    string
	Message string
}

func (e *VHDXError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// NewEngine creates a new VHDX restore point engine instance.
func NewEngine(cfg Config, exec CommandExecutor) (*Engine, error) {
	if cfg.BCDEntryID == "" {
		cfg.BCDEntryID = "{default}"
	}
	if cfg.ReserveRoot == "" {
		cfg.ReserveRoot = "EZRestore"
	}
	if exec == nil {
		exec = NewDryRunExecutor()
	}

	chain := NewChain()
	if cfg.DataDir != "" {
		metaFile := filepath.Join(cfg.DataDir, "chain.json")
		if err := chain.LoadFromFile(metaFile); err != nil && !errors.Is(err, ErrMetadataNotFound) {
			return nil, fmt.Errorf("failed to load chain metadata: %w", err)
		}
	}

	return &Engine{
		cfg:      cfg,
		chain:    chain,
		executor: exec,
	}, nil
}

// InitBase initializes the base restore point (depth = 0) if the chain is empty.
func (e *Engine) InitBase(name, description, vhdxFile string, sizeBytes int64) (*protocol.RestorePoint, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.chain.Points) > 0 {
		return nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: "chain already initialized with a base node",
		}
	}

	base := &protocol.RestorePoint{
		PointID:     "rp_base",
		Name:        name,
		Description: description,
		VHDXFile:    vhdxFile,
		ParentID:    nil,
		Depth:       0,
		SizeBytes:   sizeBytes,
		IsBase:      true,
		IsActive:    true,
		State:       protocol.RestorePointStateReady,
		CreatedBy:   protocol.CreatedBySystem,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := e.chain.AddPoint(base); err != nil {
		return nil, err
	}
	e.persistMetadataLocked()
	return base, nil
}

// ResetAfterDeploy cleans up all existing differential restore points and initializes a fresh base point (Decision D8).
func (e *Engine) ResetAfterDeploy(baseName, baseDescription, baseVHDXFile string, sizeBytes int64) (*protocol.RestorePoint, *ExecutionPlan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. Gather all non-base points to delete diff files
	var delSteps []ExecutionStep
	stepOrder := 1
	for _, p := range e.chain.Points {
		if !p.IsBase {
			diffPath, _ := e.resolvePaths(p.VHDXFile)
			delSteps = append(delSteps, ExecutionStep{
				Order:       stepOrder,
				Category:    "FILE",
				Command:     fmt.Sprintf("del /f /q \"%s\"", diffPath),
				Description: fmt.Sprintf("Clean up old differential disk %s (Decision D8)", diffPath),
			})
			stepOrder++
		}
	}

	// 2. Point BCD boot back to base.vhdx
	basePointID := fmt.Sprintf("rp_base_%s", time.Now().UTC().Format("20060102"))
	if baseName == "" {
		baseName = fmt.Sprintf("派送後基準點 %s", time.Now().UTC().Format("2006-01-02"))
	}
	if baseDescription == "" {
		baseDescription = "母機派送完成後系統自動建立的基準點"
	}
	if baseVHDXFile == "" {
		baseVHDXFile = "base.vhdx"
	}

	_, baseBCD := e.resolvePaths(baseVHDXFile)
	delSteps = append(delSteps,
		ExecutionStep{
			Order:       stepOrder,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /set %s device %s", e.cfg.BCDEntryID, baseBCD),
			Description: fmt.Sprintf("Reset BCD entry %s device to new base %s", e.cfg.BCDEntryID, baseBCD),
		},
		ExecutionStep{
			Order:       stepOrder + 1,
			Category:    "BCD",
			Command:     fmt.Sprintf("bcdedit /set %s osdevice %s", e.cfg.BCDEntryID, baseBCD),
			Description: fmt.Sprintf("Reset BCD entry %s osdevice to new base %s", e.cfg.BCDEntryID, baseBCD),
		},
	)

	plan := &ExecutionPlan{
		Action:        "RESET_AFTER_DEPLOY",
		TargetPointID: basePointID,
		Steps:         delSteps,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := e.executePlan(plan); err != nil {
		return nil, plan, fmt.Errorf("failed to execute reset plan: %w", err)
	}

	// 3. Clear chain and add single new base
	e.chain.Points = make(map[string]*protocol.RestorePoint)
	newBase := &protocol.RestorePoint{
		PointID:     basePointID,
		Name:        baseName,
		Description: baseDescription,
		VHDXFile:    baseVHDXFile,
		ParentID:    nil,
		Depth:       0,
		SizeBytes:   sizeBytes,
		IsBase:      true,
		IsActive:    true,
		State:       protocol.RestorePointStateReady,
		CreatedBy:   protocol.CreatedBySystem,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := e.chain.AddPoint(newBase); err != nil {
		return nil, plan, err
	}
	e.chain.SetActive(newBase.PointID)
	e.persistMetadataLocked()

	return newBase, plan, nil
}

// CreateRequest represents parameters for creating a new differential restore point.
type CreateRequest struct {
	PointID     string             `json:"point_id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	ParentID    *string            `json:"parent_id,omitempty"` // If nil, defaults to current active point.
	CreatedBy   protocol.CreatedBy `json:"created_by"`
}

// Create creates a new differential child restore point and updates the BCD boot entry.
func (e *Engine) Create(req CreateRequest) (*protocol.RestorePoint, *ExecutionPlan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. Determine parent
	var parent *protocol.RestorePoint
	if req.ParentID != nil && *req.ParentID != "" {
		parent = e.chain.GetPoint(*req.ParentID)
		if parent == nil {
			return nil, nil, &VHDXError{
				Code:    protocol.ErrNotFound,
				Message: fmt.Sprintf("parent restore point %q not found", *req.ParentID),
			}
		}
	} else {
		parent = e.chain.GetActivePoint()
		if parent == nil {
			return nil, nil, &VHDXError{
				Code:    protocol.ErrInvalidState,
				Message: "no active restore point found to derive child from",
			}
		}
	}

	if parent.State != protocol.RestorePointStateReady {
		return nil, nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: fmt.Sprintf("parent restore point state is %s, expected READY", parent.State),
		}
	}

	// 2. Depth check (SPEC §2.2: depth <= 5, otherwise RESTORE_POINT_LIMIT)
	newDepth := parent.Depth + 1
	if newDepth > MaxChainDepth {
		return nil, nil, &VHDXError{
			Code:    protocol.ErrRestorePointLimit,
			Message: fmt.Sprintf("exceeds maximum restore point depth limit of %d (current: %d)", MaxChainDepth, parent.Depth),
		}
	}

	// 3. ID and file generation
	pointID := req.PointID
	if pointID == "" {
		pointID = fmt.Sprintf("rp_%s", time.Now().UTC().Format("20060102_150405"))
	}
	if e.chain.GetPoint(pointID) != nil {
		return nil, nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: fmt.Sprintf("restore point ID %q already exists", pointID),
		}
	}

	vhdxFile := fmt.Sprintf("diff_%s.vhdx", strings.TrimPrefix(pointID, "rp_"))

	createdBy := req.CreatedBy
	if createdBy == "" {
		createdBy = protocol.CreatedByAdminLocal
	}

	newPoint := &protocol.RestorePoint{
		PointID:     pointID,
		Name:        req.Name,
		Description: req.Description,
		VHDXFile:    vhdxFile,
		ParentID:    &parent.PointID,
		Depth:       newDepth,
		SizeBytes:   0,
		IsBase:      false,
		IsActive:    true,
		State:       protocol.RestorePointStateReady,
		CreatedBy:   createdBy,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	// 4. Generate Execution Plan (diskpart to create diff disk, BCD backup and update)
	plan := e.generateCreatePlan(parent, newPoint)

	// 5. Execute plan (or dry run)
	if err := e.executePlan(plan); err != nil {
		return nil, plan, fmt.Errorf("failed to execute create plan: %w", err)
	}

	// 6. Update internal chain state
	if err := e.chain.AddPoint(newPoint); err != nil {
		return nil, nil, err
	}
	e.chain.SetActive(newPoint.PointID)
	e.persistMetadataLocked()

	return newPoint, plan, nil
}

// Switch switches the active boot indicator to the target restore point.
func (e *Engine) Switch(pointID string) (*ExecutionPlan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	target := e.chain.GetPoint(pointID)
	if target == nil {
		return nil, &VHDXError{
			Code:    protocol.ErrNotFound,
			Message: fmt.Sprintf("restore point %q not found", pointID),
		}
	}

	if target.State != protocol.RestorePointStateReady {
		return nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: fmt.Sprintf("cannot switch to restore point in %s state", target.State),
		}
	}

	// Generate switch plan
	plan := e.generateSwitchPlan(target)

	if err := e.executePlan(plan); err != nil {
		return plan, fmt.Errorf("failed to execute switch plan: %w", err)
	}

	e.chain.SetActive(pointID)
	e.persistMetadataLocked()
	return plan, nil
}

// Delete removes a non-active, non-base restore point without children.
func (e *Engine) Delete(pointID string) (*ExecutionPlan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	target := e.chain.GetPoint(pointID)
	if target == nil {
		return nil, &VHDXError{
			Code:    protocol.ErrNotFound,
			Message: fmt.Sprintf("restore point %q not found", pointID),
		}
	}

	// Rule 1: Cannot delete active point (SPEC §2.2)
	if target.IsActive {
		return nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: "cannot delete active restore point",
		}
	}

	// Rule 2: Cannot delete base point
	if target.IsBase {
		return nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: "cannot delete base restore point",
		}
	}

	// Rule 3: Cannot delete point that has children
	children := e.chain.GetChildren(pointID)
	if len(children) > 0 {
		return nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: fmt.Sprintf("cannot delete restore point with %d child node(s)", len(children)),
		}
	}

	plan := e.generateDeletePlan(target)
	if err := e.executePlan(plan); err != nil {
		return plan, fmt.Errorf("failed to execute delete plan: %w", err)
	}

	if err := e.chain.RemovePoint(pointID); err != nil {
		return plan, err
	}
	e.persistMetadataLocked()
	return plan, nil
}

// List returns a cloned slice of all restore points.
func (e *Engine) List() []protocol.RestorePoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.chain.List()
}

// Get returns a single restore point by ID.
func (e *Engine) Get(pointID string) *protocol.RestorePoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.chain.GetPoint(pointID)
}

// GetActive returns the currently active restore point.
func (e *Engine) GetActive() *protocol.RestorePoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.chain.GetActivePoint()
}

func (e *Engine) persistMetadataLocked() {
	if e.cfg.DataDir != "" {
		metaFile := filepath.Join(e.cfg.DataDir, "chain.json")
		_ = e.chain.SaveToFile(metaFile)
	}
}

func (e *Engine) executePlan(plan *ExecutionPlan) error {
	if e.cfg.DryRun {
		return e.executor.Simulate(plan)
	}
	if !e.cfg.AllowDestructive {
		return &VHDXError{
			Code:    protocol.ErrForbidden,
			Message: "destructive operations are disabled; AllowDestructive must be true",
		}
	}
	return e.executor.Execute(plan)
}

