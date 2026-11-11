package vhdx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/owner/back-ez/internal/protocol"
)

var (
	ErrMetadataNotFound = errors.New("metadata file not found")
)

// Chain manages the in-memory tree/list of RestorePoints and their parent-child relationships.
type Chain struct {
	mu     sync.RWMutex
	Points map[string]*protocol.RestorePoint `json:"points"`
}

// NewChain creates an empty Chain.
func NewChain() *Chain {
	return &Chain{
		Points: make(map[string]*protocol.RestorePoint),
	}
}

// AddPoint adds a restore point to the chain.
func (c *Chain) AddPoint(p *protocol.RestorePoint) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.Points[p.PointID]; exists {
		return fmt.Errorf("restore point %s already exists", p.PointID)
	}

	clone := *p
	c.Points[p.PointID] = &clone
	return nil
}

// GetPoint returns a copy of the restore point by ID, or nil if not found.
func (c *Chain) GetPoint(pointID string) *protocol.RestorePoint {
	c.mu.RLock()
	defer c.mu.RUnlock()

	p, ok := c.Points[pointID]
	if !ok {
		return nil
	}
	clone := *p
	return &clone
}

// GetActivePoint returns the single active restore point, or nil if none.
func (c *Chain) GetActivePoint() *protocol.RestorePoint {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, p := range c.Points {
		if p.IsActive {
			clone := *p
			return &clone
		}
	}
	return nil
}

// SetActive sets the specified point as active and clears IsActive on all other points.
func (c *Chain) SetActive(pointID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, p := range c.Points {
		p.IsActive = (id == pointID)
	}
}

// GetChildren returns all points whose ParentID equals pointID.
func (c *Chain) GetChildren(pointID string) []*protocol.RestorePoint {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var children []*protocol.RestorePoint
	for _, p := range c.Points {
		if p.ParentID != nil && *p.ParentID == pointID {
			clone := *p
			children = append(children, &clone)
		}
	}
	return children
}

// RemovePoint removes a restore point from the chain.
func (c *Chain) RemovePoint(pointID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.Points[pointID]; !ok {
		return fmt.Errorf("restore point %s does not exist", pointID)
	}
	delete(c.Points, pointID)
	return nil
}

// List returns all restore points sorted deterministically by depth, then created_at, then point_id.
func (c *Chain) List() []protocol.RestorePoint {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]protocol.RestorePoint, 0, len(c.Points))
	for _, p := range c.Points {
		result = append(result, *p)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Depth != result[j].Depth {
			return result[i].Depth < result[j].Depth
		}
		if result[i].CreatedAt != result[j].CreatedAt {
			return result[i].CreatedAt < result[j].CreatedAt
		}
		return result[i].PointID < result[j].PointID
	})

	return result
}

// Validate ensures chain integrity:
// 1. Exactly one active point exists (if points > 0)
// 2. Exactly one base point exists (depth = 0, is_base = true, parent_id = nil)
// 3. All non-base points reference existing parents and have correct depth
// 4. No point exceeds MaxChainDepth
func (c *Chain) Validate() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.Points) == 0 {
		return nil
	}

	activeCount := 0
	baseCount := 0

	for _, p := range c.Points {
		if p.IsActive {
			activeCount++
		}
		if p.IsBase {
			baseCount++
			if p.Depth != 0 {
				return fmt.Errorf("base point %s must have depth 0, got %d", p.PointID, p.Depth)
			}
			if p.ParentID != nil {
				return fmt.Errorf("base point %s must have nil parent_id", p.PointID)
			}
		} else {
			if p.ParentID == nil || *p.ParentID == "" {
				return fmt.Errorf("non-base point %s must have parent_id", p.PointID)
			}
			parent, exists := c.Points[*p.ParentID]
			if !exists {
				return fmt.Errorf("parent %s for point %s does not exist", *p.ParentID, p.PointID)
			}
			if p.Depth != parent.Depth+1 {
				return fmt.Errorf("point %s has invalid depth %d (parent depth %d)", p.PointID, p.Depth, parent.Depth)
			}
		}

		if p.Depth > MaxChainDepth {
			return fmt.Errorf("point %s exceeds max depth %d: %d", p.PointID, MaxChainDepth, p.Depth)
		}
	}

	if activeCount != 1 {
		return fmt.Errorf("expected exactly 1 active point, found %d", activeCount)
	}
	if baseCount != 1 {
		return fmt.Errorf("expected exactly 1 base point, found %d", baseCount)
	}

	return nil
}

// SaveToFile serializes the chain to a JSON file.
func (c *Chain) SaveToFile(path string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for metadata: %w", err)
	}

	data, err := json.MarshalIndent(c.Points, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal chain metadata: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write tmp metadata: %w", err)
	}
	return os.Rename(tmpPath, path)
}

// LoadFromFile restores the chain from a JSON file.
func (c *Chain) LoadFromFile(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrMetadataNotFound
		}
		return fmt.Errorf("failed to read metadata file: %w", err)
	}

	var points map[string]*protocol.RestorePoint
	if err := json.Unmarshal(data, &points); err != nil {
		return fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	c.Points = points
	return nil
}
