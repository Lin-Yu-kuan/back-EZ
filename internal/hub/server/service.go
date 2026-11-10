package server

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/owner/back-ez/internal/hub/store"
	"github.com/owner/back-ez/internal/protocol"
	"golang.org/x/crypto/bcrypt"
)

// Service defines the business logic operations for the Control Hub.
type Service struct {
	store      *store.Store
	loginMu    sync.Mutex
	loginFails map[string][]time.Time // IP or username -> fail timestamps
}

func NewService(st *store.Store) *Service {
	return &Service{
		store:      st,
		loginFails: make(map[string][]time.Time),
	}
}

func (s *Service) checkLoginRateLimit(key string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-5 * time.Minute)

	var valid []time.Time
	for _, t := range s.loginFails[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	s.loginFails[key] = valid

	return len(valid) < 5
}

func (s *Service) recordLoginFailure(key string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	s.loginFails[key] = append(s.loginFails[key], time.Now())
}

func generateRandomHex(n int) string {
	bytes := make([]byte, n)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// --- Auth ---

type LoginResult struct {
	SessionToken string
	CSRFToken    string
	ExpiresAt    string
}

func (s *Service) Login(username, password, clientIP string) (*LoginResult, error) {
	rateKey := clientIP + ":" + username
	if !s.checkLoginRateLimit(rateKey) {
		_ = s.store.RecordAudit(store.AuditEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Actor:     username,
			Action:    "LOGIN_RATE_LIMITED",
			TargetIP:  clientIP,
			Success:   false,
		})
		return nil, errors.New(protocol.ErrRateLimited)
	}

	admin, err := s.store.GetAdminByUsername(username)
	if err != nil {
		s.recordLoginFailure(rateKey)
		_ = s.store.RecordAudit(store.AuditEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Actor:     username,
			Action:    "LOGIN_FAILED",
			TargetIP:  clientIP,
			Success:   false,
		})
		return nil, errors.New(protocol.ErrUnauthorized)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)); err != nil {
		s.recordLoginFailure(rateKey)
		_ = s.store.RecordAudit(store.AuditEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Actor:     username,
			Action:    "LOGIN_FAILED",
			TargetIP:  clientIP,
			Success:   false,
		})
		return nil, errors.New(protocol.ErrUnauthorized)
	}

	now := time.Now().UTC()
	expires := now.Add(8 * time.Hour).Format(time.RFC3339)
	token := "sess_" + generateRandomHex(24)
	csrf := "csrf_" + generateRandomHex(24)

	sess := store.Session{
		Token:        token,
		AdminID:      admin.ID,
		CSRFToken:    csrf,
		ExpiresAt:    expires,
		CreatedAt:    now.Format(time.RFC3339),
		LastActiveAt: now.Format(time.RFC3339),
	}

	if err := s.store.CreateSession(sess); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	_ = s.store.RecordAudit(store.AuditEntry{
		Timestamp: now.Format(time.RFC3339),
		Actor:     username,
		Action:    "LOGIN_SUCCESS",
		TargetIP:  clientIP,
		Success:   true,
	})

	return &LoginResult{
		SessionToken: token,
		CSRFToken:    csrf,
		ExpiresAt:    expires,
	}, nil
}

func (s *Service) Logout(sessionToken string) error {
	return s.store.DeleteSession(sessionToken)
}

func (s *Service) ChangePassword(adminID, oldPassword, newPassword string) error {
	// Re-verify old password
	row := s.store.DB().QueryRow(`SELECT password_hash FROM admins WHERE id = ?`, adminID)
	var currentHash string
	if err := row.Scan(&currentHash); err != nil {
		return errors.New(protocol.ErrUnauthorized)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(oldPassword)); err != nil {
		return errors.New(protocol.ErrUnauthorized)
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.store.UpdateAdminPassword(adminID, string(newHash), now); err != nil {
		return err
	}

	_ = s.store.RecordAudit(store.AuditEntry{
		Timestamp: now,
		Actor:     adminID,
		Action:    "CHANGE_PASSWORD",
		Success:   true,
	})

	return nil
}

func (s *Service) ValidateSession(token string) (*store.Session, error) {
	sess, err := s.store.GetSession(token)
	if err != nil {
		return nil, errors.New(protocol.ErrUnauthorized)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if sess.ExpiresAt < now {
		_ = s.store.DeleteSession(token)
		return nil, errors.New(protocol.ErrUnauthorized)
	}

	return sess, nil
}

// --- Targets Resolver ---

func (s *Service) ResolveTargets(targets protocol.BatchTargets) ([]string, error) {
	ipMap := make(map[string]struct{})
	for _, ip := range targets.IPs {
		clean := strings.TrimSpace(ip)
		if net.ParseIP(clean) == nil {
			return nil, errors.New(protocol.ErrInvalidArgument)
		}
		ipMap[clean] = struct{}{}
	}

	if targets.GroupID != nil && *targets.GroupID != "" {
		grp, err := s.store.GetGroup(*targets.GroupID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, errors.New(protocol.ErrNotFound)
			}
			return nil, err
		}
		for _, ip := range grp.IPList {
			ipMap[ip] = struct{}{}
		}
	}

	ips := make([]string, 0, len(ipMap))
	for ip := range ipMap {
		ips = append(ips, ip)
	}
	return ips, nil
}

// --- Clients ---

func (s *Service) ListClients(groupID string) ([]protocol.ClientNode, error) {
	return s.store.ListClients(groupID)
}

func (s *Service) GetClientByIP(ip string) (*protocol.ClientNode, error) {
	c, err := s.store.GetClientByIP(ip)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errors.New(protocol.ErrNotFound)
		}
		return nil, err
	}
	return c, nil
}

