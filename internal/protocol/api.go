package protocol

import "encoding/json"

// Closed set of error codes defined in SPEC §5.1 and §3.4.
const (
	ErrUnauthorized          = "UNAUTHORIZED"
	ErrForbidden             = "FORBIDDEN"
	ErrRateLimited           = "RATE_LIMITED"
	ErrInvalidArgument       = "INVALID_ARGUMENT"
	ErrNotFound              = "NOT_FOUND"
	ErrInvalidState          = "INVALID_STATE"
	ErrIPInOtherGroup        = "IP_IN_OTHER_GROUP"
	ErrRestorePointLimit     = "RESTORE_POINT_LIMIT"
	ErrDeployInProgress      = "DEPLOY_IN_PROGRESS"
	ErrMACUnknown            = "MAC_UNKNOWN"
	ErrPENotReady            = "PE_NOT_READY"
	ErrCapabilityUnsupported = "CAPABILITY_UNSUPPORTED"
	ErrImageVerifyFailed     = "IMAGE_VERIFY_FAILED"
	ErrInternal              = "INTERNAL"

	// IPC specific error codes (SPEC §3.4)
	ErrPasswordRequired = "PASSWORD_REQUIRED"
)

// APIError represents the standard error format in REST responses.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// APIResponse represents the standard JSON envelope for REST API responses.
type APIResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *APIError       `json:"error,omitempty"`
}

// BatchTargets defines target computers for batch operations.
type BatchTargets struct {
	IPs     []string `json:"ips,omitempty"`
	GroupID *string  `json:"group_id,omitempty"`
}

// BatchResultItem represents the per-IP execution result in batch operations.
type BatchResultItem struct {
	IP    string    `json:"ip"`
	OK    bool      `json:"ok"`
	Error *APIError `json:"error"`
}

// BatchResponseData is the payload for batch operations results.
type BatchResponseData struct {
	Results []BatchResultItem `json:"results"`
}

// IPCRequest represents a request sent via Named Pipe IPC (NDJSON).
type IPCRequest struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// IPCResponse represents a response returned via Named Pipe IPC (NDJSON).
type IPCResponse struct {
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *APIError       `json:"error,omitempty"`
}
