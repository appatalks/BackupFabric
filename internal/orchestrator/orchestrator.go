package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/appatalks/backupfabric/internal/catalog"
	"github.com/appatalks/backupfabric/internal/identity"
	"github.com/appatalks/backupfabric/internal/model"
	"github.com/appatalks/backupfabric/internal/provider"
)

var nativeTimestampPattern = regexp.MustCompile(`^\d{8}T\d{6}$`)

type Orchestrator struct {
	catalog  *catalog.Catalog
	provider provider.Provider
	locks    sync.Map
}

func New(catalog *catalog.Catalog, provider provider.Provider) *Orchestrator {
	return &Orchestrator{catalog: catalog, provider: provider}
}

func (o *Orchestrator) Collect(
	ctx context.Context, applianceID, nativeTimestamp string,
) (model.Backup, error) {
	if !nativeTimestampPattern.MatchString(nativeTimestamp) {
		return model.Backup{}, fmt.Errorf("native timestamp must use YYYYMMDDTHHMMSS")
	}
	mutexValue, _ := o.locks.LoadOrStore(applianceID, &sync.Mutex{})
	mutex := mutexValue.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()

	appliance, err := o.catalog.Appliance(ctx, applianceID)
	if err != nil {
		return model.Backup{}, err
	}
	if appliance.State != "enabled" {
		return model.Backup{}, fmt.Errorf("appliance is not enabled")
	}

	id, err := identity.New("bkp")
	if err != nil {
		return model.Backup{}, err
	}
	backup := model.Backup{
		ID:              id,
		ApplianceID:     appliance.ID,
		NativeTimestamp: nativeTimestamp,
		Provider:        o.provider.Name(),
		VolumeID:        appliance.VolumeID,
		State:           "snapshotting",
		CreatedAt:       time.Now().UTC(),
	}
	if err := o.catalog.CreateBackup(ctx, backup); err != nil {
		return model.Backup{}, err
	}

	snapshot, snapshotErr := o.provider.CreateSnapshot(ctx, provider.SnapshotRequest{
		ApplianceID:     appliance.ID,
		VolumeID:        appliance.VolumeID,
		NativeTimestamp: nativeTimestamp,
		IdempotencyKey:  appliance.ID + "_" + nativeTimestamp,
	})
	completed := time.Now().UTC()
	if snapshotErr != nil {
		backup.State = "failed"
		backup.Error = snapshotErr.Error()
		if err := o.catalog.CompleteBackup(
			ctx, backup.ID, "", backup.State, backup.Error, completed,
		); err != nil {
			return model.Backup{}, fmt.Errorf("record snapshot failure: %w", err)
		}
		backup.CompletedAt = &completed
		return backup, snapshotErr
	}

	backup.State = "collected"
	backup.ProviderID = snapshot.ID
	backup.CompletedAt = &completed
	if err := o.catalog.CompleteBackup(
		ctx, backup.ID, snapshot.ID, backup.State, "", completed,
	); err != nil {
		return model.Backup{}, err
	}
	return backup, nil
}
