package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/owner/back-ez/internal/protocol"
)

// Client communicates with the background Agent service via Named Pipe / NDJSON IPC.
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	reqID  int64
}

func NewClient(conn net.Conn) *Client {
	return &Client{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}
}

func (c *Client) call(method string, params any) (*protocol.IPCResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var rawParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		rawParams = data
	}

	c.reqID++
	req := protocol.IPCRequest{
		ID:     c.reqID,
		Method: method,
		Params: rawParams,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	if _, err := c.conn.Write(data); err != nil {
		return nil, fmt.Errorf("failed to write to pipe: %w", err)
	}

	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read from pipe: %w", err)
	}

	var resp protocol.IPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("invalid response format: %w", err)
	}

	if !resp.OK {
		if resp.Error != nil {
			return &resp, errors.New(resp.Error.Code)
		}
		return &resp, errors.New(protocol.ErrInternal)
	}

	return &resp, nil
}

// --- Read-only methods (Password free) ---

func (c *Client) GetAgentInfo() (map[string]any, error) {
	resp, err := c.call("GetAgentInfo", nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	_ = json.Unmarshal(resp.Result, &res)
	return res, nil
}

func (c *Client) GetStatus() (map[string]any, error) {
	resp, err := c.call("GetStatus", nil)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	_ = json.Unmarshal(resp.Result, &res)
	return res, nil
}

func (c *Client) ListRestorePoints() ([]protocol.RestorePoint, error) {
	resp, err := c.call("ListRestorePoints", nil)
	if err != nil {
		return nil, err
	}
	var res []protocol.RestorePoint
	_ = json.Unmarshal(resp.Result, &res)
	return res, nil
}

// --- Mutating methods (Password required) ---

func (c *Client) CreateRestorePoint(password, name, description string) error {
	_, err := c.call("CreateRestorePoint", map[string]any{
		"password":    password,
		"name":        name,
		"description": description,
	})
	return err
}

func (c *Client) SwitchRestorePoint(password, pointID string) error {
	_, err := c.call("SwitchRestorePoint", map[string]any{
		"password": password,
		"point_id": pointID,
	})
	return err
}

func (c *Client) DeleteRestorePoint(password, pointID string) error {
	_, err := c.call("DeleteRestorePoint", map[string]any{
		"password": password,
		"point_id": pointID,
	})
	return err
}

func (c *Client) OverwriteRestorePoint(password, pointID string) error {
	_, err := c.call("OverwriteRestorePoint", map[string]any{
		"password": password,
		"point_id": pointID,
	})
	return err
}

func (c *Client) SetAutoRestore(password string, enabled bool) error {
	_, err := c.call("SetAutoRestore", map[string]any{
		"password": password,
		"enabled":  enabled,
	})
	return err
}

// --- Privileged methods (Password + UAC) ---

func (c *Client) SetHubEndpoint(password, hubURL, enrollToken string) error {
	_, err := c.call("SetHubEndpoint", map[string]any{
		"password":     password,
		"hub_url":      hubURL,
		"enroll_token": enrollToken,
	})
	return err
}

func (c *Client) UninstallService(password string) error {
	_, err := c.call("UninstallService", map[string]any{
		"password": password,
	})
	return err
}

// Subscribe listens for push events over the pipe connection.
func (c *Client) Subscribe(onEvent func(event string, data []byte)) error {
	_, err := c.call("Subscribe", nil)
	if err != nil {
		return err
	}

	go func() {
		for {
			line, err := c.reader.ReadBytes('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				break
			}
			var evt struct {
				Event string          `json:"event"`
				Data  json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(line, &evt); err == nil && evt.Event != "" {
				onEvent(evt.Event, evt.Data)
			}
		}
	}()

	return nil
}
