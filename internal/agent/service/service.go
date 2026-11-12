package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/owner/back-ez/internal/agent/ipc"
	"github.com/owner/back-ez/internal/protocol"
	"github.com/owner/back-ez/internal/workers/netpreserve/capture"
	"github.com/owner/back-ez/internal/workers/vhdx"
)

// AgentConfig represents local agent configuration stored in the reserve partition.
type AgentConfig struct {
	ClientID    string `json:"client_id"`
	HubURL      string `json:"hub_url"`
	ReserveRoot string `json:"reserve_root"`
	PEWIMPath   string `json:"pe_wim_path"`
	AutoRestore bool   `json:"auto_restore"`
}

// AgentCore encapsulates the Agent service business logic, IPC delegate and Hub connection.
//
// 本機還原點的唯一狀態來源是 vhdx.Engine：AgentCore 不自行保存、也不自行寫入
// chain.json（舊版同時用陣列格式寫入，造成 Engine 永遠載入失敗）。
type AgentCore struct {
	cfg        AgentConfig
	configPath string
	mu         sync.RWMutex

	ipcServer *ipc.Server
	peBoot    *vhdx.PEBootManager
	capturer  *capture.Capturer
	engine    *vhdx.Engine
	logger    *OperationLogger
	status    protocol.ClientStatus
	peReady   bool
}

// LoadOrCreateConfig loads client configuration or initializes a new permanent client_id.
func LoadOrCreateConfig(reserveRoot string) (*AgentConfig, string, error) {
	if reserveRoot == "" {
		reserveRoot = filepath.Join("C:", "EZRestore")
	}
	_ = os.MkdirAll(reserveRoot, 0755)

	configPath := filepath.Join(reserveRoot, "agent_config.json")
	if data, err := os.ReadFile(configPath); err == nil {
		var cfg AgentConfig
		if err := json.Unmarshal(data, &cfg); err == nil && cfg.ClientID != "" {
			return &cfg, configPath, nil
		}
	}

	// Generate permanent client_id (SPEC §2.3: cli_[0-9a-f]{16})
	bytes := make([]byte, 8) // 8 bytes = 16 hex chars
	_, _ = rand.Read(bytes)
	clientID := fmt.Sprintf("cli_%s", hex.EncodeToString(bytes))

	cfg := AgentConfig{
		ClientID:    clientID,
		HubURL:      "https://localhost:8443",
		ReserveRoot: reserveRoot,
		PEWIMPath:   filepath.Join(reserveRoot, "PE", "boot.wim"),
		AutoRestore: true,
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return nil, "", fmt.Errorf("failed to save agent config: %w", err)
	}

	return &cfg, configPath, nil
}

func NewAgentCore(cfg AgentConfig, configPath string, initialPassword string) (*AgentCore, error) {
	core := &AgentCore{
		cfg:        cfg,
		configPath: configPath,
		status:     protocol.ClientStatusOnlineWindows,
	}

	peCfg := vhdx.PEBootConfig{
		DryRun:           true,
		AllowDestructive: false,
		ReserveRoot:      cfg.ReserveRoot,
		PhysicalDiskRoot: cfg.ReserveRoot,
	}
	core.peBoot = vhdx.NewPEBootManager(peCfg, nil)
	core.capturer = capture.NewCapturer(nil)
	if core.peBoot != nil {
		core.peReady, _ = core.peBoot.CheckPEReady()
	}

	srv, err := ipc.NewServer(core, initialPassword)
	if err != nil {
		return nil, err
	}
	core.ipcServer = srv

	return core, nil
}

func (a *AgentCore) SetEngine(e *vhdx.Engine) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.engine = e
}

func (a *AgentCore) Engine() *vhdx.Engine {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.engine
}

func (a *AgentCore) SetLogger(l *OperationLogger) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logger = l
}

func (a *AgentCore) Logger() *OperationLogger {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.logger
}

func (a *AgentCore) IPCServer() *ipc.Server {
	return a.ipcServer
}

func (a *AgentCore) SetPEBootManager(m *vhdx.PEBootManager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.peBoot = m
}

func (a *AgentCore) SetPEReady(ready bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.peReady = ready
}

func (a *AgentCore) SetCapturer(c *capture.Capturer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.capturer = c
}

// --- IPC Delegate Implementations ---

func (a *AgentCore) GetAgentInfo() (any, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	hostname, _ := os.Hostname()
	return map[string]any{
		"client_id":     a.cfg.ClientID,
		"hostname":      hostname,
		"agent_version": "0.1.0",
		"os_edition":    "Windows 11 Education",
		"pe_ready":      a.peReady,
		"engine_ready":  a.engine != nil,
		"status":        a.status,
	}, nil
}

