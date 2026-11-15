package vhdx

import (
	"errors"
	"fmt"
	"os"
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
	DryRun           bool // When true, generate executable scripts and logs without touching real disk/BCD.
	AllowDestructive bool // When DryRun is false, must be explicitly true with risk acknowledged.
	// RealExecutionEnabled 必須由 B 案（真正實作 VHDX 原生開機與 BCD 專用開機項）明確設為 true。
	// A 案一律為 false：任何真實 diskpart/BCD 操作都會被拒絕，避免用半成品流程動到磁碟。
	RealExecutionEnabled bool
	VolumeGUID           string // Volume GUID of the reserve partition (e.g. `\\?\Volume{12345678-1234-1234-1234-123456789abc}\`).
	ReserveRoot          string // 保留區根目錄；cmd/gui 傳絕對實體路徑，測試可傳相對於磁碟區的路徑。
	ReserveRelPath       string // 保留區相對於磁碟區根目錄的子路徑（例如 "EZRestore"）；空值時由 ReserveRoot 推導。
	BCDEntryID           string // Target BCD boot entry ID, default "{default}".
	DataDir              string // Directory where metadata (chain.json) is persisted; 同時作為實體檔案根目錄。
}

// Engine manages the differential VHDX chain, BCD boot indicators, and DryRun scripts.
type Engine struct {
	mu          sync.Mutex
	cfg         Config
	chain       *Chain
	executor    CommandExecutor
	loadWarning string

	// Real-execution backends. They are only used when DryRun=false and
	// RealExecutionEnabled=true; tests inject fakes here.
	disk DiskBackend
	bcd  BCDBackend
	live *LiveLayer
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
//
// 舊版 AgentCore 曾把 chain.json 寫成 JSON 陣列；這裡會偵測、備份為
// chain.json.legacy-<UTC> 並以空鏈啟動，而不是讓整個功能永遠載入失敗。
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
	loadWarning := ""
	if cfg.DataDir != "" {
		metaFile := filepath.Join(cfg.DataDir, "chain.json")
		err := chain.LoadFromFile(metaFile)
		switch {
		case err == nil:
		case errors.Is(err, ErrMetadataNotFound):
			// 首次啟動，尚未有鏈資料。
		case errors.Is(err, ErrLegacyArrayFormat):
			backup := metaFile + ".legacy-" + time.Now().UTC().Format("20060102T150405Z")
			if renameErr := os.Rename(metaFile, backup); renameErr != nil {
				return nil, fmt.Errorf("failed to archive legacy chain metadata: %w", renameErr)
			}
			loadWarning = fmt.Sprintf("偵測到舊版 chain.json（JSON 陣列格式），已備份為 %s 並以空鏈啟動", filepath.Base(backup))
		default:
			return nil, fmt.Errorf("failed to load chain metadata: %w", err)
		}
	}

	eng := &Engine{
		cfg:         cfg,
		chain:       chain,
		executor:    exec,
		loadWarning: loadWarning,
		disk:        NewDiskBackend(),
		bcd:         NewBCDBackend(),
	}

	if cfg.DataDir != "" {
		live, liveErr := loadLiveLayer(filepath.Join(cfg.DataDir, "live.json"))
		switch {
		case liveErr == nil:
			eng.live = live
		case errors.Is(liveErr, os.ErrNotExist):
			// 尚未完成 VHDX 原生開機建置。
		default:
			return nil, fmt.Errorf("failed to load live layer metadata: %w", liveErr)
		}
	}

	if len(chain.Points) > 0 {
		if verr := chain.Validate(); verr != nil {
			chain.MarkAllBroken()
			// 載入階段盡力而為：持久化失敗只影響 BROKEN 狀態是否落盤。
			_ = eng.persistMetadataLocked()
			if loadWarning != "" {
				loadWarning += "；"
			}
			loadWarning += fmt.Sprintf("還原點鏈完整性檢查失敗，已全部標記為 BROKEN：%v", verr)
		}
	}

	if !cfg.DryRun && cfg.RealExecutionEnabled {
		eng.reconcileLiveLocked()
	}

	return eng, nil
}

// Live returns a copy of the live-layer metadata, or nil when not provisioned.
func (e *Engine) Live() *LiveLayer {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.live == nil {
		return nil
	}
	clone := *e.live
	return &clone
}

// LoadWarning 回傳載入期間發生的可復原問題（例如舊格式遷移、鏈損壞）。
func (e *Engine) LoadWarning() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loadWarning
}

// DryRun 回報引擎是否只做模擬（不修改磁碟／BCD）。
func (e *Engine) DryRun() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.DryRun
}

// AllowDestructive 回報引擎是否允許破壞性操作。
func (e *Engine) AllowDestructive() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.AllowDestructive
}

