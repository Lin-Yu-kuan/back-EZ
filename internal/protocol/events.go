package protocol

import "encoding/json"

// EventType represents the event name in WSMessage.
type EventType string

// Browser channel event types (Hub -> Web UI)
const (
	EventHello                EventType = "HELLO"
	EventClientStatusChanged  EventType = "CLIENT_STATUS_CHANGED"
	EventClientUpserted       EventType = "CLIENT_UPSERTED"
	EventGroupUpdated         EventType = "GROUP_UPDATED"
	EventRestorePointsUpdated EventType = "RESTORE_POINTS_UPDATED"
	EventWOLSent              EventType = "WOL_SENT"
	EventTaskCreated          EventType = "TASK_CREATED"
	EventDeployProgress       EventType = "DEPLOY_PROGRESS"
	EventDeployFinished       EventType = "DEPLOY_FINISHED"
	EventDeployFailed         EventType = "DEPLOY_FAILED"
	EventAgentError           EventType = "AGENT_ERROR"
)

// Agent command event types (Hub -> Agent)
const (
	CmdPrepareDeploy        EventType = "CMD_PREPARE_DEPLOY"
	CmdEnterPE              EventType = "CMD_ENTER_PE"
	CmdStartSender          EventType = "CMD_START_SENDER"
	CmdStartReceiver        EventType = "CMD_START_RECEIVER"
	CmdAbort                EventType = "CMD_ABORT"
	CmdCollectRestorePoints EventType = "CMD_COLLECT_RESTORE_POINTS"
	CmdSyncPassword         EventType = "CMD_SYNC_PASSWORD"
	CmdPing                 EventType = "CMD_PING"
)

// Agent report event types (Agent -> Hub)
const (
	ReportAgentHello        EventType = "AGENT_HELLO"
	ReportAgentStatus       EventType = "AGENT_STATUS"
	ReportAgentProgress     EventType = "AGENT_PROGRESS"
	ReportNetworkIdentity   EventType = "NETWORK_IDENTITY"
	ReportImageVerifyResult EventType = "IMAGE_VERIFY_RESULT"
	ReportCmdResult         EventType = "CMD_RESULT"
)

