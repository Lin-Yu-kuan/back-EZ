package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/owner/back-ez/internal/protocol"
)

// Server handles REST API routing and HTTP middleware.
type Server struct {
	service *Service
	mux     *http.ServeMux
}

func NewServer(svc *Service) *Server {
	s := &Server{
		service: svc,
		mux:     http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.authMiddleware(s.mux)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	resp := map[string]any{
		"ok":   true,
		"data": data,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func writeError(w http.ResponseWriter, code, message string, details any) {
	status := http.StatusInternalServerError
	switch code {
	case protocol.ErrUnauthorized:
		status = http.StatusUnauthorized
	case protocol.ErrForbidden:
		status = http.StatusForbidden
	case protocol.ErrRateLimited:
		status = http.StatusTooManyRequests
	case protocol.ErrInvalidArgument:
		status = http.StatusBadRequest
	case protocol.ErrNotFound:
		status = http.StatusNotFound
	case protocol.ErrInvalidState,
		protocol.ErrIPInOtherGroup,
		protocol.ErrRestorePointLimit,
		protocol.ErrDeployInProgress,
		protocol.ErrMACUnknown,
		protocol.ErrPENotReady,
		protocol.ErrCapabilityUnsupported:
		status = http.StatusConflict
	case protocol.ErrImageVerifyFailed:
		status = http.StatusUnprocessableEntity
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	resp := map[string]any{
		"ok": false,
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// authMiddleware enforces Session Cookie & CSRF validation.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Public endpoints that bypass session auth
		if path == "/api/v1/auth/login" || path == "/api/v1/agent/enroll" || path == "/ws/agent" {
			next.ServeHTTP(w, r)
			return
		}

		// Check Session Cookie
		cookie, err := r.Cookie("backez_session")
		if err != nil || cookie.Value == "" {
			writeError(w, protocol.ErrUnauthorized, "authentication required", nil)
			return
		}

		sess, err := s.service.ValidateSession(cookie.Value)
		if err != nil {
			writeError(w, protocol.ErrUnauthorized, "invalid or expired session", nil)
			return
		}

		// Enforce CSRF header check for mutating methods
		if r.Method == http.MethodPost || r.Method == http.MethodPatch ||
			r.Method == http.MethodDelete || r.Method == http.MethodPut {
			csrfHeader := r.Header.Get("X-CSRF-Token")
			if csrfHeader == "" || csrfHeader != sess.CSRFToken {
				writeError(w, protocol.ErrForbidden, "missing or invalid CSRF token", nil)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) registerRoutes() {
	// Auth
	s.mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	s.mux.HandleFunc("POST /api/v1/auth/password", s.handlePasswordChange)

	// Clients & Groups
	s.mux.HandleFunc("GET /api/v1/clients", s.handleListClients)
	s.mux.HandleFunc("GET /api/v1/clients/{ip}", s.handleGetClient)
	s.mux.HandleFunc("PATCH /api/v1/clients/{ip}/settings", s.handlePatchClientSettings)
	s.mux.HandleFunc("POST /api/v1/clients/macs/import", s.handleImportMACs)

	s.mux.HandleFunc("GET /api/v1/groups", s.handleListGroups)
	s.mux.HandleFunc("POST /api/v1/groups", s.handleCreateOrUpdateGroup)
	s.mux.HandleFunc("DELETE /api/v1/groups/{group_id}", s.handleDeleteGroup)

	// Power & Restore Points
	s.mux.HandleFunc("POST /api/v1/power/wol", s.handleWOL)
	s.mux.HandleFunc("GET /api/v1/clients/{ip}/restore-points", s.handleListRestorePoints)
	s.mux.HandleFunc("POST /api/v1/restore-points/action", s.handleRestorePointsAction)

	// Deploy Tasks
	s.mux.HandleFunc("POST /api/v1/deploy/start", s.handleDeployStart)
	s.mux.HandleFunc("POST /api/v1/deploy/cancel", s.handleDeployCancel)
	s.mux.HandleFunc("POST /api/v1/deploy/retry", s.handleDeployRetry)
	s.mux.HandleFunc("GET /api/v1/tasks", s.handleListTasks)
	s.mux.HandleFunc("GET /api/v1/tasks/{task_id}", s.handleGetTask)

	// Audit & Agent
	s.mux.HandleFunc("GET /api/v1/audit", s.handleListAudit)
	s.mux.HandleFunc("POST /api/v1/agent/enroll", s.handleAgentEnroll)
	s.mux.HandleFunc("POST /api/v1/agent/heartbeat", s.handleAgentHeartbeat)
}

// RegisterWS registers external WebSocket handler functions for /ws and /ws/agent.
func (s *Server) RegisterWS(uiHandler, agentHandler http.HandlerFunc) {
	s.mux.HandleFunc("/ws", uiHandler)
	s.mux.HandleFunc("/ws/agent", agentHandler)
}

// --- Handler Implementations ---

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}
	if body.Username == "" {
		body.Username = "admin"
	}

	clientIP := r.RemoteAddr
	if host, _, err := strings.Cut(r.RemoteAddr, ":"); err && host != "" {
		clientIP = host
	}

	res, err := s.service.Login(body.Username, body.Password, clientIP)
	if err != nil {
		code := err.Error()
		if code != protocol.ErrRateLimited && code != protocol.ErrUnauthorized {
			code = protocol.ErrUnauthorized
		}
		writeError(w, code, "login failed", nil)
		return
	}

	// Set HttpOnly, Secure, SameSite=Strict Session Cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "backez_session",
		Value:    res.SessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"csrf_token": res.CSRFToken,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("backez_session"); err == nil {
		_ = s.service.Logout(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "backez_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"logged_out": true})
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}

	cookie, _ := r.Cookie("backez_session")
	sess, _ := s.service.ValidateSession(cookie.Value)

	if err := s.service.ChangePassword(sess.AdminID, body.OldPassword, body.NewPassword); err != nil {
		writeError(w, err.Error(), "password change failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (s *Server) handleListClients(w http.ResponseWriter, r *http.Request) {
	groupID := r.URL.Query().Get("group_id")
	clients, err := s.service.ListClients(groupID)
	if err != nil {
		writeError(w, protocol.ErrInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, clients)
}

func (s *Server) handleGetClient(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	c, err := s.service.GetClientByIP(ip)
	if err != nil {
		writeError(w, err.Error(), "client not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handlePatchClientSettings(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	var body struct {
		AutoRestoreOnReboot bool `json:"auto_restore_on_reboot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}

	if err := s.service.UpdateClientSettings(ip, body.AutoRestoreOnReboot); err != nil {
		writeError(w, err.Error(), "failed to update settings", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (s *Server) handleImportMACs(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		// Try raw body if not multipart
		if r.Body != nil {
			count, err := s.service.ImportMACs(r.Body)
			if err != nil {
				writeError(w, err.Error(), "failed to import macs", nil)
				return
			}
			writeJSON(w, http.StatusOK, map[string]int{"imported": count})
			return
		}
		writeError(w, protocol.ErrInvalidArgument, "missing file", nil)
		return
	}
	defer file.Close()

	count, err := s.service.ImportMACs(file)
	if err != nil {
		writeError(w, err.Error(), "failed to import macs", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"imported": count})
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.service.ListGroups()
	if err != nil {
		writeError(w, protocol.ErrInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) handleCreateOrUpdateGroup(w http.ResponseWriter, r *http.Request) {
	var g protocol.IPGroup
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}

	if err := s.service.CreateOrUpdateGroup(g); err != nil {
		writeError(w, err.Error(), "failed to save group", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	groupID := r.PathValue("group_id")
	if err := s.service.DeleteGroup(groupID); err != nil {
		writeError(w, err.Error(), "failed to delete group", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) handleWOL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Targets protocol.BatchTargets `json:"targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}

	results, err := s.service.WakeOnLAN(body.Targets)
	if err != nil {
		writeError(w, err.Error(), "failed to wake targets", nil)
		return
	}
	writeJSON(w, http.StatusOK, protocol.BatchResponseData{Results: results})
}

func (s *Server) handleListRestorePoints(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	points, activePID, err := s.service.ListRestorePoints(ip)
	if err != nil {
		writeError(w, err.Error(), "failed to list restore points", nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active_point_id": activePID,
		"restore_points":  points,
	})
}

func (s *Server) handleRestorePointsAction(w http.ResponseWriter, r *http.Request) {
	var req RestorePointActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}

	results, err := s.service.HandleRestorePointAction(req)
	if err != nil {
		writeError(w, err.Error(), "failed to execute restore point action", nil)
		return
	}
	writeJSON(w, http.StatusOK, protocol.BatchResponseData{Results: results})
}

func (s *Server) handleDeployStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MasterIP  string   `json:"master_ip"`
		TargetIPs []string `json:"target_ips"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}

	task, err := s.service.StartDeploy(body.MasterIP, body.TargetIPs)
	if err != nil {
		writeError(w, err.Error(), "failed to start deploy", nil)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleDeployCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}

	if err := s.service.CancelDeploy(body.TaskID); err != nil {
		writeError(w, err.Error(), "failed to cancel deploy", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

func (s *Server) handleDeployRetry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}

	if err := s.service.RetryDeploy(body.TaskID); err != nil {
		writeError(w, err.Error(), "failed to retry deploy", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"retried": true})
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	tasks, err := s.service.ListTasks(limit, offset)
	if err != nil {
		writeError(w, protocol.ErrInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	task, err := s.service.GetTask(taskID)
	if err != nil {
		writeError(w, err.Error(), "task not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	entries, err := s.service.ListAudit(limit, offset)
	if err != nil {
		writeError(w, protocol.ErrInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleAgentEnroll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}

	cert, err := s.service.AgentEnroll(body.Token, r.RemoteAddr)
	if err != nil {
		writeError(w, err.Error(), "enrollment failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"client_certificate": cert,
	})
}

func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	var node protocol.ClientNode
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		writeError(w, protocol.ErrInvalidArgument, "invalid request body", nil)
		return
	}

	if err := s.service.AgentHeartbeat(node); err != nil {
		writeError(w, protocol.ErrInternal, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"acknowledged": true})
}