func (e *Engine) physicalRoot() string {
	if e.cfg.DataDir != "" {
		return e.cfg.DataDir
	}
	return e.cfg.ReserveRoot
}

func (e *Engine) physicalPath(vhdxFile string) string {
	if filepath.IsAbs(vhdxFile) {
		return vhdxFile
	}
	return filepath.Join(e.physicalRoot(), vhdxFile)
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
	e.chain.SetActive(base.PointID)
	if err := e.persistMetadataLocked(); err != nil {
		return base, err
	}
	return base, nil
}

// EnsureBase 只在保留區真的存在 base.vhdx 時建立基準節點。
// 若檔案不存在（尚未完成 VHDX 原生開機建置），回傳 CAPABILITY_UNSUPPORTED，
// 讓上層明確告訴使用者「缺什麼」，而不是捏造一個不存在的 base.vhdx。
func (e *Engine) EnsureBase(name, description string) (*protocol.RestorePoint, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	points := e.chain.List()
	if len(points) > 0 {
		for _, p := range points {
			if p.IsBase {
				cp := p
				return &cp, nil
			}
		}
		return nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: "還原點鏈已存在但找不到基準節點（metadata 損壞）",
		}
	}

	baseFile := "base.vhdx"
	info, err := os.Stat(e.physicalPath(baseFile))
	if err != nil || info.IsDir() {
		return nil, &VHDXError{
			Code: protocol.ErrCapabilityUnsupported,
			Message: fmt.Sprintf(
				"尚未建立基準磁碟 %s；請先完成「實體機 → VHDX 原生開機」一次性建置",
				e.physicalPath(baseFile)),
		}
	}

	if strings.TrimSpace(name) == "" {
		name = "Base System"
	}
	if strings.TrimSpace(description) == "" {
		description = "Original Clean Base Image"
	}

	base := &protocol.RestorePoint{
		PointID:     "rp_base",
		Name:        name,
		Description: description,
		VHDXFile:    baseFile,
		ParentID:    nil,
		Depth:       0,
		SizeBytes:   info.Size(),
		IsBase:      true,
		IsActive:    true,
		State:       protocol.RestorePointStateReady,
		CreatedBy:   protocol.CreatedBySystem,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := e.chain.AddPoint(base); err != nil {
		return nil, err
	}
	e.chain.SetActive(base.PointID)
	if err := e.persistMetadataLocked(); err != nil {
		return base, err
	}

	cp := *base
	return &cp, nil
}