func (s *Service) UpdateClientSettings(ip string, autoRestore bool) error {
	err := s.store.UpdateClientSettings(ip, autoRestore)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New(protocol.ErrNotFound)
		}
		return err
	}
	return nil
}

func (s *Service) ImportMACs(reader io.Reader) (int, error) {
	csvReader := csv.NewReader(reader)
	records, err := csvReader.ReadAll()
	if err != nil {
		return 0, errors.New(protocol.ErrInvalidArgument)
	}

	count := 0
	for _, rec := range records {
		if len(rec) < 2 {
			continue
		}
		ip := strings.TrimSpace(rec[0])
		mac := strings.ToLower(strings.TrimSpace(rec[1]))
		if net.ParseIP(ip) == nil || len(mac) != 17 {
			continue
		}

		if err := s.store.UpsertMAC(ip, mac, protocol.MACSourceImport); err == nil {
			count++
		}
	}
	return count, nil
}

// --- Groups ---

func (s *Service) ListGroups() ([]protocol.IPGroup, error) {
	return s.store.ListGroups()
}

func (s *Service) CreateOrUpdateGroup(group protocol.IPGroup) error {
	if group.GroupID == "" {
		group.GroupID = "grp_" + generateRandomHex(6)
	}
	if group.CreatedAt == "" {
		group.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	// Validate IPs
	for _, ip := range group.IPList {
		if net.ParseIP(ip) == nil {
			return errors.New(protocol.ErrInvalidArgument)
		}
	}

	err := s.store.CreateOrUpdateGroup(group)
	if err != nil {
		if errors.Is(err, store.ErrIPInOtherGroup) {
			return errors.New(protocol.ErrIPInOtherGroup)
		}
		return err
	}
	return nil
}

func (s *Service) DeleteGroup(groupID string) error {
	err := s.store.DeleteGroup(groupID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New(protocol.ErrNotFound)
		}
		return err
	}
	return nil
}

// --- Power (WOL) ---

func (s *Service) WakeOnLAN(targets protocol.BatchTargets) ([]protocol.BatchResultItem, error) {
	ips, err := s.ResolveTargets(targets)
	if err != nil {
		return nil, err
	}

	results := make([]protocol.BatchResultItem, 0, len(ips))
	for _, ip := range ips {
		mac, _, err := s.store.GetMAC(ip)
		if err != nil || mac == nil || *mac == "" {
			results = append(results, protocol.BatchResultItem{
				IP: ip,
				OK: false,
				Error: &protocol.APIError{
					Code:    protocol.ErrMACUnknown,
					Message: fmt.Sprintf("MAC address unknown for IP %s", ip),
				},
			})
			continue
		}

		// Update status to WAKING_UP if client exists
		client, err := s.store.GetClientByIP(ip)
		if err == nil && client != nil {
			if protocol.IsValidClientTransition(client.Status, protocol.ClientStatusWakingUp) {
				client.Status = protocol.ClientStatusWakingUp
				_ = s.store.UpsertClient(*client)
			}
		}

		results = append(results, protocol.BatchResultItem{
			IP:    ip,
			OK:    true,
			Error: nil,
		})
	}
	return results, nil
}

// --- Restore Points ---

func (s *Service) ListRestorePoints(ip string) ([]protocol.RestorePoint, *string, error) {
	client, err := s.store.GetClientByIP(ip)
	if err != nil {
		return nil, nil, errors.New(protocol.ErrNotFound)
	}

	points, err := s.store.ListRestorePoints(client.ClientID)
	if err != nil {
		return nil, nil, err
	}
	return points, client.ActivePointID, nil
}

