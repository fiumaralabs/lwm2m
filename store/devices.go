// Package store defines tenant-scoped native API persistence contracts.
package store

import (
	"errors"
	"time"
)

var (
	ErrDeviceExists   = errors.New("device exists")
	ErrDeviceNotFound = errors.New("device not found")
	ErrDeviceCursor   = errors.New("invalid device cursor")
)

// Device contains provisioned metadata, not the current registration.
type Device struct {
	Endpoint   string            `json:"endpoint"`
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
	LastSeenAt *time.Time        `json:"lastSeenAt"`
}

// DeviceFilter selects metadata and optionally a live status projection.
// StatusOf must be safe for concurrent use and must not modify the store.
type DeviceFilter struct {
	Q, Status string
	StatusOf  func(Device) string
}

// Devices implementations must be concurrency-safe and return owned copies.
// List sorts by endpoint; cursors are opaque and bound to tenant and filters.
type Devices interface {
	Create(tenant string, d Device) (Device, error)
	Get(tenant, endpoint string) (Device, error)
	List(tenant string, filter DeviceFilter, cursor string, limit int) ([]Device, string, error)
	Delete(tenant, endpoint string) error
	// Seen records successful registration/update history without provisioning.
	Seen(tenant, endpoint string, at time.Time)
}