// ResetAfterDeploy cleans up all existing differential restore points and initializes a fresh base point (Decision D8).
func (e *Engine) ResetAfterDeploy(baseName, baseDescription, baseVHDXFile string, sizeBytes int64) (*protocol.RestorePoint, *ExecutionPlan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.preflightRealLocked("RESET", ""); err != nil {
		return nil, nil, err
	}

	// 1. Gather all non-base points to delete diff files
	var delSteps []ExecutionStep
	stepOrder := 1
	for _, p := range e.chain.Points {
		if !p.IsBase {
			diffPath, _, pathErr := e.resolvePaths(p.VHDXFile)
			if pathErr != nil {
				return nil, nil, pathErr
			}
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

	_, baseBCD, pathErr := e.resolvePaths(baseVHDXFile)
	if pathErr != nil {
		return nil, nil, pathErr
	}
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
	if err := e.persistMetadataLocked(); err != nil {
		return newBase, plan, err
	}

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

	if !e.cfg.DryRun && e.cfg.RealExecutionEnabled {
		return e.createRealLocked(req)
	}

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

	if err := e.preflightRealLocked("CREATE", parent.VHDXFile); err != nil {
		return nil, nil, err
	}

	// 2. Depth check (SPEC §2.2: depth <= 5, otherwise RESTORE_POINT_LIMIT)
	newDepth := parent.Depth + 1
	if newDepth > MaxChainDepth {
		return nil, nil, &VHDXError{
			Code:    protocol.ErrRestorePointLimit,
			Message: fmt.Sprintf("exceeds maximum restore point depth limit of %d (current: %d)", MaxChainDepth, parent.Depth),
		}
	}

	// 3. ID and file generation (同秒建立時加上序號，避免撞 ID)
	pointID := req.PointID
	if pointID == "" {
		baseID := fmt.Sprintf("rp_%s", time.Now().UTC().Format("20060102_150405"))
		pointID = baseID
		for i := 2; e.chain.GetPoint(pointID) != nil; i++ {
			pointID = fmt.Sprintf("%s_%d", baseID, i)
		}
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
	plan, err := e.generateCreatePlan(parent, newPoint)
	if err != nil {
		return nil, nil, err
	}

	// 5. Execute plan (or dry run)
	if err := e.executePlan(plan); err != nil {
		return nil, plan, fmt.Errorf("failed to execute create plan: %w", err)
	}

	// 6. Update internal chain state
	if err := e.chain.AddPoint(newPoint); err != nil {
		return nil, nil, err
	}
	e.chain.SetActive(newPoint.PointID)
	if err := e.persistMetadataLocked(); err != nil {
		return newPoint, plan, err
	}

	return newPoint, plan, nil
}

// Switch switches the active boot indicator to the target restore point.
//
// 注意：直接的「切換」只能指到葉節點。若目標仍有子節點，開機進該層會讓所有子節點失效
// （VHDX 差分鏈的硬限制），因此一律拒絕；真正的「重建乾淨讀寫層」屬 B 案。
func (e *Engine) Switch(pointID string) (*ExecutionPlan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.cfg.DryRun && e.cfg.RealExecutionEnabled {
		return e.switchRealLocked(pointID)
	}

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

	if children := e.chain.GetChildren(pointID); len(children) > 0 {
		return nil, &VHDXError{
			Code: protocol.ErrInvalidState,
			Message: fmt.Sprintf(
				"還原點 %s 仍有 %d 個子節點；直接開機進上層會使其失效，需改用『基於目標點重建乾淨讀寫層』（B 案）",
				pointID, len(children)),
		}
	}

	if err := e.preflightRealLocked("SWITCH", target.VHDXFile); err != nil {
		return nil, err
	}

	plan, err := e.generateSwitchPlan(target)
	if err != nil {
		return nil, err
	}

	if err := e.executePlan(plan); err != nil {
		return plan, fmt.Errorf("failed to execute switch plan: %w", err)
	}

	e.chain.SetActive(pointID)
	if err := e.persistMetadataLocked(); err != nil {
		return plan, err
	}
	return plan, nil
}

// Delete removes a non-active, non-base restore point without children.
func (e *Engine) Delete(pointID string) (*ExecutionPlan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.cfg.DryRun && e.cfg.RealExecutionEnabled {
		return e.deleteRealLocked(pointID)
	}

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

	if err := e.preflightRealLocked("DELETE", target.VHDXFile); err != nil {
		return nil, err
	}

	plan, err := e.generateDeletePlan(target)
	if err != nil {
		return nil, err
	}
	if err := e.executePlan(plan); err != nil {
		return plan, fmt.Errorf("failed to execute delete plan: %w", err)
	}

	if err := e.chain.RemovePoint(pointID); err != nil {
		return plan, err
	}
	if err := e.persistMetadataLocked(); err != nil {
		return plan, err
	}
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

func (e *Engine) persistMetadataLocked() error {
	if e.cfg.DataDir == "" {
		return nil
	}
	metaFile := filepath.Join(e.cfg.DataDir, "chain.json")
	if err := e.chain.SaveToFile(metaFile); err != nil {
		return fmt.Errorf("鏈資料寫入失敗（重啟後可能遺失）：%w", err)
	}
	return nil
}

// preflightRealLocked 是「真實執行」的總開關。DryRun 時一律放行。
// A 案 RealExecutionEnabled 固定為 false，因此任何真實 diskpart/BCD 操作都會被拒絕，
// 不會出現「假成功」或半成品指令動到磁碟。
func (e *Engine) preflightRealLocked(action, vhdxFile string) error {
	if e.cfg.DryRun {
		return nil
	}
	if !e.cfg.AllowDestructive {
		return &VHDXError{
			Code:    protocol.ErrForbidden,
			Message: "destructive operations are disabled; AllowDestructive must be true",
		}
	}
	if !e.cfg.RealExecutionEnabled {
		return &VHDXError{
			Code:    protocol.ErrCapabilityUnsupported,
			Message: "真實還原流程尚未啟用（B 案）；目前僅提供 DryRun 預覽，不會修改磁碟或 BCD",
		}
	}
	if e.cfg.BCDEntryID == "" || e.cfg.BCDEntryID == "{default}" {
		return &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: "BCD 開機項不可使用 {default}；必須建立專用開機項（B 案）",
		}
	}
	if _, _, err := e.resolvePaths("preflight.vhdx"); err != nil {
		return err
	}
	if (action == "CREATE" || action == "SWITCH") && vhdxFile != "" {
		phys := e.physicalPath(vhdxFile)
		if info, err := os.Stat(phys); err != nil || info.IsDir() {
			return &VHDXError{
				Code:    protocol.ErrInvalidState,
				Message: fmt.Sprintf("目標 VHDX 不存在：%s", phys),
			}
		}
	}
	return nil
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
	if !e.cfg.RealExecutionEnabled {
		return &VHDXError{
			Code:    protocol.ErrCapabilityUnsupported,
			Message: "真實還原流程尚未啟用（B 案）；目前僅提供 DryRun 預覽",
		}
	}
	return e.executor.Execute(plan)
}