type RestorePointActionRequest struct {
	Action      string                `json:"action"`
	PointID     *string               `json:"point_id,omitempty"`
	Name        *string               `json:"name,omitempty"`
	Description *string               `json:"description,omitempty"`
	Targets     protocol.BatchTargets `json:"targets"`
}

func (s *Service) HandleRestorePointAction(req RestorePointActionRequest) ([]protocol.BatchResultItem, error) {
	if req.Action != "create" && req.Action != "switch" && req.Action != "delete" {
		return nil, errors.New(protocol.ErrInvalidArgument)
	}

	ips, err := s.ResolveTargets(req.Targets)
	if err != nil {
		return nil, err
	}

	results := make([]protocol.BatchResultItem, 0, len(ips))
	for _, ip := range ips {
		client, err := s.store.GetClientByIP(ip)
		if err != nil {
			results = append(results, protocol.BatchResultItem{
				IP: ip,
				OK: false,
				Error: &protocol.APIError{
					Code:    protocol.ErrNotFound,
					Message: fmt.Sprintf("client not found for IP %s", ip),
				},
			})
			continue
		}

		// Validate capability
		if strings.Contains(strings.ToLower(client.OSEdition), "home") {
			results = append(results, protocol.BatchResultItem{
				IP: ip,
				OK: false,
				Error: &protocol.APIError{
					Code:    protocol.ErrCapabilityUnsupported,
					Message: "Home edition does not support native VHDX restore points",
				},
			})
			continue
		}

		// Execute action
		switch req.Action {
		case "create":
			points, _ := s.store.ListRestorePoints(client.ClientID)
			if len(points) >= 5 {
				results = append(results, protocol.BatchResultItem{
					IP: ip,
					OK: false,
					Error: &protocol.APIError{
						Code:    protocol.ErrRestorePointLimit,
						Message: "restore point depth limit of 5 reached",
					},
				})
				continue
			}

			name := "還原點"
			if req.Name != nil && *req.Name != "" {
				name = *req.Name
			}
			desc := ""
			if req.Description != nil {
				desc = *req.Description
			}

			newPID := "rp_" + generateRandomHex(8)
			now := time.Now().UTC().Format(time.RFC3339)
			newRP := protocol.RestorePoint{
				PointID:     newPID,
				Name:        name,
				Description: desc,
				VHDXFile:    newPID + ".vhdx",
				ParentID:    client.ActivePointID,
				Depth:       len(points),
				SizeBytes:   0,
				IsBase:      false,
				IsActive:    true,
				State:       protocol.RestorePointStateReady,
				CreatedBy:   protocol.CreatedByAdminRemote,
				CreatedAt:   now,
			}
			points = append(points, newRP)
			_ = s.store.SaveRestorePoints(client.ClientID, points)
			results = append(results, protocol.BatchResultItem{IP: ip, OK: true})

		case "switch":
			if req.PointID == nil || *req.PointID == "" {
				results = append(results, protocol.BatchResultItem{
					IP: ip,
					OK: false,
					Error: &protocol.APIError{
						Code:    protocol.ErrInvalidArgument,
						Message: "point_id required for switch",
					},
				})
				continue
			}
			err := s.store.SetActiveRestorePoint(client.ClientID, *req.PointID)
			if err != nil {
				results = append(results, protocol.BatchResultItem{
					IP: ip,
					OK: false,
					Error: &protocol.APIError{
						Code:    protocol.ErrNotFound,
						Message: "restore point not found",
					},
				})
			} else {
				results = append(results, protocol.BatchResultItem{IP: ip, OK: true})
			}

		case "delete":
			if req.PointID == nil || *req.PointID == "" {
				results = append(results, protocol.BatchResultItem{
					IP: ip,
					OK: false,
					Error: &protocol.APIError{
						Code:    protocol.ErrInvalidArgument,
						Message: "point_id required for delete",
					},
				})
				continue
			}
			if client.ActivePointID != nil && *client.ActivePointID == *req.PointID {
				results = append(results, protocol.BatchResultItem{
					IP: ip,
					OK: false,
					Error: &protocol.APIError{
						Code:    protocol.ErrInvalidState,
						Message: "cannot delete active restore point",
					},
				})
				continue
			}
			// Delete logic
			points, _ := s.store.ListRestorePoints(client.ClientID)
			var updated []protocol.RestorePoint
			found := false
			for _, p := range points {
				if p.PointID == *req.PointID {
					found = true
					continue
				}
				updated = append(updated, p)
			}
			if !found {
				results = append(results, protocol.BatchResultItem{
					IP: ip,
					OK: false,
					Error: &protocol.APIError{
						Code:    protocol.ErrNotFound,
						Message: "restore point not found",
					},
				})
			} else {
				_ = s.store.SaveRestorePoints(client.ClientID, updated)
				results = append(results, protocol.BatchResultItem{IP: ip, OK: true})
			}
		}
	}
	return results, nil
}