func (a *AgentCore) GetStatus() (any, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var activePointID any
	if a.engine != nil {
		if p := a.engine.GetActive(); p != nil {
			activePointID = p.PointID
		}
	}

	engineWarning := ""
	if a.engine != nil {
		engineWarning = a.engine.LoadWarning()
	}

	return map[string]any{
		"status":                 a.status,
		"active_point_id":        activePointID,
		"auto_restore_on_reboot": a.cfg.AutoRestore,
		"engine_ready":           a.engine != nil,
		"engine_warning":         engineWarning,
		"dry_run":                a.engine != nil && a.engine.DryRun(),
		"pending_reboot":         false,
	}, nil
}

// deleteModeNote 讓刪除日誌也能區分預覽與真實執行。
func deleteModeNote(engine *vhdx.Engine) string {
	if engine != nil && engine.DryRun() {
		return "（預覽模式：未刪除實體檔案）"
	}
	return ""
}

func (a *AgentCore) ListRestorePoints() ([]protocol.RestorePoint, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.engine == nil {
		return []protocol.RestorePoint{}, nil
	}
	return a.engine.List(), nil
}

func (a *AgentCore) CreateRestorePoint(name, desc string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		a.logLocked("CREATE", "(未命名)", "FAILED", "還原點名稱不可為空")
		return errors.New(protocol.ErrInvalidArgument)
	}
	if a.engine == nil {
		a.logLocked("CREATE", name, "FAILED", "VHDX 引擎不可用（尚未完成 VHDX 原生開機建置）")
		return errors.New(protocol.ErrCapabilityUnsupported)
	}

	point, plan, err := a.engine.Create(vhdx.CreateRequest{
		Name:        name,
		Description: desc,
		CreatedBy:   protocol.CreatedByAdminLocal,
	})
	if err != nil {
		a.logLocked("CREATE", name, "FAILED", err.Error())
		return err
	}

	steps := 0
	if plan != nil {
		steps = len(plan.Steps)
	}
	modeNote := ""
	if a.engine.DryRun() {
		modeNote = "（預覽模式：僅產生執行計畫，未修改磁碟/BCD）"
	}
	a.logLocked("CREATE", fmt.Sprintf("%s (%s)", point.Name, point.PointID), "SUCCESS",
		fmt.Sprintf("已建立差分還原點（depth=%d，共 %d 個執行步驟，需重新開機才生效）%s", point.Depth, steps, modeNote))

	if a.ipcServer != nil {
		a.ipcServer.BroadcastEvent("restore_points_updated", a.engine.List())
	}
	return nil
}

func (a *AgentCore) SwitchRestorePoint(pointID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.engine == nil {
		a.logLocked("SWITCH", pointID, "FAILED", "VHDX 引擎不可用（尚未完成 VHDX 原生開機建置）")
		return errors.New(protocol.ErrCapabilityUnsupported)
	}

	target := a.engine.Get(pointID)
	if target == nil {
		a.logLocked("SWITCH", pointID, "FAILED", "找不到指定的還原點")
		return errors.New(protocol.ErrNotFound)
	}

	plan, err := a.engine.Switch(pointID)
	if err != nil {
		a.logLocked("SWITCH", fmt.Sprintf("%s (%s)", target.Name, pointID), "FAILED", err.Error())
		return err
	}

	backupPath := ""
	if plan != nil {
		backupPath = plan.BackupBCDPath
	}
	modeNote := ""
	if a.engine.DryRun() {
		modeNote = "（預覽模式：未修改 BCD）"
	}
	a.logLocked("SWITCH", fmt.Sprintf("%s (%s)", target.Name, pointID), "SUCCESS",
		fmt.Sprintf("已設定下次開機目標；請重新開機後生效（BCD 備份：%s）%s", backupPath, modeNote))

	if a.ipcServer != nil {
		a.ipcServer.BroadcastEvent("restore_points_updated", a.engine.List())
	}
	return nil
}

func (a *AgentCore) DeleteRestorePoint(pointID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.engine == nil {
		a.logLocked("DELETE", pointID, "FAILED", "VHDX 引擎不可用（尚未完成 VHDX 原生開機建置）")
		return errors.New(protocol.ErrCapabilityUnsupported)
	}

	var targetName string
	if p := a.engine.Get(pointID); p != nil {
		targetName = p.Name
	}

	if _, err := a.engine.Delete(pointID); err != nil {
		a.logLocked("DELETE", fmt.Sprintf("%s (%s)", targetName, pointID), "FAILED", err.Error())
		return err
	}

	a.logLocked("DELETE", fmt.Sprintf("%s (%s)", targetName, pointID), "SUCCESS", "已刪除還原點及其差分檔案"+deleteModeNote(a.engine))

	if a.ipcServer != nil {
		a.ipcServer.BroadcastEvent("restore_points_updated", a.engine.List())
	}
	return nil
}

