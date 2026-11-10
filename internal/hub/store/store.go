package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/owner/back-ez/internal/protocol"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound       = errors.New(protocol.ErrNotFound)
	ErrIPInOtherGroup = errors.New(protocol.ErrIPInOtherGroup)
)

// AdminUser represents a dashboard admin record.
type AdminUser struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    string
	UpdatedAt    string
}

// Session represents an authenticated admin session.
type Session struct {
	Token        string
	AdminID      string
	CSRFToken    string
	ExpiresAt    string
	CreatedAt    string
	LastActiveAt string
}

// AuditEntry represents an audit log record.
type AuditEntry struct {
	ID             int64  `json:"id"`
	Timestamp      string `json:"timestamp"`
	Actor          string `json:"actor"`
	Action         string `json:"action"`
	TargetIP       string `json:"target_ip,omitempty"`
	TargetClientID string `json:"target_client_id,omitempty"`
	Details        string `json:"details,omitempty"`
	Success        bool   `json:"success"`
}

// Store encapsulates the SQLite database connection with a write lock.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// Open initializes SQLite connection with WAL mode and runs migrations.
func Open(dbPath string) (*Store, error) {
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB for testing/inspecting.
func (s *Store) DB() *sql.DB {
	return s.db
}

// --- Admin & Sessions ---

func (s *Store) CreateAdmin(user AdminUser) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
INSERT INTO admins (id, username, password_hash, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
`, user.ID, user.Username, user.PasswordHash, user.CreatedAt, user.UpdatedAt)
	return err
}

func (s *Store) GetAdminByUsername(username string) (*AdminUser, error) {
	row := s.db.QueryRow(`
SELECT id, username, password_hash, created_at, updated_at
FROM admins
WHERE username = ?
`, username)

	var u AdminUser
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) UpdateAdminPassword(adminID, newHash, updatedAt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`
UPDATE admins
SET password_hash = ?, updated_at = ?
WHERE id = ?
`, newHash, updatedAt, adminID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateSession(sess Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
INSERT INTO sessions (token, admin_id, csrf_token, expires_at, created_at, last_active_at)
VALUES (?, ?, ?, ?, ?, ?)
`, sess.Token, sess.AdminID, sess.CSRFToken, sess.ExpiresAt, sess.CreatedAt, sess.LastActiveAt)
	return err
}

func (s *Store) GetSession(token string) (*Session, error) {
	row := s.db.QueryRow(`
SELECT token, admin_id, csrf_token, expires_at, created_at, last_active_at
FROM sessions
WHERE token = ?
`, token)

	var sess Session
	if err := row.Scan(&sess.Token, &sess.AdminID, &sess.CSRFToken, &sess.ExpiresAt, &sess.CreatedAt, &sess.LastActiveAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &sess, nil
}

func (s *Store) DeleteSession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (s *Store) DeleteExpiredSessions(now string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now)
	return err
}

// --- IP Groups ---

func (s *Store) CreateOrUpdateGroup(group protocol.IPGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Verify no IP belongs to another group
	for _, ip := range group.IPList {
		var existingGroupID string
		row := tx.QueryRow(`SELECT group_id FROM group_ips WHERE ip = ?`, ip)
		if err := row.Scan(&existingGroupID); err == nil {
			if existingGroupID != group.GroupID {
				return ErrIPInOtherGroup
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}

	// Insert or replace group
	_, err = tx.Exec(`
INSERT INTO ip_groups (group_id, group_name, created_at)
VALUES (?, ?, ?)
ON CONFLICT(group_id) DO UPDATE SET group_name = excluded.group_name
`, group.GroupID, group.GroupName, group.CreatedAt)
	if err != nil {
		return err
	}

	// Clear existing IPs for this group
	if _, err := tx.Exec(`DELETE FROM group_ips WHERE group_id = ?`, group.GroupID); err != nil {
		return err
	}

	// Insert group IPs
	for _, ip := range group.IPList {
		_, err := tx.Exec(`INSERT INTO group_ips (group_id, ip) VALUES (?, ?)`, group.GroupID, ip)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return ErrIPInOtherGroup
			}
			return err
		}
	}

	// Update clients group_id association
	_, _ = tx.Exec(`UPDATE clients SET group_id = NULL WHERE group_id = ?`, group.GroupID)
	for _, ip := range group.IPList {
		_, _ = tx.Exec(`UPDATE clients SET group_id = ? WHERE current_ip = ?`, group.GroupID, ip)
	}

	return tx.Commit()
}

