package live

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) (*Store, error) {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS live_endpoints (
	appliance_id TEXT PRIMARY KEY REFERENCES appliances(id),
	role TEXT NOT NULL CHECK(role IN ('source','receiver','restore')),
	host TEXT NOT NULL UNIQUE,
	receiver_id TEXT UNIQUE REFERENCES live_endpoints(appliance_id),
	config TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS live_settings (
	id INTEGER PRIMARY KEY CHECK(id=1),
	config TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS live_jobs (
	id TEXT PRIMARY KEY,
	state TEXT NOT NULL,
	config TEXT NOT NULL
);`)
	if err != nil {
		return nil, fmt.Errorf("initialize live catalog: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) SaveEndpoint(ctx context.Context, e Endpoint) error {
	if err := ValidateEndpoint(e); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldConfig string
	err = tx.QueryRowContext(ctx, "SELECT config FROM live_endpoints WHERE appliance_id=?", e.ApplianceID).Scan(&oldConfig)
	if err == nil {
		// Identity and role changes require a new registration rather than rebinding archived history.
		var old Endpoint
		if err := json.Unmarshal([]byte(oldConfig), &old); err != nil {
			return err
		}
		if old.Role != e.Role || old.Host != e.Host || old.ReceiverID != e.ReceiverID || old.CollectionMode != e.CollectionMode || old.NativeRoute != e.NativeRoute || old.NativeSourceUUID != e.NativeSourceUUID {
			return fmt.Errorf("endpoint host, role, and receiver mapping are immutable; register a new appliance")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if e.Role == "source" && e.CollectionMode != "native_push" {
		var role string
		if err := tx.QueryRowContext(ctx, "SELECT role FROM live_endpoints WHERE appliance_id=?", e.ReceiverID).Scan(&role); err != nil {
			return fmt.Errorf("receiver must already have a registered SSH profile: %w", err)
		}
		if role != "receiver" {
			return fmt.Errorf("mapped endpoint is not a receiver")
		}
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	var receiver any
	if e.ReceiverID != "" {
		receiver = e.ReceiverID
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO live_endpoints(appliance_id,role,host,receiver_id,config) VALUES(?,?,?,?,?)
ON CONFLICT(appliance_id) DO UPDATE SET config=excluded.config`,
		e.ApplianceID, e.Role, e.Host, receiver, string(data))
	if err != nil {
		return fmt.Errorf("save endpoint (host and receiver must be unique): %w", err)
	}
	return tx.Commit()
}

func (s *Store) Endpoints(ctx context.Context) ([]Endpoint, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT config FROM live_endpoints ORDER BY appliance_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Endpoint, 0)
	for rows.Next() {
		var raw string
		var e Endpoint
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

func (s *Store) Endpoint(ctx context.Context, id string) (Endpoint, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT config FROM live_endpoints WHERE appliance_id=?", id).Scan(&raw); err != nil {
		return Endpoint{}, err
	}
	var e Endpoint
	err := json.Unmarshal([]byte(raw), &e)
	return e, err
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT config FROM live_settings WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{TimeoutMins: 120}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var value Settings
	err = json.Unmarshal([]byte(raw), &value)
	return value, err
}

func (s *Store) SaveSettings(ctx context.Context, value Settings) error {
	if err := ValidateSettings(value); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO live_settings VALUES(1,?) ON CONFLICT(id) DO UPDATE SET config=excluded.config", string(data))
	return err
}

func (s *Store) SaveJob(ctx context.Context, j Job) error {
	j.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO live_jobs(id,state,config) VALUES(?,?,?)
ON CONFLICT(id) DO UPDATE SET state=excluded.state,config=excluded.config`, j.ID, j.State, string(raw))
	return err
}

func (s *Store) ObserveNativeSnapshot(ctx context.Context, snapshot NativeSnapshot) (bool, error) {
	if snapshot.State != "received_unverified" || !validTimestamp(snapshot.Timestamp) ||
		!idPattern.MatchString(snapshot.SourceID) || snapshot.SourceID[:4] != "app_" ||
		!nativeVersion.MatchString(snapshot.Version) || !nativeUUID.MatchString(snapshot.UUID) || !refPattern.MatchString(snapshot.Route) {
		return false, fmt.Errorf("only valid native snapshot observations can be journaled")
	}
	fingerprint := sha256.Sum256([]byte(snapshot.SourceID + "\x00" + snapshot.Timestamp))
	id := fmt.Sprintf("job_%x", fingerprint[:16])
	now := time.Now().UTC()
	job := Job{
		ID: id, Request: Request{Action: "discover", SourceID: snapshot.SourceID, Timestamp: snapshot.Timestamp},
		State: "received_unverified", Phase: "observed", NativeSnapshot: &snapshot,
		Message:   "Snapshot discovered on its isolated receiver. Observation time is not transfer time; transfer origin/completion, checksums, retention, and restore qualification are not inferred.",
		CreatedAt: now, UpdatedAt: now,
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, "INSERT INTO live_jobs(id,state,config) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING", id, job.State, string(raw))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count == 1 {
		return true, nil
	}
	existing, err := s.Job(ctx, id)
	if err != nil {
		return false, err
	}
	previous := existing.NativeSnapshot
	if existing.Request.Action != "discover" || previous == nil || previous.SourceID != snapshot.SourceID ||
		previous.Timestamp != snapshot.Timestamp || previous.UUID != snapshot.UUID ||
		previous.Version != snapshot.Version || previous.Route != snapshot.Route {
		return false, fmt.Errorf("previously observed snapshot identity/version changed; inspect receiver history")
	}
	return false, nil
}

func (s *Store) Job(ctx context.Context, id string) (Job, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT config FROM live_jobs WHERE id=?", id).Scan(&raw); err != nil {
		return Job{}, err
	}
	var j Job
	err := json.Unmarshal([]byte(raw), &j)
	return j, err
}

func (s *Store) Jobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT config FROM live_jobs ORDER BY rowid DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Job, 0)
	for rows.Next() {
		var raw string
		var j Job
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &j); err != nil {
			return nil, err
		}
		result = append(result, j)
	}
	return result, rows.Err()
}

func (s *Store) Recover(ctx context.Context) error {
	jobs, err := s.Jobs(ctx)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.State == "running" {
			j.State = "interrupted"
			j.Message = "Service restarted. Partial staging is not usable; inspect remote and local state before retrying."
			if err := s.SaveJob(ctx, j); err != nil {
				return err
			}
		}
	}
	return nil
}