func (a *AgentCore) SetAutoRestore(enabled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.cfg.AutoRestore = enabled
	data, _ := json.MarshalIndent(a.cfg, "", "  ")
	_ = os.WriteFile(a.configPath, data, 0644)
	return nil
}

func (a *AgentCore) SetHubEndpoint(hubURL, enrollToken string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.cfg.HubURL = hubURL
	data, _ := json.MarshalIndent(a.cfg, "", "  ")
	_ = os.WriteFile(a.configPath, data, 0644)
	return nil
}

func (a *AgentCore) UninstallService() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.status = protocol.ClientStatusOffline
	return nil
}

// logLocked 只在呼叫端已持有 a.mu 時使用，避免與 SetLogger 競爭。
func (a *AgentCore) logLocked(action, target, status, details string) {
	if a.logger != nil {
		a.logger.Log(action, target, status, details)
	}
}

// --- Command Execution & Expiration Framework ---

func (a *AgentCore) ExecuteCommand(ctx context.Context, msg protocol.WSMessage) (*protocol.CmdResultPayload, error) {
	switch msg.Event {
	case protocol.CmdPing:
		var ping protocol.CmdPingPayload
		if err := json.Unmarshal(msg.Payload, &ping); err != nil {
			return nil, err
		}
		return &protocol.CmdResultPayload{CmdID: ping.CmdID, OK: true}, nil

	case protocol.CmdPrepareDeploy:
		var prep protocol.CmdPrepareDeployPayload
		if err := json.Unmarshal(msg.Payload, &prep); err != nil {
			return nil, err
		}
		if prep.ExpiresAt != "" {
			exp, err := time.Parse(time.RFC3339, prep.ExpiresAt)
			if err == nil && time.Now().After(exp) {
				errMsg := "command expired"
				return &protocol.CmdResultPayload{CmdID: prep.CmdID, OK: false, Error: &errMsg}, nil
			}
		}
		if a.capturer != nil {
			if _, err := a.capturer.Capture(a.cfg.ReserveRoot); err != nil {
				errMsg := err.Error()
				return &protocol.CmdResultPayload{CmdID: prep.CmdID, OK: false, Error: &errMsg}, nil
			}
		}
		return &protocol.CmdResultPayload{CmdID: prep.CmdID, OK: true}, nil

	case protocol.CmdSyncPassword:
		var syncPayload protocol.CmdSyncPasswordPayload
		if err := json.Unmarshal(msg.Payload, &syncPayload); err != nil {
			return nil, err
		}

		// Check expiration (SPEC §5.3: expired commands MUST be dropped)
		if syncPayload.ExpiresAt != "" {
			exp, err := time.Parse(time.RFC3339, syncPayload.ExpiresAt)
			if err == nil && time.Now().After(exp) {
				errMsg := "command expired"
				return &protocol.CmdResultPayload{CmdID: syncPayload.CmdID, OK: false, Error: &errMsg}, nil
			}
		}

		if err := a.ipcServer.SetPassword(syncPayload.NewPassword); err != nil {
			errMsg := err.Error()
			return &protocol.CmdResultPayload{CmdID: syncPayload.CmdID, OK: false, Error: &errMsg}, nil
		}
		return &protocol.CmdResultPayload{CmdID: syncPayload.CmdID, OK: true}, nil

	case protocol.CmdEnterPE:
		var enter protocol.CmdEnterPEPayload
		if err := json.Unmarshal(msg.Payload, &enter); err != nil {
			return nil, err
		}
		if enter.ExpiresAt != "" {
			exp, err := time.Parse(time.RFC3339, enter.ExpiresAt)
			if err == nil && time.Now().After(exp) {
				errMsg := "command expired"
				return &protocol.CmdResultPayload{CmdID: enter.CmdID, OK: false, Error: &errMsg}, nil
			}
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if !a.peReady {
			errMsg := protocol.ErrPENotReady
			return &protocol.CmdResultPayload{CmdID: enter.CmdID, OK: false, Error: &errMsg}, nil
		}
		if a.peBoot != nil {
			if _, err := a.peBoot.TriggerOneTimePEBoot(); err != nil {
				errMsg := err.Error()
				return &protocol.CmdResultPayload{CmdID: enter.CmdID, OK: false, Error: &errMsg}, nil
			}
		}
		a.status = protocol.ClientStatusEnteringPE
		return &protocol.CmdResultPayload{CmdID: enter.CmdID, OK: true}, nil

	default:
		return nil, fmt.Errorf("unsupported command: %s", msg.Event)
	}
}

// RunIPCListener starts serving IPC requests on a net.Listener (Named Pipe or net.Pipe).
func (a *AgentCore) RunIPCListener(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go a.ipcServer.HandleConn(conn)
	}
}
