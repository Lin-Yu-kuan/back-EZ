package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/owner/back-ez/internal/protocol"
	"golang.org/x/crypto/bcrypt"
)

const PipeName = `\\.\pipe\ezrestore-agent`

// HandlerDelegate is implemented by the Agent service to satisfy IPC methods.
type HandlerDelegate interface {
	GetAgentInfo() (any, error)
	GetStatus() (any, error)
	ListRestorePoints() ([]protocol.RestorePoint, error)
	CreateRestorePoint(name, desc string) error
	SwitchRestorePoint(pointID string) error
	DeleteRestorePoint(pointID string) error
	OverwriteRestorePoint(pointID string) error
	SetAutoRestore(enabled bool) error
	SetHubEndpoint(hubURL, enrollToken string) error
	UninstallService() error
}

// Server handles Named Pipe / NDJSON IPC connections from the local UI client.
type Server struct {
	delegate HandlerDelegate
	listener net.Listener

	mu           sync.Mutex
	passwordHash string
	failAttempts int
	lockedUntil  time.Time

	subscribersMu sync.RWMutex
	subscribers   map[chan []byte]struct{}
}

func NewServer(delegate HandlerDelegate, initialPassword string) (*Server, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(initialPassword), 12)
	if err != nil {
		return nil, fmt.Errorf("failed to hash initial password: %w", err)
	}

	return &Server{
		delegate:     delegate,
		passwordHash: string(hash),
		subscribers:  make(map[chan []byte]struct{}),
	}, nil
}

// SetPassword updates the local admin password hash.
func (s *Server) SetPassword(newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passwordHash = string(hash)
	return nil
}

func (s *Server) verifyPassword(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if now.Before(s.lockedUntil) {
		return errors.New(protocol.ErrRateLimited)
	}

	if password == "" {
		return errors.New(protocol.ErrPasswordRequired)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(s.passwordHash), []byte(password)); err != nil {
		s.failAttempts++
		if s.failAttempts >= 5 {
			s.lockedUntil = now.Add(5 * time.Minute)
			s.failAttempts = 0
			return errors.New(protocol.ErrRateLimited)
		}
		return errors.New(protocol.ErrUnauthorized)
	}

	// Reset attempts on successful verification
	s.failAttempts = 0
	return nil
}

// BroadcastEvent publishes an event to all IPC subscribers.
func (s *Server) BroadcastEvent(event string, data any) {
	s.subscribersMu.RLock()
	defer s.subscribersMu.RUnlock()

	payload, _ := json.Marshal(map[string]any{
		"event": event,
		"data":  data,
	})

	for ch := range s.subscribers {
		select {
		case ch <- payload:
		default:
		}
	}
}

// HandleConn processes NDJSON requests on a single IPC connection.
func (s *Server) HandleConn(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			break
		}

		clean := strings.TrimSpace(string(line))
		if clean == "" {
			continue
		}

		var req protocol.IPCRequest
		if err := json.Unmarshal([]byte(clean), &req); err != nil {
			resp := protocol.IPCResponse{
				ID: 0,
				OK: false,
				Error: &protocol.APIError{
					Code:    protocol.ErrInvalidArgument,
					Message: "invalid NDJSON request format",
				},
			}
			s.writeResponse(conn, resp)
			continue
		}

		resp := s.dispatch(conn, req)
		s.writeResponse(conn, resp)
	}
}

func (s *Server) writeResponse(w io.Writer, resp protocol.IPCResponse) {
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	_, _ = w.Write(data)
}

