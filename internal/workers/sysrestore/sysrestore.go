// Package sysrestore wraps the built-in Windows System Restore feature.
//
// It is the low-risk "local backup" path: it can list, create and apply Windows
// restore points without touching VHDX files or BCD. It never changes System
// Protection settings unless the caller explicitly asks for Enable.
package sysrestore

import (
	"context"
	"fmt"
)

const (
	// DefaultThrottleMinutes is Windows' default minimum interval between
	// restore points (24 hours) when SystemRestorePointCreationFrequency is
	// not configured. The registry value is expressed in minutes.
	DefaultThrottleMinutes = 1440
	// MinFreeBytes is the free-space floor required before creating a point.
	MinFreeBytes uint64 = 1 << 30
	// MaxDescriptionRunes bounds the restore point description.
	MaxDescriptionRunes = 200
)

// Blocker explains why an operation is unavailable.
type Blocker struct {
	Key     string `json:"key"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

// Point is one Windows System Restore point.
type Point struct {
	Sequence    int    `json:"sequence"`
	Description string `json:"description"`
	Type        int    `json:"type"`
	CreatedAt   string `json:"created_at"`
}

// Status is the capability report for the built-in System Restore feature.
type Status struct {
	Supported         bool      `json:"supported"`
	IsAdmin           bool      `json:"is_admin"`
	Drive             string    `json:"drive"`
	ProtectionEnabled bool      `json:"protection_enabled"`
	FreeBytes         uint64    `json:"free_bytes"`
	ThrottleMinutes   int       `json:"throttle_minutes"`
	NextAllowedAt     string    `json:"next_allowed_at,omitempty"`
	PointCount        int       `json:"point_count"`
	LastCreatedAt     string    `json:"last_created_at,omitempty"`
	CanCreate         bool      `json:"can_create"`
	CanRestore        bool      `json:"can_restore"`
	Blockers          []Blocker `json:"blockers"`
	Error             string    `json:"error,omitempty"`
}

// Service is the read/write surface used by the local GUI.
type Service interface {
	Status(ctx context.Context) Status
	List(ctx context.Context) ([]Point, error)
	Create(ctx context.Context, description string) (Point, error)
	Restore(ctx context.Context, sequence int) error
	Enable(ctx context.Context, drive string) error
}

// Error carries a SPEC-compliant error code so handlers can map it to HTTP.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func newError(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// compile-time assertion that Manager implements Service.
var _ Service = (*Manager)(nil)