func (s *Store) GetGroup(groupID string) (*protocol.IPGroup, error) {
	row := s.db.QueryRow(`SELECT group_id, group_name, created_at FROM ip_groups WHERE group_id = ?`, groupID)
	var g protocol.IPGroup
	if err := row.Scan(&g.GroupID, &g.GroupName, &g.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	rows, err := s.db.Query(`SELECT ip FROM group_ips WHERE group_id = ? ORDER BY ip ASC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	g.IPList = make([]string, 0)
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, err
		}
		g.IPList = append(g.IPList, ip)
	}
	return &g, nil
}

func (s *Store) ListGroups() ([]protocol.IPGroup, error) {
	rows, err := s.db.Query(`SELECT group_id, group_name, created_at FROM ip_groups ORDER BY group_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []protocol.IPGroup
	for rows.Next() {
		var g protocol.IPGroup
		if err := rows.Scan(&g.GroupID, &g.GroupName, &g.CreatedAt); err != nil {
			return nil, err
		}

		ipRows, err := s.db.Query(`SELECT ip FROM group_ips WHERE group_id = ? ORDER BY ip ASC`, g.GroupID)
		if err != nil {
			return nil, err
		}
		g.IPList = make([]string, 0)
		for ipRows.Next() {
			var ip string
			if err := ipRows.Scan(&ip); err == nil {
				g.IPList = append(g.IPList, ip)
			}
		}
		ipRows.Close()

		groups = append(groups, g)
	}
	if groups == nil {
		groups = make([]protocol.IPGroup, 0)
	}
	return groups, nil
}

func (s *Store) DeleteGroup(groupID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`DELETE FROM ip_groups WHERE group_id = ?`, groupID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetGroupByIP(ip string) (*protocol.IPGroup, error) {
	row := s.db.QueryRow(`SELECT group_id FROM group_ips WHERE ip = ?`, ip)
	var groupID string
	if err := row.Scan(&groupID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return s.GetGroup(groupID)
}

// --- Clients ---

func (s *Store) UpsertClient(c protocol.ClientNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Auto associate group_id by current_ip if not given
	if c.GroupID == nil {
		var gid string
		row := tx.QueryRow(`SELECT group_id FROM group_ips WHERE ip = ?`, c.CurrentIP)
		if err := row.Scan(&gid); err == nil {
			c.GroupID = &gid
		}
	}

	peReadyInt := 0
	if c.PEReady {
		peReadyInt = 1
	}
	autoRestoreInt := 0
	if c.AutoRestoreOnReboot {
		autoRestoreInt = 1
	}

	var macSourceStr *string
	if c.MACSource != nil {
		str := string(*c.MACSource)
		macSourceStr = &str
	}

	_, err = tx.Exec(`
INSERT INTO clients (
    client_id, current_ip, mac_address, mac_source, hostname, os_edition,
    agent_version, pe_ready, group_id, status, auto_restore_on_reboot,
    active_point_id, last_task_id, progress_percent, error_message, last_seen
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(client_id) DO UPDATE SET
    current_ip = excluded.current_ip,
    mac_address = COALESCE(excluded.mac_address, clients.mac_address),
    mac_source = COALESCE(excluded.mac_source, clients.mac_source),
    hostname = excluded.hostname,
    os_edition = excluded.os_edition,
    agent_version = excluded.agent_version,
    pe_ready = excluded.pe_ready,
    group_id = COALESCE(excluded.group_id, clients.group_id),
    status = excluded.status,
    auto_restore_on_reboot = excluded.auto_restore_on_reboot,
    active_point_id = COALESCE(excluded.active_point_id, clients.active_point_id),
    last_task_id = excluded.last_task_id,
    progress_percent = excluded.progress_percent,
    error_message = excluded.error_message,
    last_seen = excluded.last_seen
`, c.ClientID, c.CurrentIP, c.MACAddress, macSourceStr, c.Hostname, c.OSEdition,
		c.AgentVersion, peReadyInt, c.GroupID, string(c.Status), autoRestoreInt,
		c.ActivePointID, c.LastTaskID, c.ProgressPercent, c.ErrorMessage, c.LastSeen)
	if err != nil {
		return err
	}

	// Update MAC cache if mac_address is provided
	if c.MACAddress != nil && *c.MACAddress != "" {
		source := string(protocol.MACSourceAgent)
		if c.MACSource != nil {
			source = string(*c.MACSource)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		_, _ = tx.Exec(`
INSERT INTO mac_cache (ip, mac_address, source, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(ip) DO UPDATE SET mac_address = excluded.mac_address, source = excluded.source, updated_at = excluded.updated_at
`, c.CurrentIP, *c.MACAddress, source, now)
	}

	return tx.Commit()
}

func (s *Store) populateClientSubResources(c *protocol.ClientNode) error {
	// Restore Point IDs
	rpRows, err := s.db.Query(`SELECT point_id FROM restore_points WHERE client_id = ? ORDER BY depth ASC, created_at ASC`, c.ClientID)
	if err == nil {
		defer rpRows.Close()
		c.RestorePointIDs = make([]string, 0)
		for rpRows.Next() {
			var pid string
			if err := rpRows.Scan(&pid); err == nil {
				c.RestorePointIDs = append(c.RestorePointIDs, pid)
			}
		}
	}

	// Network Snapshot
	var (
		mac        string
		mode       string
		ip         string
		mask       string
		gw         string
		dnsJSON    string
		origHost   string
		capturedAt string
	)
	row := s.db.QueryRow(`
SELECT mac_address, ip_mode, ip_address, subnet_mask, default_gateway, dns_servers, original_hostname, captured_at
FROM network_identities
WHERE client_id = ?
`, c.ClientID)
	if err := row.Scan(&mac, &mode, &ip, &mask, &gw, &dnsJSON, &origHost, &capturedAt); err == nil {
		var dns []string
		_ = json.Unmarshal([]byte(dnsJSON), &dns)
		c.NetworkSnapshot = protocol.NetworkSnapshot{
			Identity: &protocol.NetworkIdentity{
				MACAddress:       mac,
				IPMode:           protocol.IPMode(mode),
				IPAddress:        ip,
				SubnetMask:       mask,
				DefaultGateway:   gw,
				DNSServers:       dns,
				OriginalHostname: origHost,
				CapturedAt:       capturedAt,
			},
		}
	} else {
		c.NetworkSnapshot = protocol.NetworkSnapshot{Identity: nil}
	}

	return nil
}

func (s *Store) GetClient(clientID string) (*protocol.ClientNode, error) {
	row := s.db.QueryRow(`
SELECT client_id, current_ip, mac_address, mac_source, hostname, os_edition,
       agent_version, pe_ready, group_id, status, auto_restore_on_reboot,
       active_point_id, last_task_id, progress_percent, error_message, last_seen
FROM clients
WHERE client_id = ?
`, clientID)

	var (
		c              protocol.ClientNode
		peReadyInt     int
		autoRestoreInt int
		statusStr      string
		macSourceStr   *string
	)

	err := row.Scan(
		&c.ClientID, &c.CurrentIP, &c.MACAddress, &macSourceStr, &c.Hostname, &c.OSEdition,
		&c.AgentVersion, &peReadyInt, &c.GroupID, &statusStr, &autoRestoreInt,
		&c.ActivePointID, &c.LastTaskID, &c.ProgressPercent, &c.ErrorMessage, &c.LastSeen,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	c.PEReady = (peReadyInt == 1)
	c.AutoRestoreOnReboot = (autoRestoreInt == 1)
	c.Status = protocol.ClientStatus(statusStr)
	if macSourceStr != nil {
		src := protocol.MACSource(*macSourceStr)
		c.MACSource = &src
	}

	if err := s.populateClientSubResources(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) GetClientByIP(ip string) (*protocol.ClientNode, error) {
	row := s.db.QueryRow(`SELECT client_id FROM clients WHERE current_ip = ?`, ip)
	var clientID string
	if err := row.Scan(&clientID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return s.GetClient(clientID)
}

func (s *Store) ListClients(groupID string) ([]protocol.ClientNode, error) {
	var (
		rows *sql.Rows
		err  error
	)

	if groupID != "" {
		rows, err = s.db.Query(`
SELECT client_id, current_ip, mac_address, mac_source, hostname, os_edition,
       agent_version, pe_ready, group_id, status, auto_restore_on_reboot,
       active_point_id, last_task_id, progress_percent, error_message, last_seen
FROM clients
WHERE group_id = ?
ORDER BY current_ip ASC
`, groupID)
	} else {
		rows, err = s.db.Query(`
SELECT client_id, current_ip, mac_address, mac_source, hostname, os_edition,
       agent_version, pe_ready, group_id, status, auto_restore_on_reboot,
       active_point_id, last_task_id, progress_percent, error_message, last_seen
FROM clients
ORDER BY current_ip ASC
`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []protocol.ClientNode
	for rows.Next() {
		var (
			c              protocol.ClientNode
			peReadyInt     int
			autoRestoreInt int
			statusStr      string
			macSourceStr   *string
		)

		err := rows.Scan(
			&c.ClientID, &c.CurrentIP, &c.MACAddress, &macSourceStr, &c.Hostname, &c.OSEdition,
			&c.AgentVersion, &peReadyInt, &c.GroupID, &statusStr, &autoRestoreInt,
			&c.ActivePointID, &c.LastTaskID, &c.ProgressPercent, &c.ErrorMessage, &c.LastSeen,
		)
		if err != nil {
			return nil, err
		}

		c.PEReady = (peReadyInt == 1)
		c.AutoRestoreOnReboot = (autoRestoreInt == 1)
		c.Status = protocol.ClientStatus(statusStr)
		if macSourceStr != nil {
			src := protocol.MACSource(*macSourceStr)
			c.MACSource = &src
		}

		if err := s.populateClientSubResources(&c); err != nil {
			return nil, err
		}

		clients = append(clients, c)
	}
	if clients == nil {
		clients = make([]protocol.ClientNode, 0)
	}
	return clients, nil
}

func (s *Store) UpdateClientSettings(ip string, autoRestore bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	autoRestoreInt := 0
	if autoRestore {
		autoRestoreInt = 1
	}

	res, err := s.db.Exec(`UPDATE clients SET auto_restore_on_reboot = ? WHERE current_ip = ?`, autoRestoreInt, ip)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Restore Points ---

func (s *Store) SaveRestorePoints(clientID string, points []protocol.RestorePoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Clear existing points for this client
	if _, err := tx.Exec(`DELETE FROM restore_points WHERE client_id = ?`, clientID); err != nil {
		return err
	}

	var activePID *string
	for _, rp := range points {
		isBaseInt := 0
		if rp.IsBase {
			isBaseInt = 1
		}
		isActiveInt := 0
		if rp.IsActive {
			isActiveInt = 1
			activePID = &rp.PointID
		}

		_, err := tx.Exec(`
INSERT INTO restore_points (
    point_id, client_id, name, description, vhdx_file, parent_id, depth,
    size_bytes, is_base, is_active, state, created_by, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, rp.PointID, clientID, rp.Name, rp.Description, rp.VHDXFile, rp.ParentID, rp.Depth,
			rp.SizeBytes, isBaseInt, isActiveInt, string(rp.State), string(rp.CreatedBy), rp.CreatedAt)
		if err != nil {
			return err
		}
	}

	if activePID != nil {
		_, _ = tx.Exec(`UPDATE clients SET active_point_id = ? WHERE client_id = ?`, *activePID, clientID)
	}

	return tx.Commit()
}

func (s *Store) ListRestorePoints(clientID string) ([]protocol.RestorePoint, error) {
	rows, err := s.db.Query(`
SELECT point_id, name, description, vhdx_file, parent_id, depth,
       size_bytes, is_base, is_active, state, created_by, created_at
FROM restore_points
WHERE client_id = ?
ORDER BY depth ASC, created_at ASC
`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []protocol.RestorePoint
	for rows.Next() {
		var (
			rp          protocol.RestorePoint
			isBaseInt   int
			isActiveInt int
			stateStr    string
			createdStr  string
		)
		err := rows.Scan(
			&rp.PointID, &rp.Name, &rp.Description, &rp.VHDXFile, &rp.ParentID, &rp.Depth,
			&rp.SizeBytes, &isBaseInt, &isActiveInt, &stateStr, &createdStr, &rp.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		rp.IsBase = (isBaseInt == 1)
		rp.IsActive = (isActiveInt == 1)
		rp.State = protocol.RestorePointState(stateStr)
		rp.CreatedBy = protocol.CreatedBy(createdStr)
		points = append(points, rp)
	}
	if points == nil {
		points = make([]protocol.RestorePoint, 0)
	}
	return points, nil
}

func (s *Store) SetActiveRestorePoint(clientID string, pointID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`UPDATE restore_points SET is_active = 0 WHERE client_id = ?`, clientID)
	if err != nil {
		return err
	}

	res, err = tx.Exec(`UPDATE restore_points SET is_active = 1 WHERE client_id = ? AND point_id = ?`, clientID, pointID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}

	_, _ = tx.Exec(`UPDATE clients SET active_point_id = ? WHERE client_id = ?`, pointID, clientID)
	return tx.Commit()
}

// --- Network Identity ---

func (s *Store) SaveNetworkIdentity(clientID string, id protocol.NetworkIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dnsJSON, err := json.Marshal(id.DNSServers)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
INSERT INTO network_identities (
    client_id, mac_address, ip_mode, ip_address, subnet_mask,
    default_gateway, dns_servers, original_hostname, captured_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(client_id) DO UPDATE SET
    mac_address = excluded.mac_address,
    ip_mode = excluded.ip_mode,
    ip_address = excluded.ip_address,
    subnet_mask = excluded.subnet_mask,
    default_gateway = excluded.default_gateway,
    dns_servers = excluded.dns_servers,
    original_hostname = excluded.original_hostname,
    captured_at = excluded.captured_at
`, clientID, id.MACAddress, string(id.IPMode), id.IPAddress, id.SubnetMask,
		id.DefaultGateway, string(dnsJSON), id.OriginalHostname, id.CapturedAt)
	return err
}

func (s *Store) GetNetworkIdentity(clientID string) (*protocol.NetworkIdentity, error) {
	row := s.db.QueryRow(`
SELECT mac_address, ip_mode, ip_address, subnet_mask, default_gateway, dns_servers, original_hostname, captured_at
FROM network_identities
WHERE client_id = ?
`, clientID)

	var (
		id         protocol.NetworkIdentity
		mode       string
		dnsJSON    string
		capturedAt string
	)
	err := row.Scan(&id.MACAddress, &mode, &id.IPAddress, &id.SubnetMask, &id.DefaultGateway, &dnsJSON, &id.OriginalHostname, &capturedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	id.IPMode = protocol.IPMode(mode)
	id.CapturedAt = capturedAt
	_ = json.Unmarshal([]byte(dnsJSON), &id.DNSServers)
	return &id, nil
}

// --- MAC Cache ---

func (s *Store) UpsertMAC(ip, mac string, source protocol.MACSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
INSERT INTO mac_cache (ip, mac_address, source, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(ip) DO UPDATE SET mac_address = excluded.mac_address, source = excluded.source, updated_at = excluded.updated_at
`, ip, mac, string(source), now)
	return err
}

func (s *Store) GetMAC(ip string) (*string, *protocol.MACSource, error) {
	row := s.db.QueryRow(`SELECT mac_address, source FROM mac_cache WHERE ip = ?`, ip)
	var mac, src string
	if err := row.Scan(&mac, &src); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	source := protocol.MACSource(src)
	return &mac, &source, nil
}

// --- Deploy Tasks ---

func (s *Store) CreateTask(task protocol.DeployTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	targetJSON, err := json.Marshal(task.TargetIPs)
	if err != nil {
		return err
	}
	resetsInt := 0
	if task.ResetsRestorePoints {
		resetsInt = 1
	}

	_, err = s.db.Exec(`
INSERT INTO deploy_tasks (
    task_id, state, master_ip, target_ips, image_sha256, image_bytes,
    image_path, resets_restore_points, created_at, started_at, finished_at,
    error_code, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, task.TaskID, string(task.State), task.MasterIP, string(targetJSON), task.ImageSHA256,
		task.ImageBytes, task.ImagePath, resetsInt, task.CreatedAt, task.StartedAt,
		task.FinishedAt, task.ErrorCode, task.ErrorMessage)
	return err
}

func (s *Store) UpdateTask(task protocol.DeployTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	targetJSON, err := json.Marshal(task.TargetIPs)
	if err != nil {
		return err
	}

	res, err := s.db.Exec(`
UPDATE deploy_tasks
SET state = ?, master_ip = ?, target_ips = ?, image_sha256 = ?, image_bytes = ?,
    image_path = ?, started_at = ?, finished_at = ?, error_code = ?, error_message = ?
WHERE task_id = ?
`, string(task.State), task.MasterIP, string(targetJSON), task.ImageSHA256, task.ImageBytes,
		task.ImagePath, task.StartedAt, task.FinishedAt, task.ErrorCode, task.ErrorMessage, task.TaskID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetTask(taskID string) (*protocol.DeployTask, error) {
	row := s.db.QueryRow(`
SELECT task_id, state, master_ip, target_ips, image_sha256, image_bytes,
       image_path, resets_restore_points, created_at, started_at, finished_at,
       error_code, error_message
FROM deploy_tasks
WHERE task_id = ?
`, taskID)

	var (
		task       protocol.DeployTask
		stateStr   string
		targetJSON string
		resetsInt  int
	)
	err := row.Scan(
		&task.TaskID, &stateStr, &task.MasterIP, &targetJSON, &task.ImageSHA256,
		&task.ImageBytes, &task.ImagePath, &resetsInt, &task.CreatedAt,
		&task.StartedAt, &task.FinishedAt, &task.ErrorCode, &task.ErrorMessage,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	task.State = protocol.TaskState(stateStr)
	task.ResetsRestorePoints = (resetsInt == 1)
	_ = json.Unmarshal([]byte(targetJSON), &task.TargetIPs)
	return &task, nil
}

func (s *Store) ListTasks(limit, offset int) ([]protocol.DeployTask, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
SELECT task_id, state, master_ip, target_ips, image_sha256, image_bytes,
       image_path, resets_restore_points, created_at, started_at, finished_at,
       error_code, error_message
FROM deploy_tasks
ORDER BY created_at DESC
LIMIT ? OFFSET ?
`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []protocol.DeployTask
	for rows.Next() {
		var (
			task       protocol.DeployTask
			stateStr   string
			targetJSON string
			resetsInt  int
		)
		err := rows.Scan(
			&task.TaskID, &stateStr, &task.MasterIP, &targetJSON, &task.ImageSHA256,
			&task.ImageBytes, &task.ImagePath, &resetsInt, &task.CreatedAt,
			&task.StartedAt, &task.FinishedAt, &task.ErrorCode, &task.ErrorMessage,
		)
		if err != nil {
			return nil, err
		}
		task.State = protocol.TaskState(stateStr)
		task.ResetsRestorePoints = (resetsInt == 1)
		_ = json.Unmarshal([]byte(targetJSON), &task.TargetIPs)
		tasks = append(tasks, task)
	}
	if tasks == nil {
		tasks = make([]protocol.DeployTask, 0)
	}
	return tasks, nil
}

// --- Audit Log ---

func (s *Store) RecordAudit(entry AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	succInt := 0
	if entry.Success {
		succInt = 1
	}

	_, err := s.db.Exec(`
INSERT INTO audit_log (timestamp, actor, action, target_ip, target_client_id, details, success)
VALUES (?, ?, ?, ?, ?, ?, ?)
`, entry.Timestamp, entry.Actor, entry.Action, entry.TargetIP, entry.TargetClientID, entry.Details, succInt)
	return err
}

func (s *Store) ListAudit(limit, offset int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
SELECT id, timestamp, actor, action, target_ip, target_client_id, details, success
FROM audit_log
ORDER BY timestamp DESC
LIMIT ? OFFSET ?
`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var (
			e         AuditEntry
			succInt   int
			targetIP  sql.NullString
			targetCID sql.NullString
			details   sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Actor, &e.Action, &targetIP, &targetCID, &details, &succInt); err != nil {
			return nil, err
		}
		e.Success = (succInt == 1)
		if targetIP.Valid {
			e.TargetIP = targetIP.String
		}
		if targetCID.Valid {
			e.TargetClientID = targetCID.String
		}
		if details.Valid {
			e.Details = details.String
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = make([]AuditEntry, 0)
	}
	return entries, nil
}
