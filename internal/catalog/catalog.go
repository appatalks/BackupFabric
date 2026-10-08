package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/appatalks/backupfabric/internal/live"
	"github.com/appatalks/backupfabric/internal/model"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("catalog record not found")

type Catalog struct {
	db *sql.DB
}

func (c *Catalog) LiveStore() (*live.Store, error) {
	return live.NewStore(c.db)
}

func Open(path string) (*Catalog, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open catalog: %w", err)
	}
	db.SetMaxOpenConns(1)
	c := &Catalog{db: db}
	if err := c.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}

func (c *Catalog) Close() error {
	return c.db.Close()
}

func (c *Catalog) migrate(ctx context.Context) error {
	const schema = `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
CREATE TABLE IF NOT EXISTS appliances (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	hostname TEXT NOT NULL UNIQUE,
	volume_id TEXT NOT NULL UNIQUE,
	appliance_uuid TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL CHECK (state IN ('enabled', 'disabled')),
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS backups (
	id TEXT PRIMARY KEY,
	appliance_id TEXT NOT NULL REFERENCES appliances(id),
	native_timestamp TEXT NOT NULL,
	provider TEXT NOT NULL,
	provider_snapshot_id TEXT NOT NULL DEFAULT '',
	volume_id TEXT NOT NULL,
	state TEXT NOT NULL CHECK (
		state IN ('requested', 'snapshotting', 'collected', 'failed',
		          'structurally_valid', 'restore_qualified')
	),
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	completed_at TEXT,
	UNIQUE(appliance_id, native_timestamp)
);
CREATE INDEX IF NOT EXISTS backups_appliance_created
	ON backups(appliance_id, created_at DESC);
`
	if _, err := c.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate catalog: %w", err)
	}
	return nil
}

func (c *Catalog) CreateAppliance(ctx context.Context, appliance model.Appliance) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO appliances
			(id, name, hostname, volume_id, appliance_uuid, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		appliance.ID, appliance.Name, appliance.Hostname, appliance.VolumeID,
		appliance.ApplianceID, appliance.State, appliance.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create appliance: %w", err)
	}
	return nil
}

func (c *Catalog) ListAppliances(ctx context.Context) ([]model.Appliance, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, name, hostname, volume_id, appliance_uuid, state, created_at
		FROM appliances ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list appliances: %w", err)
	}
	defer rows.Close()

	result := make([]model.Appliance, 0)
	for rows.Next() {
		appliance, err := scanAppliance(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, appliance)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list appliances: %w", err)
	}
	return result, nil
}

func (c *Catalog) Appliance(ctx context.Context, id string) (model.Appliance, error) {
	row := c.db.QueryRowContext(ctx, `
		SELECT id, name, hostname, volume_id, appliance_uuid, state, created_at
		FROM appliances WHERE id = ?`, id)
	appliance, err := scanAppliance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Appliance{}, ErrNotFound
	}
	return appliance, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAppliance(row rowScanner) (model.Appliance, error) {
	var appliance model.Appliance
	var created string
	if err := row.Scan(
		&appliance.ID, &appliance.Name, &appliance.Hostname, &appliance.VolumeID,
		&appliance.ApplianceID, &appliance.State, &created,
	); err != nil {
		return model.Appliance{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return model.Appliance{}, fmt.Errorf("parse appliance creation time: %w", err)
	}
	appliance.CreatedAt = parsed
	return appliance, nil
}

func (c *Catalog) CreateBackup(ctx context.Context, backup model.Backup) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO backups
			(id, appliance_id, native_timestamp, provider, provider_snapshot_id,
			 volume_id, state, error, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		backup.ID, backup.ApplianceID, backup.NativeTimestamp, backup.Provider,
		backup.ProviderID, backup.VolumeID, backup.State, backup.Error,
		backup.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	return nil
}

func (c *Catalog) CompleteBackup(
	ctx context.Context, id, providerID, state, failure string, completed time.Time,
) error {
	result, err := c.db.ExecContext(ctx, `
		UPDATE backups
		SET provider_snapshot_id = ?, state = ?, error = ?, completed_at = ?
		WHERE id = ?`,
		providerID, state, failure, completed.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("complete backup: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read backup update result: %w", err)
	}
	if changed != 1 {
		return ErrNotFound
	}
	return nil
}

func (c *Catalog) ListBackups(ctx context.Context) ([]model.Backup, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, appliance_id, native_timestamp, provider,
		       provider_snapshot_id, volume_id, state, error, created_at, completed_at
		FROM backups ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	defer rows.Close()

	result := make([]model.Backup, 0)
	for rows.Next() {
		var backup model.Backup
		var created string
		var completed sql.NullString
		if err := rows.Scan(
			&backup.ID, &backup.ApplianceID, &backup.NativeTimestamp, &backup.Provider,
			&backup.ProviderID, &backup.VolumeID, &backup.State, &backup.Error,
			&created, &completed,
		); err != nil {
			return nil, fmt.Errorf("scan backup: %w", err)
		}
		backup.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse backup creation time: %w", err)
		}
		if completed.Valid {
			value, err := time.Parse(time.RFC3339Nano, completed.String)
			if err != nil {
				return nil, fmt.Errorf("parse backup completion time: %w", err)
			}
			backup.CompletedAt = &value
		}
		result = append(result, backup)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	return result, nil
}

func IsConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "constraint failed")
}
