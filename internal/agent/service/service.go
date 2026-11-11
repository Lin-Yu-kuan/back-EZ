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
type AgentCore struct {
	cfg        AgentConfig
	configPath string
	mu         sync.RWMutex

	ipcServer *ipc.Server
	peBoot    *vhdx.PEBootManager
	capturer  *capture.Capturer
	status    protocol.ClientStatus
	peReady   bool
	activePID *string
	points    []protocol.RestorePoint
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
		peReady:    true,
		points: []protocol.RestorePoint{
			{
				PointID:     "rp_base",
				Name:        "Base System",
				Description: "Original Clean Base Image",
				VHDXFile:    "base.vhdx",
				Depth:       0,
				SizeBytes:   32212254720,
				IsBase:      true,
				IsActive:    true,
				State:       protocol.RestorePointStateReady,
				CreatedBy:   protocol.CreatedBySystem,
				CreatedAt:   time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	basePID := "rp_base"
	core.activePID = &basePID

	peCfg := vhdx.PEBootConfig{
		DryRun:           true,
		ReserveRoot:      cfg.ReserveRoot,
		PhysicalDiskRoot: cfg.ReserveRoot,
	}
	core.peBoot = vhdx.NewPEBootManager(peCfg, nil)
	core.capturer = capture.NewCapturer(nil)

	srv, err := ipc.NewServer(core, initialPassword)
	if err != nil {
		return nil, err
	}
	core.ipcServer = srv

	return core, nil
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
		"status":        a.status,
	}, nil
}

func (a *AgentCore) GetStatus() (any, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return map[string]any{
		"status":                 a.status,
		"active_point_id":        a.activePID,
		"auto_restore_on_reboot": a.cfg.AutoRestore,
	}, nil
}

func (a *AgentCore) ListRestorePoints() ([]protocol.RestorePoint, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.points, nil
}

func (a *AgentCore) CreateRestorePoint(name, desc string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.points) >= 5 {
		return errors.New(protocol.ErrRestorePointLimit)
	}

	pid := fmt.Sprintf("rp_%d", time.Now().Unix())
	newRP := protocol.RestorePoint{
		PointID:     pid,
		Name:        name,
		Description: desc,
		VHDXFile:    pid + ".vhdx",
		ParentID:    a.activePID,
		Depth:       len(a.points),
		SizeBytes:   0,
		IsBase:      false,
		IsActive:    true,
		State:       protocol.RestorePointStateReady,
		CreatedBy:   protocol.CreatedByAdminLocal,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	for i := range a.points {
		a.points[i].IsActive = false
	}
	a.points = append(a.points, newRP)
	a.activePID = &pid

	a.ipcServer.BroadcastEvent("restore_points_updated", a.points)
	return nil
}

func (a *AgentCore) SwitchRestorePoint(pointID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	found := false
	for i := range a.points {
		if a.points[i].PointID == pointID {
			a.points[i].IsActive = true
			found = true
		} else {
			a.points[i].IsActive = false
		}
	}
	if !found {
		return errors.New(protocol.ErrNotFound)
	}

	a.activePID = &pointID
	a.ipcServer.BroadcastEvent("restore_points_updated", a.points)
	return nil
}

func (a *AgentCore) DeleteRestorePoint(pointID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.activePID != nil && *a.activePID == pointID {
		return errors.New(protocol.ErrInvalidState)
	}

	var updated []protocol.RestorePoint
	found := false
	for _, p := range a.points {
		if p.PointID == pointID {
			if p.IsBase {
				return errors.New(protocol.ErrInvalidState)
			}
			found = true
			continue
		}
		updated = append(updated, p)
	}
	if !found {
		return errors.New(protocol.ErrNotFound)
	}

	a.points = updated
	a.ipcServer.BroadcastEvent("restore_points_updated", a.points)
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
