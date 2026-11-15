package vhdx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owner/back-ez/internal/protocol"
)

// LiveLayer is the writable VHDX layer that the machine currently boots from.
// It is not a restore point: points are immutable snapshots, while the live
// layer accumulates the running system's changes until the next create/switch.
type LiveLayer struct {
	File          string `json:"file"`
	ParentPointID string `json:"parent_point_id"`
	BootEntryID   string `json:"boot_entry_id"`
	BCDBackup     string `json:"bcd_backup,omitempty"`
	CreatedAt     string `json:"created_at"`
}

func loadLiveLayer(path string) (*LiveLayer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var live LiveLayer
	if err := json.Unmarshal(data, &live); err != nil {
		return nil, fmt.Errorf("parse live layer metadata: %w", err)
	}
	if strings.TrimSpace(live.File) == "" {
		return nil, fmt.Errorf("live layer file is empty")
	}
	return &live, nil
}

func saveLiveLayer(path string, live *LiveLayer) error {
	if live == nil {
		return fmt.Errorf("live layer is nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create live metadata directory: %w", err)
	}
	data, err := json.MarshalIndent(live, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal live layer: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write live layer metadata: %w", err)
	}
	return os.Rename(tmp, path)
}

func (e *Engine) liveMetadataPath() string {
	if e.cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(e.cfg.DataDir, "live.json")
}

func (e *Engine) persistLiveLocked() error {
	path := e.liveMetadataPath()
	if path == "" {
		return nil
	}
	return saveLiveLayer(path, e.live)
}

func (e *Engine) addWarningLocked(message string) {
	if e.loadWarning != "" {
		e.loadWarning += "；"
	}
	e.loadWarning += message
}

// reconcileLiveLocked checks whether the machine is actually booted from the
// recorded live layer, and finishes the "create" transition after a reboot.
func (e *Engine) reconcileLiveLocked() {
	if e.live == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	output, err := e.bcd.Enumerate(ctx, "{current}")
	if err != nil {
		e.addWarningLocked("無法確認目前開機來源（通常需要管理員權限）：" + err.Error())
		return
	}
	if !strings.Contains(output, e.live.File) {
		e.addWarningLocked("目前系統不是從 VHDX live layer 開機；建立/還原後必須重新開機才會生效")
		return
	}

	parent := e.chain.GetPoint(e.live.ParentPointID)
	if parent == nil {
		e.addWarningLocked("live layer 的父還原點不存在，鏈資料可能損壞")
		return
	}
	if parent.State == protocol.RestorePointStateCreating {
		// The reboot completed: the old live layer is now frozen and is a point.
		e.chain.SetState(parent.PointID, protocol.RestorePointStateReady)
		if err := e.persistMetadataLocked(); err != nil {
			e.addWarningLocked("還原點已凍結，但鏈資料寫入失敗：" + err.Error())
		}
	}
}

// realPreflightLocked validates everything the real create/switch path needs.
// It deliberately does not use cfg.BCDEntryID: the dedicated entry lives in
// live.json (created by provisioning).
func (e *Engine) realPreflightLocked() error {
	if !e.cfg.AllowDestructive {
		return &VHDXError{
			Code:    protocol.ErrForbidden,
			Message: "destructive operations are disabled; AllowDestructive must be true",
		}
	}
	if !e.cfg.RealExecutionEnabled {
		return &VHDXError{
			Code:    protocol.ErrCapabilityUnsupported,
			Message: "真實還原流程尚未啟用（缺少 B 案開關）",
		}
	}
	if e.live == nil {
		return &VHDXError{
			Code:    protocol.ErrCapabilityUnsupported,
			Message: "尚未完成 VHDX 原生開機建置（找不到 live layer）",
		}
	}
	if !validEntryID(e.live.BootEntryID) {
		return &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: "live layer 缺少有效的專用 BCD 開機項",
		}
	}
	if _, _, err := e.resolvePaths("probe.vhdx"); err != nil {
		return err
	}
	for _, point := range e.chain.List() {
		if point.State == protocol.RestorePointStateCreating {
			return &VHDXError{
				Code:    protocol.ErrInvalidState,
				Message: "上一個還原點尚未完成（需重新開機）；請先重開機後再操作",
			}
		}
	}
	return nil
}

