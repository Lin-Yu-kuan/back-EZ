package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// normalizeJSON formats JSON with 2-space indentation and a trailing newline.
func normalizeJSON(t *testing.T, data []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, bytes.TrimSpace(data), "", "  "); err != nil {
		t.Fatalf("failed to normalize json: %v", err)
	}
	buf.WriteByte('\n')
	return buf.String()
}

func readTestData(t *testing.T, filename string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read testdata %s: %v", filename, err)
	}
	return data
}

func TestNetworkIdentity_Golden(t *testing.T) {
	raw := readTestData(t, "network_identity.json")

	var identity NetworkIdentity
	if err := json.Unmarshal(raw, &identity); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if identity.IPMode != IPModeStatic {
		t.Errorf("expected IPMode %s, got %s", IPModeStatic, identity.IPMode)
	}
	if identity.IPAddress != "192.168.1.101" {
		t.Errorf("unexpected IPAddress: %s", identity.IPAddress)
	}
	if len(identity.DNSServers) != 2 {
		t.Errorf("expected 2 DNS servers, got %d", len(identity.DNSServers))
	}

	marshaled, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	if normalizeJSON(t, raw) != normalizeJSON(t, marshaled) {
		t.Errorf("JSON mismatch:\nwant:\n%s\ngot:\n%s", normalizeJSON(t, raw), normalizeJSON(t, marshaled))
	}
}

func TestRestorePoint_Golden(t *testing.T) {
	raw := readTestData(t, "restore_point.json")

	var rp RestorePoint
	if err := json.Unmarshal(raw, &rp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if rp.PointID != "rp_20261110_01" {
		t.Errorf("unexpected PointID: %s", rp.PointID)
	}
	if rp.Depth != 1 {
		t.Errorf("expected Depth 1, got %d", rp.Depth)
	}
	if rp.State != RestorePointStateReady {
		t.Errorf("expected state READY, got %s", rp.State)
	}
	if rp.CreatedBy != CreatedByAdminRemote {
		t.Errorf("expected created_by ADMIN_REMOTE, got %s", rp.CreatedBy)
	}

	marshaled, err := json.MarshalIndent(rp, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	if normalizeJSON(t, raw) != normalizeJSON(t, marshaled) {
		t.Errorf("JSON mismatch:\nwant:\n%s\ngot:\n%s", normalizeJSON(t, raw), normalizeJSON(t, marshaled))
	}
}

func TestClientNode_Golden(t *testing.T) {
	raw := readTestData(t, "client_node.json")

	var client ClientNode
	if err := json.Unmarshal(raw, &client); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if client.ClientID != "cli_9f2c4b1e8a7d4c3f" {
		t.Errorf("unexpected ClientID: %s", client.ClientID)
	}
	if client.Status != ClientStatusOnlineWindows {
		t.Errorf("expected status ONLINE_WINDOWS, got %s", client.Status)
	}
	if client.NetworkSnapshot.Identity != nil {
		t.Errorf("expected empty NetworkSnapshot identity, got %+v", client.NetworkSnapshot.Identity)
	}
	if client.LastTaskID != nil {
		t.Errorf("expected LastTaskID to be nil, got %v", *client.LastTaskID)
	}
	if client.ProgressPercent != nil {
		t.Errorf("expected ProgressPercent to be nil, got %v", *client.ProgressPercent)
	}
	if client.ErrorMessage != nil {
		t.Errorf("expected ErrorMessage to be nil, got %v", *client.ErrorMessage)
	}

	marshaled, err := json.MarshalIndent(client, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	if normalizeJSON(t, raw) != normalizeJSON(t, marshaled) {
		t.Errorf("JSON mismatch:\nwant:\n%s\ngot:\n%s", normalizeJSON(t, raw), normalizeJSON(t, marshaled))
	}
}

func TestIPGroup_Golden(t *testing.T) {
	raw := readTestData(t, "ip_group.json")

	var group IPGroup
	if err := json.Unmarshal(raw, &group); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if group.GroupID != "grp_row_1" {
		t.Errorf("unexpected GroupID: %s", group.GroupID)
	}
	if len(group.IPList) != 3 {
		t.Errorf("expected 3 IPs, got %d", len(group.IPList))
	}

	marshaled, err := json.MarshalIndent(group, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	if normalizeJSON(t, raw) != normalizeJSON(t, marshaled) {
		t.Errorf("JSON mismatch:\nwant:\n%s\ngot:\n%s", normalizeJSON(t, raw), normalizeJSON(t, marshaled))
	}
}

func TestDeployTask_Golden(t *testing.T) {
	raw := readTestData(t, "deploy_task.json")

	var task DeployTask
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if task.TaskID != "task_9981" {
		t.Errorf("unexpected TaskID: %s", task.TaskID)
	}
	if task.State != TaskStateCasting {
		t.Errorf("expected state CASTING, got %s", task.State)
	}
	if task.ImageBytes != 32212254720 {
		t.Errorf("unexpected image bytes: %d", task.ImageBytes)
	}

	marshaled, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	if normalizeJSON(t, raw) != normalizeJSON(t, marshaled) {
		t.Errorf("JSON mismatch:\nwant:\n%s\ngot:\n%s", normalizeJSON(t, raw), normalizeJSON(t, marshaled))
	}
}

func TestWSMessage_DeployProgress_Golden(t *testing.T) {
	raw := readTestData(t, "ws_message_deploy_progress.json")

	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if msg.V != 1 || msg.Seq != 128 || msg.Event != EventDeployProgress {
		t.Errorf("unexpected header: v=%d seq=%d event=%s", msg.V, msg.Seq, msg.Event)
	}

	var payload DeployProgressPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload error: %v", err)
	}

	if payload.TaskID != "task_9981" || payload.MasterIP != "192.168.1.100" {
		t.Errorf("unexpected payload: task=%s master=%s", payload.TaskID, payload.MasterIP)
	}
	if len(payload.Clients) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(payload.Clients))
	}
	if payload.Clients[0].Progress != 68.5 || payload.Clients[0].Status != ClientStatusDeploying {
		t.Errorf("unexpected client 0 progress: %+v", payload.Clients[0])
	}
}
