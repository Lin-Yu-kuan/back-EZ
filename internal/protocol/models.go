package protocol

import (
	"encoding/json"
)

// IPMode represents the network identity IP configuration mode.
type IPMode string

const (
	IPModeStatic IPMode = "STATIC"
	IPModeDHCP   IPMode = "DHCP"
)

// NetworkIdentity represents the client network identity snapshot.
type NetworkIdentity struct {
	MACAddress       string   `json:"mac_address"`
	IPMode           IPMode   `json:"ip_mode"`
	IPAddress        string   `json:"ip_address"`
	SubnetMask       string   `json:"subnet_mask"`
	DefaultGateway   string   `json:"default_gateway"`
	DNSServers       []string `json:"dns_servers"`
	OriginalHostname string   `json:"original_hostname"`
	CapturedAt       string   `json:"captured_at"`
}

// RestorePointState represents the lifecycle state of a RestorePoint.
type RestorePointState string

const (
	RestorePointStateReady    RestorePointState = "READY"
	RestorePointStateCreating RestorePointState = "CREATING"
	RestorePointStateMerging  RestorePointState = "MERGING"
	RestorePointStateBroken   RestorePointState = "BROKEN"
)

// CreatedBy represents the creator origin of a RestorePoint.
type CreatedBy string

const (
	CreatedByAdminLocal  CreatedBy = "ADMIN_LOCAL"
	CreatedByAdminRemote CreatedBy = "ADMIN_REMOTE"
	CreatedBySystem      CreatedBy = "SYSTEM"
)

// RestorePoint represents a differential VHDX restore point.
type RestorePoint struct {
	PointID     string            `json:"point_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	VHDXFile    string            `json:"vhdx_file"`
	ParentID    *string           `json:"parent_id"`
	Depth       int               `json:"depth"`
	SizeBytes   int64             `json:"size_bytes"`
	IsBase      bool              `json:"is_base"`
	IsActive    bool              `json:"is_active"`
	State       RestorePointState `json:"state"`
	CreatedBy   CreatedBy         `json:"created_by"`
	CreatedAt   string            `json:"created_at"`
}

// MACSource represents how the MAC address was discovered or imported.
type MACSource string

const (
	MACSourceAgent  MACSource = "AGENT"
	MACSourceImport MACSource = "IMPORT"
	MACSourceARP    MACSource = "ARP"
	MACSourceManual MACSource = "MANUAL"
)

// ClientStatus represents the status of a ClientNode in the state machine.
type ClientStatus string

const (
	ClientStatusOffline          ClientStatus = "OFFLINE"
	ClientStatusWakingUp         ClientStatus = "WAKING_UP"
	ClientStatusOnlineWindows    ClientStatus = "ONLINE_WINDOWS"
	ClientStatusEnteringPE       ClientStatus = "ENTERING_PE"
	ClientStatusReadyForCast     ClientStatus = "READY_FOR_CAST"
	ClientStatusDeploying        ClientStatus = "DEPLOYING"
	ClientStatusVerifying        ClientStatus = "VERIFYING"
	ClientStatusPostRestoringNet ClientStatus = "POST_RESTORING_NET"
	ClientStatusError            ClientStatus = "ERROR"
)

// NetworkSnapshot wraps *NetworkIdentity to serialize as {} when uncaptured.
type NetworkSnapshot struct {
	Identity *NetworkIdentity
}

// MarshalJSON serializes empty snapshot as {} instead of null.
func (s NetworkSnapshot) MarshalJSON() ([]byte, error) {
	if s.Identity == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(s.Identity)
}

// UnmarshalJSON deserializes {} into nil Identity.
func (s *NetworkSnapshot) UnmarshalJSON(data []byte) error {
	if string(data) == "{}" || string(data) == "null" {
		s.Identity = nil
		return nil
	}
	var id NetworkIdentity
	if err := json.Unmarshal(data, &id); err != nil {
		return err
	}
	s.Identity = &id
	return nil
}

// ClientNode represents a managed client computer node.
type ClientNode struct {
	ClientID            string          `json:"client_id"`
	CurrentIP           string          `json:"current_ip"`
	MACAddress          *string         `json:"mac_address"`
	MACSource           *MACSource      `json:"mac_source"`
	Hostname            string          `json:"hostname"`
	OSEdition           string          `json:"os_edition"`
	AgentVersion        string          `json:"agent_version"`
	PEReady             bool            `json:"pe_ready"`
	GroupID             *string         `json:"group_id"`
	Status              ClientStatus    `json:"status"`
	AutoRestoreOnReboot bool            `json:"auto_restore_on_reboot"`
	ActivePointID       *string         `json:"active_point_id"`
	RestorePointIDs     []string        `json:"restore_point_ids"`
	NetworkSnapshot     NetworkSnapshot `json:"network_snapshot"`
	LastTaskID          *string         `json:"last_task_id"`
	ProgressPercent     *float64        `json:"progress_percent"`
	ErrorMessage        *string         `json:"error_message"`
	LastSeen            string          `json:"last_seen"`
}

// IPGroup represents a grouped set of IP addresses.
type IPGroup struct {
	GroupID   string   `json:"group_id"`
	GroupName string   `json:"group_name"`
	IPList    []string `json:"ip_list"`
	CreatedAt string   `json:"created_at"`
}

// TaskState represents the execution state of a DeployTask.
type TaskState string

const (
	TaskStatePendingPrepare TaskState = "PENDING_PREPARE"
	TaskStatePreparing      TaskState = "PREPARING"
	TaskStateReady          TaskState = "READY"
	TaskStateCasting        TaskState = "CASTING"
	TaskStateVerifying      TaskState = "VERIFYING"
	TaskStatePostRestore    TaskState = "POST_RESTORE"
	TaskStateSucceeded      TaskState = "SUCCEEDED"
	TaskStateFailed         TaskState = "FAILED"
	TaskStateCancelled      TaskState = "CANCELLED"
)

// DeployTask represents a master-to-target multicast deployment task.
type DeployTask struct {
	TaskID              string    `json:"task_id"`
	State               TaskState `json:"state"`
	MasterIP            string    `json:"master_ip"`
	TargetIPs           []string  `json:"target_ips"`
	ImageSHA256         string    `json:"image_sha256"`
	ImageBytes          int64     `json:"image_bytes"`
	ImagePath           string    `json:"image_path"`
	ResetsRestorePoints bool      `json:"resets_restore_points"`
	CreatedAt           string    `json:"created_at"`
	StartedAt           *string   `json:"started_at"`
	FinishedAt          *string   `json:"finished_at"`
	ErrorCode           *string   `json:"error_code"`
	ErrorMessage        *string   `json:"error_message"`
}