func (e *Engine) backupBCD(ctx context.Context, label string) (string, error) {
	path := e.physicalPath(fmt.Sprintf("bcd_backup_%s_%s.bcd", label, time.Now().UTC().Format("20060102_150405")))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("建立 BCD 備份資料夾失敗：%w", err)
	}
	if err := e.bcd.Backup(ctx, path); err != nil {
		return "", fmt.Errorf("匯出 BCD 失敗：%w", err)
	}
	return path, nil
}

func (e *Engine) rollbackRealLocked(ctx context.Context, backupPath, newLiveFile string) []string {
	var problems []string
	if backupPath != "" {
		if err := e.bcd.Restore(ctx, backupPath); err != nil {
			problems = append(problems, "BCD 還原失敗："+err.Error())
		}
	}
	if newLiveFile != "" {
		if err := e.disk.Delete(ctx, e.physicalPath(newLiveFile)); err != nil {
			problems = append(problems, "刪除新 live layer 失敗："+err.Error())
		}
	}
	return problems
}

func (e *Engine) nextPointIDLocked(base string) string {
	id := base
	for i := 2; e.chain.GetPoint(id) != nil; i++ {
		id = fmt.Sprintf("%s_%d", base, i)
	}
	return id
}

func (e *Engine) nextLiveFileLocked() string {
	base := fmt.Sprintf("live_%s", time.Now().UTC().Format("20060102_150405"))
	name := base + ".vhdx"
	for i := 2; e.disk.Exists(e.physicalPath(name)); i++ {
		name = fmt.Sprintf("%s_%d.vhdx", base, i)
	}
	return name
}

func (e *Engine) createRealLocked(req CreateRequest) (*protocol.RestorePoint, *ExecutionPlan, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	if err := e.realPreflightLocked(); err != nil {
		return nil, nil, err
	}
	parent := e.chain.GetPoint(e.live.ParentPointID)
	if parent == nil {
		return nil, nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: "live layer 的父還原點不存在",
		}
	}
	if parent.State != protocol.RestorePointStateReady {
		return nil, nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: fmt.Sprintf("父還原點狀態為 %s，需為 READY", parent.State),
		}
	}
	if parent.Depth+1 > MaxChainDepth {
		return nil, nil, &VHDXError{
			Code:    protocol.ErrRestorePointLimit,
			Message: fmt.Sprintf("已達還原點深度上限 %d", MaxChainDepth),
		}
	}
	if !e.disk.Exists(e.physicalPath(e.live.File)) {
		return nil, nil, &VHDXError{
			Code:    protocol.ErrInvalidState,
			Message: "目前的 live layer 檔案不存在：" + e.physicalPath(e.live.File),
		}
	}

	now := time.Now().UTC()
	pointID := e.nextPointIDLocked(fmt.Sprintf("rp_%s", now.Format("20060102_150405")))
	newLiveFile := e.nextLiveFileLocked()

	// 1. New writable layer based on the current live layer.
	if err := e.disk.CreateDifferencing(ctx, e.physicalPath(newLiveFile), e.physicalPath(e.live.File)); err != nil {
		return nil, nil, &VHDXError{Code: protocol.ErrInternal, Message: "建立差分 VHDX 失敗：" + err.Error()}
	}

	// 2. Point the dedicated BCD entry at the new layer, with a backup.
	device, _, pathErr := e.resolvePaths(newLiveFile)
	if pathErr != nil {
		_ = e.disk.Delete(ctx, e.physicalPath(newLiveFile))
		return nil, nil, pathErr
	}
	backupPath, backupErr := e.backupBCD(ctx, "create_"+pointID)
	if backupErr != nil {
		_ = e.disk.Delete(ctx, e.physicalPath(newLiveFile))
		return nil, nil, &VHDXError{Code: protocol.ErrInternal, Message: backupErr.Error()}
	}
	if err := e.bcd.SetTarget(ctx, e.live.BootEntryID, device); err != nil {
		_ = e.rollbackRealLocked(ctx, backupPath, newLiveFile)
		return nil, nil, &VHDXError{Code: protocol.ErrInternal, Message: "設定 BCD 開機目標失敗：" + err.Error()}
	}
	if err := e.bcd.Verify(ctx, e.live.BootEntryID, device); err != nil {
		_ = e.rollbackRealLocked(ctx, backupPath, newLiveFile)
		return nil, nil, &VHDXError{Code: protocol.ErrInternal, Message: "BCD 開機目標驗證失敗：" + err.Error()}
	}

	createdBy := req.CreatedBy
	if createdBy == "" {
		createdBy = protocol.CreatedByAdminLocal
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = fmt.Sprintf("還原點 %s", now.Format("2006-01-02 15:04"))
	}

	point := &protocol.RestorePoint{
		PointID:     pointID,
		Name:        name,
		Description: req.Description,
		VHDXFile:    e.live.File, // the previous live layer becomes the immutable point
		ParentID:    &parent.PointID,
		Depth:       parent.Depth + 1,
		SizeBytes:   0,
		IsBase:      false,
		IsActive:    true,
		State:       protocol.RestorePointStateCreating,
		CreatedBy:   createdBy,
		CreatedAt:   now.Format(time.RFC3339),
	}

	oldLive := *e.live
	oldActive := e.chain.GetActivePoint()
	if err := e.chain.AddPoint(point); err != nil {
		_ = e.rollbackRealLocked(ctx, backupPath, newLiveFile)
		return nil, nil, err
	}
	e.chain.SetActive(point.PointID)
	e.live = &LiveLayer{
		File:          newLiveFile,
		ParentPointID: point.PointID,
		BootEntryID:   oldLive.BootEntryID,
		BCDBackup:     backupPath,
		CreatedAt:     now.Format(time.RFC3339),
	}

	if err := e.persistLiveLocked(); err != nil {
		e.chain.RemovePoint(point.PointID)
		if oldActive != nil {
			e.chain.SetActive(oldActive.PointID)
		}
		e.live = &oldLive
		_ = e.rollbackRealLocked(ctx, backupPath, newLiveFile)
		return nil, nil, &VHDXError{Code: protocol.ErrInternal, Message: "寫入 live layer 設定失敗：" + err.Error()}
	}
	if err := e.persistMetadataLocked(); err != nil {
		e.chain.RemovePoint(point.PointID)
		if oldActive != nil {
			e.chain.SetActive(oldActive.PointID)
		}
		e.live = &oldLive
		_ = e.persistLiveLocked()
		_ = e.rollbackRealLocked(ctx, backupPath, newLiveFile)
		return nil, nil, &VHDXError{Code: protocol.ErrInternal, Message: "寫入鏈資料失敗：" + err.Error()}
	}

	plan := &ExecutionPlan{
		Action:        "CREATE_REAL",
		TargetPointID: pointID,
		Steps: []ExecutionStep{
			{Order: 1, Category: "VHDX", Command: newLiveFile, Description: "建立新的可寫入差分層"},
			{Order: 2, Category: "BCD", Command: device, Description: "設定專用 BCD 開機項指向新層"},
			{Order: 3, Category: "REBOOT", Command: "shutdown /r", Description: "重新開機以凍結舊層並套用新層"},
		},
		GeneratedAt:    now.Format(time.RFC3339),
		RequiresReboot: true,
	}
	return point, plan, nil
}

