package store

import (
	"database/sql"
	"fmt"
	"time"
)

type migration struct {
	version int
	up      string
}

var migrations = []migration{
	{
		version: 1,
		up: `
CREATE TABLE IF NOT EXISTS admins (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL,
    csrf_token TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_active_at TEXT NOT NULL,
    FOREIGN KEY (admin_id) REFERENCES admins(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ip_groups (
    group_id TEXT PRIMARY KEY,
    group_name TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS group_ips (
    group_id TEXT NOT NULL,
    ip TEXT NOT NULL UNIQUE,
    PRIMARY KEY (group_id, ip),
    FOREIGN KEY (group_id) REFERENCES ip_groups(group_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS clients (
    client_id TEXT PRIMARY KEY,
    current_ip TEXT NOT NULL,
    mac_address TEXT,
    mac_source TEXT,
    hostname TEXT NOT NULL,
    os_edition TEXT NOT NULL,
    agent_version TEXT NOT NULL,
    pe_ready INTEGER NOT NULL DEFAULT 0,
    group_id TEXT,
    status TEXT NOT NULL,
    auto_restore_on_reboot INTEGER NOT NULL DEFAULT 0,
    active_point_id TEXT,
    last_task_id TEXT,
    progress_percent REAL,
    error_message TEXT,
    last_seen TEXT NOT NULL,
    FOREIGN KEY (group_id) REFERENCES ip_groups(group_id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS restore_points (
    point_id TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    vhdx_file TEXT NOT NULL,
    parent_id TEXT,
    depth INTEGER NOT NULL,
    size_bytes INTEGER NOT NULL,
    is_base INTEGER NOT NULL,
    is_active INTEGER NOT NULL,
    state TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (client_id) REFERENCES clients(client_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS network_identities (
    client_id TEXT PRIMARY KEY,
    mac_address TEXT NOT NULL,
    ip_mode TEXT NOT NULL,
    ip_address TEXT NOT NULL,
    subnet_mask TEXT NOT NULL,
    default_gateway TEXT NOT NULL,
    dns_servers TEXT NOT NULL,
    original_hostname TEXT NOT NULL,
    captured_at TEXT NOT NULL,
    FOREIGN KEY (client_id) REFERENCES clients(client_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS mac_cache (
    ip TEXT PRIMARY KEY,
    mac_address TEXT NOT NULL,
    source TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS deploy_tasks (
    task_id TEXT PRIMARY KEY,
    state TEXT NOT NULL,
    master_ip TEXT NOT NULL,
    target_ips TEXT NOT NULL,
    image_sha256 TEXT NOT NULL,
    image_bytes INTEGER NOT NULL,
    image_path TEXT NOT NULL,
    resets_restore_points INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    error_code TEXT,
    error_message TEXT
);

CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    target_ip TEXT,
    target_client_id TEXT,
    details TEXT,
    success INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_clients_current_ip ON clients(current_ip);
CREATE INDEX IF NOT EXISTS idx_clients_group_id ON clients(group_id);
CREATE INDEX IF NOT EXISTS idx_restore_points_client ON restore_points(client_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_timestamp ON audit_log(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_deploy_tasks_created_at ON deploy_tasks(created_at DESC);
`,
	},
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);
`)
	if err != nil {
		return fmt.Errorf("failed to create schema_version table: %w", err)
	}

	var currentVersion int
	row := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	if err := row.Scan(&currentVersion); err != nil {
		return fmt.Errorf("failed to query schema_version: %w", err)
	}

	for _, m := range migrations {
		if m.version > currentVersion {
			tx, err := db.Begin()
			if err != nil {
				return fmt.Errorf("failed to begin migration %d tx: %w", m.version, err)
			}

			if _, err := tx.Exec(m.up); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("failed to apply migration %d: %w", m.version, err)
			}

			now := time.Now().UTC().Format(time.RFC3339)
			if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`, m.version, now); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("failed to record schema_version %d: %w", m.version, err)
			}

			if err := tx.Commit(); err != nil {
				return fmt.Errorf("failed to commit migration %d: %w", m.version, err)
			}
		}
	}

	return nil
}