func (s *Server) dispatch(conn net.Conn, req protocol.IPCRequest) protocol.IPCResponse {
	// Parse params map
	var params map[string]any
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &params)
	}

	password, _ := params["password"].(string)

	switch req.Method {
	// --- Read-only methods (Password free) ---
	case "GetAgentInfo":
		info, err := s.delegate.GetAgentInfo()
		if err != nil {
			return errorResponse(req.ID, protocol.ErrInternal, err.Error())
		}
		return successResponse(req.ID, info)

	case "GetStatus":
		status, err := s.delegate.GetStatus()
		if err != nil {
			return errorResponse(req.ID, protocol.ErrInternal, err.Error())
		}
		return successResponse(req.ID, status)

	case "ListRestorePoints":
		points, err := s.delegate.ListRestorePoints()
		if err != nil {
			return errorResponse(req.ID, protocol.ErrInternal, err.Error())
		}
		return successResponse(req.ID, points)

	case "Subscribe":
		subCh := make(chan []byte, 32)
		s.subscribersMu.Lock()
		s.subscribers[subCh] = struct{}{}
		s.subscribersMu.Unlock()

		go func() {
			defer func() {
				s.subscribersMu.Lock()
				delete(s.subscribers, subCh)
				s.subscribersMu.Unlock()
			}()
			for msg := range subCh {
				msg = append(msg, '\n')
				if _, err := conn.Write(msg); err != nil {
					break
				}
			}
		}()
		return successResponse(req.ID, map[string]bool{"subscribed": true})

	// --- Mutating methods (Password required) ---
	case "CreateRestorePoint":
		if err := s.verifyPassword(password); err != nil {
			return errorResponse(req.ID, err.Error(), "authentication failed")
		}
		name, _ := params["name"].(string)
		desc, _ := params["description"].(string)
		if err := s.delegate.CreateRestorePoint(name, desc); err != nil {
			return errorResponse(req.ID, protocol.ErrInternal, err.Error())
		}
		return successResponse(req.ID, map[string]bool{"created": true})

	case "SwitchRestorePoint":
		if err := s.verifyPassword(password); err != nil {
			return errorResponse(req.ID, err.Error(), "authentication failed")
		}
		pointID, _ := params["point_id"].(string)
		if pointID == "" {
			return errorResponse(req.ID, protocol.ErrInvalidArgument, "point_id required")
		}
		if err := s.delegate.SwitchRestorePoint(pointID); err != nil {
			return errorResponse(req.ID, protocol.ErrInternal, err.Error())
		}
		return successResponse(req.ID, map[string]bool{"switched": true})

	case "DeleteRestorePoint":
		if err := s.verifyPassword(password); err != nil {
			return errorResponse(req.ID, err.Error(), "authentication failed")
		}
		pointID, _ := params["point_id"].(string)
		if pointID == "" {
			return errorResponse(req.ID, protocol.ErrInvalidArgument, "point_id required")
		}
		if err := s.delegate.DeleteRestorePoint(pointID); err != nil {
			return errorResponse(req.ID, protocol.ErrInternal, err.Error())
		}
		return successResponse(req.ID, map[string]bool{"deleted": true})

	case "OverwriteRestorePoint":
		if err := s.verifyPassword(password); err != nil {
			return errorResponse(req.ID, err.Error(), "authentication failed")
		}
		pointID, _ := params["point_id"].(string)
		if pointID == "" {
			return errorResponse(req.ID, protocol.ErrInvalidArgument, "point_id required")
		}
		if err := s.delegate.OverwriteRestorePoint(pointID); err != nil {
			return errorResponse(req.ID, protocol.ErrInternal, err.Error())
		}
		return successResponse(req.ID, map[string]bool{"overwritten": true})

	case "SetAutoRestore":
		if err := s.verifyPassword(password); err != nil {
			return errorResponse(req.ID, err.Error(), "authentication failed")
		}
		enabled, _ := params["enabled"].(bool)
		if err := s.delegate.SetAutoRestore(enabled); err != nil {
			return errorResponse(req.ID, protocol.ErrInternal, err.Error())
		}
		return successResponse(req.ID, map[string]bool{"updated": true})

	// --- Privileged / UAC methods (Password required) ---
	case "SetHubEndpoint":
		if err := s.verifyPassword(password); err != nil {
			return errorResponse(req.ID, err.Error(), "authentication failed")
		}
		hubURL, _ := params["hub_url"].(string)
		enrollToken, _ := params["enroll_token"].(string)
		if err := s.delegate.SetHubEndpoint(hubURL, enrollToken); err != nil {
			return errorResponse(req.ID, protocol.ErrInternal, err.Error())
		}
		return successResponse(req.ID, map[string]bool{"configured": true})

	case "UninstallService":
		if err := s.verifyPassword(password); err != nil {
			return errorResponse(req.ID, err.Error(), "authentication failed")
		}
		if err := s.delegate.UninstallService(); err != nil {
			return errorResponse(req.ID, protocol.ErrInternal, err.Error())
		}
		return successResponse(req.ID, map[string]bool{"uninstalled": true})

	default:
		return errorResponse(req.ID, protocol.ErrInvalidArgument, fmt.Sprintf("unknown method: %s", req.Method))
	}
}

func successResponse(id int64, result any) protocol.IPCResponse {
	raw, _ := json.Marshal(result)
	return protocol.IPCResponse{
		ID:     id,
		OK:     true,
		Result: raw,
	}
}

func errorResponse(id int64, code, message string) protocol.IPCResponse {
	return protocol.IPCResponse{
		ID: id,
		OK: false,
		Error: &protocol.APIError{
			Code:    code,
			Message: message,
		},
	}
}