func (e *Engine) switchRealLocked(pointID string) (*ExecutionPlan, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	if err := e.realPreflightLocked(); err != nil {
		return nil, err
	}
	target := e.chain.GetPoint(pointID)
	if target == nil {
		return nil, &VHDXError{Code: protocol.ErrNotFound, Message: fmt.Sprintf("找不到還原點 %q", pointID)}
	}
	if target.State != protocol.RestorePointStateReady {
		return nil, &VHDXError{Code: protocol.ErrInvalidState, Message: fmt.Sprintf("還原點狀態為 %s，需為 READY", target.State)}
	}
	if !e.disk.Exists(e.physicalPath(target.VHDXFile)) {
		return nil, &VHDXError{Code: protocol.ErrInvalidState, Message: "目標還原點檔案不存在：" + e.physicalPath(target.VHDXFile)}
	}

	now := time.Now().UTC()
	newLiveFile := e.nextLiveFileLocked()
	if err := e.disk.CreateDifferencing(ctx, e.physicalPath(newLiveFile), e.physicalPath(target.VHDXFile)); err != nil {
		return nil, &VHDXError{Code: protocol.ErrInternal, Message: "建立新的可寫入層失敗：" + err.Error()}
	}

	device, _, pathErr := e.resolvePaths(newLiveFile)
	if pathErr != nil {
		_ = e.disk.Delete(ctx, e.physicalPath(newLiveFile))
		return nil, pathErr
	}
	backupPath, backupErr := e.backupBCD(ctx, "switch_"+target.PointID)
	if backupErr != nil {
		_ = e.disk.Delete(ctx, e.physicalPath(newLiveFile))
		return nil, &VHDXError{Code: protocol.ErrInternal, Message: backupErr.Error()}
	}
	if err := e.bcd.SetTarget(ctx, e.live.BootEntryID, device); err != nil {
		_ = e.rollbackRealLocked(ctx, backupPath, newLiveFile)
		return nil, &VHDXError{Code: protocol.ErrInternal, Message: "設定 BCD 開機目標失敗：" + err.Error()}
	}
	if err := e.bcd.Verify(ctx, e.live.BootEntryID, device); err != nil {
		_ = e.rollbackRealLocked(ctx, backupPath, newLiveFile)
		return nil, &VHDXError{Code: protocol.ErrInternal, Message: "BCD 開機目標驗證失敗：" + err.Error()}
	}

	oldLive := *e.live
	oldActive := e.chain.GetActivePoint()
	e.live = &LiveLayer{
		File:          newLiveFile,
		ParentPointID: target.PointID,
		BootEntryID:   oldLive.BootEntryID,
		BCDBackup:     backupPath,
		CreatedAt:     now.Format(time.RFC3339),
	}
	e.chain.SetActive(target.PointID)

	if err := e.persistLiveLocked(); err != nil {
		if oldActive != nil {
			e.chain.SetActive(oldActive.PointID)
		}
		e.live = &oldLive
		_ = e.rollbackRealLocked(ctx, backupPath, newLiveFile)
		return nil, &VHDXError{Code: protocol.ErrInternal, Message: "寫入 live layer 設定失敗：" + err.Error()}
	}
	if err := e.persistMetadataLocked(); err != nil {
		if oldActive != nil {
			e.chain.SetActive(oldActive.PointID)
		}
		e.live = &oldLive
		_ = e.persistLiveLocked()
		_ = e.rollbackRealLocked(ctx, backupPath, newLiveFile)
		return nil, &VHDXError{Code: protocol.ErrInternal, Message: "寫入鏈資料失敗：" + err.Error()}
	}

	plan := &ExecutionPlan{
		Action:        "SWITCH_REAL",
		TargetPointID: target.PointID,
		Steps: []ExecutionStep{
			{Order: 1, Category: "VHDX", Command: newLiveFile, Description: "以目標還原點為基礎建立新的可寫入層"},
			{Order: 2, Category: "BCD", Command: device, Description: "設定專用 BCD 開機項指向新層"},
			{Order: 3, Category: "REBOOT", Command: "shutdown /r", Description: "重新開機以套用目標還原點"},
		},
		GeneratedAt:    now.Format(time.RFC3339),
		RequiresReboot: true,
	}
	return plan, nil
}

