package provider

import (
	"context"
	"fmt"
	"sync"
)

type SnapshotRequest struct {
	ApplianceID     string
	VolumeID        string
	NativeTimestamp string
	IdempotencyKey  string
}

type Snapshot struct {
	ID string
}

type Provider interface {
	Name() string
	CreateSnapshot(context.Context, SnapshotRequest) (Snapshot, error)
}

// DevelopmentProvider returns deterministic references and never copies data.
type DevelopmentProvider struct {
	mu      sync.Mutex
	created map[string]Snapshot
}

func NewDevelopmentProvider() *DevelopmentProvider {
	return &DevelopmentProvider{created: make(map[string]Snapshot)}
}

func (p *DevelopmentProvider) Name() string {
	return "development-reference-only"
}

func (p *DevelopmentProvider) CreateSnapshot(
	_ context.Context, request SnapshotRequest,
) (Snapshot, error) {
	if request.VolumeID == "" || request.IdempotencyKey == "" {
		return Snapshot{}, fmt.Errorf("volume and idempotency key are required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.created[request.IdempotencyKey]; ok {
		return existing, nil
	}
	snapshot := Snapshot{ID: "devsnap_" + request.IdempotencyKey}
	p.created[request.IdempotencyKey] = snapshot
	return snapshot, nil
}