// WSMessage represents the uniform envelope for all WebSocket messages.
type WSMessage struct {
	V         int             `json:"v"`
	Seq       int64           `json:"seq"`
	Event     EventType       `json:"event"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// HelloPayload is sent immediately after WebSocket connection is established.
type HelloPayload struct {
	ServerTime      string `json:"server_time"`
	Seq             int64  `json:"seq"`
	ContractVersion int    `json:"contract_version"`
}

// ClientStatusChangedPayload is pushed when a client transitions state.
type ClientStatusChangedPayload struct {
	ClientID string       `json:"client_id"`
	IP       string       `json:"ip"`
	From     ClientStatus `json:"from"`
	To       ClientStatus `json:"to"`
}

// ClientUpsertedPayload is pushed when client node information is added or updated.
type ClientUpsertedPayload struct {
	Client ClientNode `json:"client"`
}

// GroupUpdatedPayload is pushed when an IPGroup is created or modified.
type GroupUpdatedPayload struct {
	Group IPGroup `json:"group"`
}

// RestorePointsUpdatedPayload is pushed when restore points for an IP change.
type RestorePointsUpdatedPayload struct {
	IP            string         `json:"ip"`
	RestorePoints []RestorePoint `json:"restore_points"`
}

// WOLSentPayload is pushed when Wake-on-LAN packets are broadcast.
type WOLSentPayload struct {
	IPs  []string `json:"ips"`
	MACs []string `json:"macs"`
}

// TaskCreatedPayload is pushed when a new DeployTask is initiated.
type TaskCreatedPayload struct {
	Task DeployTask `json:"task"`
}

// DeployClientProgress describes the progress of an individual client in a deployment.
type DeployClientProgress struct {
	IP       string       `json:"ip"`
	Progress float64      `json:"progress"`
	Status   ClientStatus `json:"status"`
}

// DeployProgressPayload is periodically pushed during deployment.
type DeployProgressPayload struct {
	TaskID    string                 `json:"task_id"`
	MasterIP  string                 `json:"master_ip"`
	SpeedMbps float64                `json:"speed_mbps"`
	Clients   []DeployClientProgress `json:"clients"`
}

// DeployFinishedPayload is pushed when deployment finishes successfully.
type DeployFinishedPayload struct {
	TaskID     string    `json:"task_id"`
	State      TaskState `json:"state"`
	FinishedAt string    `json:"finished_at"`
}

// DeployFailedPayload is pushed when deployment fails.
type DeployFailedPayload struct {
	TaskID    string   `json:"task_id"`
	ErrorCode string   `json:"error_code"`
	Message   string   `json:"message"`
	FailedIPs []string `json:"failed_ips"`
}

// AgentErrorPayload is pushed when a non-task agent error occurs.
type AgentErrorPayload struct {
	IP        string `json:"ip"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

// PERole represents the target boot role in WinPE.
type PERole string

const (
	PERoleSender   PERole = "SENDER"
	PERoleReceiver PERole = "RECEIVER"
)

// AgentMode represents the environment where Agent is executing.
type AgentMode string

const (
	AgentModeWindows AgentMode = "WINDOWS"
	AgentModePE      AgentMode = "PE"
)

// ProgressPhase represents the direction of multicast transfer.
type ProgressPhase string

const (
	ProgressPhaseReceiving ProgressPhase = "RECEIVING"
	ProgressPhaseSending   ProgressPhase = "SENDING"
)

// CmdPrepareDeployPayload is the payload for CMD_PREPARE_DEPLOY.
type CmdPrepareDeployPayload struct {
	CmdID     string `json:"cmd_id"`
	TaskID    string `json:"task_id"`
	ExpiresAt string `json:"expires_at"`
}

// CmdEnterPEPayload is the payload for CMD_ENTER_PE.
type CmdEnterPEPayload struct {
	CmdID     string `json:"cmd_id"`
	Role      PERole `json:"role"`
	TaskID    string `json:"task_id"`
	ExpiresAt string `json:"expires_at"`
}

// CmdStartSenderPayload is the payload for CMD_START_SENDER.
type CmdStartSenderPayload struct {
	CmdID       string   `json:"cmd_id"`
	TargetIPs   []string `json:"target_ips"`
	ImageSHA256 string   `json:"image_sha256"`
	ImageBytes  int64    `json:"image_bytes"`
	ExpiresAt   string   `json:"expires_at"`
}

// CmdStartReceiverPayload is the payload for CMD_START_RECEIVER.
type CmdStartReceiverPayload struct {
	CmdID       string `json:"cmd_id"`
	MasterIP    string `json:"master_ip"`
	ImageSHA256 string `json:"image_sha256"`
	ImageBytes  int64  `json:"image_bytes"`
	ExpiresAt   string `json:"expires_at"`
}

// CmdAbortPayload is the payload for CMD_ABORT.
type CmdAbortPayload struct {
	CmdID  string `json:"cmd_id"`
	Reason string `json:"reason"`
}

// CmdCollectRestorePointsPayload is the payload for CMD_COLLECT_RESTORE_POINTS.
type CmdCollectRestorePointsPayload struct {
	CmdID string `json:"cmd_id"`
}

// CmdSyncPasswordPayload is the payload for CMD_SYNC_PASSWORD.
type CmdSyncPasswordPayload struct {
	CmdID       string `json:"cmd_id"`
	NewPassword string `json:"new_password"`
	ExpiresAt   string `json:"expires_at"`
}

// CmdPingPayload is the payload for CMD_PING.
type CmdPingPayload struct {
	CmdID string `json:"cmd_id"`
}

// AgentHelloPayload is the payload for AGENT_HELLO.
type AgentHelloPayload struct {
	ClientID     string    `json:"client_id"`
	Hostname     string    `json:"hostname"`
	OSEdition    string    `json:"os_edition"`
	AgentVersion string    `json:"agent_version"`
	MACAddress   *string   `json:"mac_address"`
	CurrentIP    string    `json:"current_ip"`
	PEReady      bool      `json:"pe_ready"`
	Mode         AgentMode `json:"mode"`
}

// AgentStatusPayload is the payload for AGENT_STATUS.
type AgentStatusPayload struct {
	ClientID string       `json:"client_id"`
	Status   ClientStatus `json:"status"`
	Reason   *string      `json:"reason,omitempty"`
}

// AgentProgressPayload is the payload for AGENT_PROGRESS.
type AgentProgressPayload struct {
	TaskID    string        `json:"task_id"`
	Percent   float64       `json:"percent"`
	SpeedMbps float64       `json:"speed_mbps"`
	Phase     ProgressPhase `json:"phase"`
}

// NetworkIdentityPayload is the payload for NETWORK_IDENTITY.
type NetworkIdentityPayload struct {
	TaskID   string          `json:"task_id"`
	Identity NetworkIdentity `json:"identity"`
}

// ImageVerifyResultPayload is the payload for IMAGE_VERIFY_RESULT.
type ImageVerifyResultPayload struct {
	TaskID string `json:"task_id"`
	OK     bool   `json:"ok"`
	SHA256 string `json:"sha256"`
}

// CmdResultPayload is the payload for CMD_RESULT.
type CmdResultPayload struct {
	CmdID string  `json:"cmd_id"`
	OK    bool    `json:"ok"`
	Error *string `json:"error,omitempty"`
}