func (e *Engine) deleteRealLocked(pointID string) (*ExecutionPlan, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := e.realPreflightLocked(); err != nil {
		return nil, err
	}
	target := e.chain.GetPoint(pointID)
	if target == nil {
		return nil, &VHDXError{Code: protocol.ErrNotFound, Message: fmt.Sprintf("找不到還原點 %q", pointID)}
	}
	if target.IsActive {
		return nil, &VHDXError{Code: protocol.ErrInvalidState, Message: "無法刪除目前使用中的還原點"}
	}
	if target.IsBase {
		return nil, &VHDXError{Code: protocol.ErrInvalidState, Message: "無法刪除基準還原點"}
	}
	if children := e.chain.GetChildren(pointID); len(children) > 0 {
		return nil, &VHDXError{Code: protocol.ErrInvalidState, Message: fmt.Sprintf("此還原點仍有 %d 個子節點", len(children))}
	}

	if err := e.disk.Delete(ctx, e.physicalPath(target.VHDXFile)); err != nil {
		return nil, &VHDXError{Code: protocol.ErrInternal, Message: "刪除 VHDX 失敗：" + err.Error()}
	}
	if err := e.chain.RemovePoint(pointID); err != nil {
		return nil, err
	}
	if err := e.persistMetadataLocked(); err != nil {
		return nil, &VHDXError{Code: protocol.ErrInternal, Message: "寫入鏈資料失敗：" + err.Error()}
	}

	return &ExecutionPlan{
		Action:        "DELETE_REAL",
		TargetPointID: pointID,
		Steps: []ExecutionStep{
			{Order: 1, Category: "VHDX", Command: target.VHDXFile, Description: "刪除差分 VHDX 檔案"},
			{Order: 2, Category: "CHAIN", Command: pointID, Description: "從鏈資料移除節點"},
		},
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}
