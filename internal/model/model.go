package model

import "time"

type Appliance struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Hostname    string    `json:"hostname"`
	VolumeID    string    `json:"volume_id"`
	ApplianceID string    `json:"appliance_uuid,omitempty"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
}

type Backup struct {
	ID              string     `json:"id"`
	ApplianceID     string     `json:"appliance_id"`
	NativeTimestamp string     `json:"native_timestamp"`
	Provider        string     `json:"provider"`
	ProviderID      string     `json:"provider_snapshot_id,omitempty"`
	VolumeID        string     `json:"volume_id"`
	State           string     `json:"state"`
	Error           string     `json:"error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

type CollectionRequest struct {
	NativeTimestamp string `json:"native_timestamp"`
}

type Health struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Provider string `json:"provider"`
}