// --- Deploy Tasks ---

func (s *Service) StartDeploy(masterIP string, targetIPs []string) (*protocol.DeployTask, error) {
	if net.ParseIP(masterIP) == nil || len(targetIPs) == 0 {
		return nil, errors.New(protocol.ErrInvalidArgument)
	}

	// Check if active deploy task exists
	existingTasks, err := s.store.ListTasks(10, 0)
	if err == nil {
		for _, t := range existingTasks {
			if t.State != protocol.TaskStateSucceeded &&
				t.State != protocol.TaskStateFailed &&
				t.State != protocol.TaskStateCancelled {
				return nil, errors.New(protocol.ErrDeployInProgress)
			}
		}
	}

	// Pre-conditions check: PE ready on master
	master, err := s.store.GetClientByIP(masterIP)
	if err != nil {
		return nil, errors.New(protocol.ErrNotFound)
	}
	if !master.PEReady {
		return nil, errors.New(protocol.ErrPENotReady)
	}

	// Check PE ready on targets
	for _, tip := range targetIPs {
		target, err := s.store.GetClientByIP(tip)
		if err != nil {
			return nil, errors.New(protocol.ErrNotFound)
		}
		if !target.PEReady {
			return nil, errors.New(protocol.ErrPENotReady)
		}
	}

	taskID := "task_" + generateRandomHex(6)
	now := time.Now().UTC().Format(time.RFC3339)
	task := protocol.DeployTask{
		TaskID:              taskID,
		State:               protocol.TaskStatePendingPrepare,
		MasterIP:            masterIP,
		TargetIPs:           targetIPs,
		ImageSHA256:         "mock_sha256_master",
		ImageBytes:          32212254720,
		ImagePath:           "base.vhdx",
		ResetsRestorePoints: true,
		CreatedAt:           now,
	}

	if err := s.store.CreateTask(task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *Service) CancelDeploy(taskID string) error {
	task, err := s.store.GetTask(taskID)
	if err != nil {
		return errors.New(protocol.ErrNotFound)
	}

	if !protocol.IsValidTaskTransition(task.State, protocol.TaskStateCancelled) {
		return errors.New(protocol.ErrInvalidState)
	}

	task.State = protocol.TaskStateCancelled
	now := time.Now().UTC().Format(time.RFC3339)
	task.FinishedAt = &now
	return s.store.UpdateTask(*task)
}

func (s *Service) RetryDeploy(taskID string) error {
	task, err := s.store.GetTask(taskID)
	if err != nil {
		return errors.New(protocol.ErrNotFound)
	}

	if task.State != protocol.TaskStateFailed {
		return errors.New(protocol.ErrInvalidState)
	}

	task.State = protocol.TaskStatePendingPrepare
	task.ErrorCode = nil
	task.ErrorMessage = nil
	task.FinishedAt = nil
	return s.store.UpdateTask(*task)
}

func (s *Service) GetTask(taskID string) (*protocol.DeployTask, error) {
	task, err := s.store.GetTask(taskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errors.New(protocol.ErrNotFound)
		}
		return nil, err
	}
	return task, nil
}

func (s *Service) ListTasks(limit, offset int) ([]protocol.DeployTask, error) {
	return s.store.ListTasks(limit, offset)
}

// --- Audit & Agent ---

func (s *Service) ListAudit(limit, offset int) ([]store.AuditEntry, error) {
	return s.store.ListAudit(limit, offset)
}

func (s *Service) AgentEnroll(token string, clientIP string) (string, error) {
	if token == "" {
		return "", errors.New(protocol.ErrUnauthorized)
	}
	// Return a simulated client certificate PEM string
	cert := fmt.Sprintf("-----BEGIN CERTIFICATE-----\nMIIB...fake_cert_for_%s...\n-----END CERTIFICATE-----", clientIP)
	return cert, nil
}

func (s *Service) AgentHeartbeat(node protocol.ClientNode) error {
	node.LastSeen = time.Now().UTC().Format(time.RFC3339)
	return s.store.UpsertClient(node)
}
