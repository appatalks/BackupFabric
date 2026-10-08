package live

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/appatalks/backupfabric/internal/identity"
)

type Service struct {
	Store           *Store
	Transport       Transport
	ArchiveRoot     string
	Logger          *slog.Logger
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	busy            bool
	wg              sync.WaitGroup
	nativeReceivers map[string]NativeReceiver
}

func NewService(store *Store, root, secrets string, logger *slog.Logger, runner Runner) (*Service, error) {
	for _, path := range []string{root, secrets} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\n\r\x00") {
			return nil, fmt.Errorf("archive and secret roots must be absolute paths")
		}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != filepath.Clean(root) {
		return nil, fmt.Errorf("archive root must not contain symlinks")
	}
	if err := store.Recover(context.Background()); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		Store: store, ArchiveRoot: root, Logger: logger,
		Transport: Transport{Runner: runner, SecretsDir: secrets},
		ctx:       ctx, cancel: cancel,
	}, nil
}

func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

func (s *Service) SaveEndpoint(ctx context.Context, e Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return fmt.Errorf("cannot edit endpoints during a live operation")
	}
	if e.CollectionMode == "native_push" {
		receiver, err := s.nativeReceiver(e)
		if err != nil {
			return err
		}
		e.NativeRoute, e.NativeSourceUUID = receiver.Route, receiver.SourceUUID
	}
	return s.Store.SaveEndpoint(ctx, e)
}

func (s *Service) Preflight(ctx context.Context, id string) (Preflight, error) {
	e, err := s.Store.Endpoint(ctx, id)
	if err != nil {
		return Preflight{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if e.CollectionMode == "native_push" {
		s.mu.Lock()
		var err error
		if !s.busy {
			_, err = s.reconcileNativeSource(ctx, e)
		}
		s.mu.Unlock()
		if err != nil {
			return Preflight{}, fmt.Errorf("native observation pre-check: %w", err)
		}
	}
	return s.Transport.Preflight(ctx, e)
}

func (s *Service) Start(ctx context.Context, r Request) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return Job{}, fmt.Errorf("one live operation is already running; wait for completion")
	}
	if err := s.ctx.Err(); err != nil {
		return Job{}, err
	}
	source, err := s.Store.Endpoint(ctx, r.SourceID)
	if err != nil {
		return Job{}, err
	}
	if source.Role != "source" {
		return Job{}, fmt.Errorf("selected appliance is not a source")
	}
	switch r.Action {
	case "backup":
		if r.Confirmation != "BACKUP "+r.SourceID {
			return Job{}, fmt.Errorf("type BACKUP followed by the selected source ID")
		}
	case "archive", "verify":
		if source.CollectionMode != "native_push" || r.Confirmation != strings.ToUpper(r.Action)+" "+r.SourceID {
			return Job{}, fmt.Errorf("native push requires the matching ARCHIVE or VERIFY source approval")
		}
		if r.Action == "verify" && !r.Quiesced {
			return Job{}, fmt.Errorf("pause source and receiver writers before checksum verification")
		}
	case "collect":
		if !r.Quiesced || r.Confirmation != "COLLECT "+r.SourceID {
			return Job{}, fmt.Errorf("confirm COLLECT source ID and that source backup/pruning and receiver remote sync are paused and complete")
		}
	case "stage":
		target, err := s.Store.Endpoint(ctx, r.TargetID)
		if err != nil {
			return Job{}, err
		}
		if target.Role != "restore" || target.ApplianceID == source.ApplianceID || target.ApplianceID == source.ReceiverID {
			return Job{}, fmt.Errorf("target must be a separately registered restore appliance")
		}
		if source.CollectionMode == "native_push" {
			receiver, err := s.nativeReceiver(source)
			if err != nil || strings.EqualFold(target.Host, receiver.DestinationHost) {
				return Job{}, fmt.Errorf("native receiver host cannot be a restore target")
			}
		}
		collection, err := s.Store.Job(ctx, r.CollectionID)
		if err != nil {
			return Job{}, err
		}
		if collection.Request.Action != "collect" || collection.State != "collected_unverified" || collection.Request.SourceID != r.SourceID {
			return Job{}, fmt.Errorf("choose a completed collection belonging to the selected source")
		}
		if !validTimestamp(r.Timestamp) {
			return Job{}, fmt.Errorf("snapshot timestamp must be a valid YYYYMMDDTHHMMSS")
		}
		if !r.Quiesced || !r.CompatibleTarget || r.Confirmation != "STAGE "+r.TargetID {
			return Job{}, fmt.Errorf("confirm STAGE target ID, paused target writers, and GHES version compatibility")
		}
	default:
		return Job{}, fmt.Errorf("unknown action")
	}
	if source.CollectionMode == "native_push" {
		if _, err := s.reconcileNativeSource(ctx, source); err != nil {
			return Job{}, fmt.Errorf("native observation pre-check: %w", err)
		}
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return Job{}, err
	}
	id, err := identity.New("job")
	if err != nil {
		return Job{}, err
	}
	now := time.Now().UTC()
	job := Job{ID: id, Request: r, State: "running", Phase: "preflight", CreatedAt: now, UpdatedAt: now}
	if err := s.Store.SaveJob(ctx, job); err != nil {
		return Job{}, err
	}
	s.busy = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(s.ctx, time.Duration(settings.TimeoutMins)*time.Minute)
		defer cancel()
		state, message, err := s.execute(ctx, &job, source, settings)
		if err != nil {
			state, message = "failed", err.Error()
			s.Logger.Error("live operation failed", "job_id", job.ID, "action", r.Action, "error", err)
		}
		job.State, job.Phase, job.Message = state, "finished", message
		recordCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := s.Store.SaveJob(recordCtx, job); err != nil {
			s.Logger.Error("cannot persist operation outcome", "job_id", job.ID, "error", err)
			s.cancel()
		}
	}()
	return job, nil
}

func (s *Service) phase(ctx context.Context, job *Job, phase string) error {
	job.Phase = phase
	return s.Store.SaveJob(ctx, *job)
}

func (s *Service) execute(ctx context.Context, job *Job, source Endpoint, settings Settings) (string, string, error) {
	if source.CollectionMode == "native_push" && job.Request.Action != "stage" {
		return s.executeNative(ctx, job, source, settings)
	}
	switch job.Request.Action {
	case "backup":
		if _, err := s.Transport.Preflight(ctx, source); err != nil {
			return "", "", err
		}
		receiver, err := s.Store.Endpoint(ctx, source.ReceiverID)
		if err != nil {
			return "", "", err
		}
		destination, err := s.Transport.SSH(ctx, source, "ghe-config backup.remote-archive-destination-host")
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(destination) != receiver.Host {
			return "", "", fmt.Errorf("native remote-archive destination does not match registered receiver %s; configure it manually using GitHub documentation", receiver.Host)
		}
		if err := s.phase(ctx, job, "native_backup"); err != nil {
			return "", "", err
		}
		if _, err := s.Transport.SSH(ctx, source, "ghe-backup"); err != nil {
			return "", "", err
		}
		return "backup_completed", "Native backup command completed. Receiver synchronization is asynchronous and has NOT been verified. Confirm completion and pause writers before collecting.", nil
	case "collect":
		return s.collect(ctx, job, source, settings)
	case "stage":
		return s.stage(ctx, job, settings)
	}
	return "", "", fmt.Errorf("unknown action")
}

func (s *Service) dataPath(source, collection string) (string, error) {
	if !idPattern.MatchString(source) || !idPattern.MatchString(collection) {
		return "", fmt.Errorf("invalid archive identity")
	}
	return filepath.Join(s.ArchiveRoot, source, collection, "data"), nil
}

func (s *Service) collect(ctx context.Context, job *Job, source Endpoint, settings Settings) (string, string, error) {
	receiver, err := s.Store.Endpoint(ctx, source.ReceiverID)
	if err != nil {
		return "", "", err
	}
	if _, err := s.Transport.Preflight(ctx, receiver); err != nil {
		return "", "", err
	}
	if err := s.checkCollectionOwnership(ctx, receiver); err != nil {
		return "", "", err
	}
	before, err := s.Transport.SSH(ctx, receiver, receiverTimestamp)
	if err != nil {
		return "", "", err
	}
	if !validTimestamp(strings.TrimSpace(before)) {
		return "", "", fmt.Errorf("receiver current symlink must name a native timestamp directly")
	}
	job.Request.Timestamp = strings.TrimSpace(before)
	data, err := s.dataPath(source.ApplianceID, job.ID)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		return "", "", err
	}
	if err := s.phase(ctx, job, "receiver_transfer"); err != nil {
		return "", "", err
	}
	if _, err := s.Transport.Rsync(ctx, receiver, data, "/data/backup/data", true, false, settings.BandwidthKiB); err != nil {
		return "", "", err
	}
	if err := s.phase(ctx, job, "checksum_verification"); err != nil {
		return "", "", err
	}
	diff, err := s.Transport.Rsync(ctx, receiver, data, "/data/backup/data", true, true, settings.BandwidthKiB)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(diff) != "" {
		return "", "", fmt.Errorf("receiver and collected tree differ; source may have changed or metadata was not preserved")
	}
	after, err := s.Transport.SSH(ctx, receiver, receiverTimestamp)
	if err != nil {
		return "", "", err
	}
	if before != after {
		return "", "", fmt.Errorf("receiver current changed during collection")
	}
	if err := s.checkCollectionOwnership(ctx, receiver); err != nil {
		return "", "", err
	}
	if err := validateTree(data); err != nil {
		return "", "", err
	}
	return "collected_unverified", "Full receiver history copied and checksum-compared. This is experimental file collection, NOT GHES restore qualification. Native remote-sync completion relied on operator attestation.", nil
}

func validateTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("unresolved backup symlink %s: %w", path, err)
			}
			relative, err := filepath.Rel(root, resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("backup symlink escapes collected tree: %s", path)
			}
		} else if !entry.IsDir() && entry.Type() != 0 {
			return fmt.Errorf("unsupported special file in backup: %s", path)
		}
		return nil
	})
}

func (s *Service) stage(ctx context.Context, job *Job, settings Settings) (state, message string, resultErr error) {
	r := job.Request
	target, err := s.Store.Endpoint(ctx, r.TargetID)
	if err != nil {
		return "", "", err
	}
	data, err := s.dataPath(r.SourceID, r.CollectionID)
	if err != nil {
		return "", "", err
	}
	if err := validateTree(data); err != nil {
		return "", "", err
	}
	selected, err := os.Lstat(filepath.Join(data, r.Timestamp))
	if err != nil || !selected.IsDir() || selected.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("selected timestamp is not a directory in the retained collection")
	}
	if _, err := s.Transport.Preflight(ctx, target); err != nil {
		return "", "", err
	}
	if err := s.checkTargetOwnership(ctx, target, data); err != nil {
		return "", "", err
	}
	stage := "/data/backup/backupfabric-" + job.ID
	lock := "/data/backup/.backupfabric-staging-lock"
	prepare := `set -eu
test ! -e /data/user/common/backup_utils_in_progress
test -d /data/backup/data
test ! -L /data/backup/data
test -z "$(find /data/backup/data -mindepth 1 -maxdepth 1 -print -quit)"
mkdir -- ` + lock + `
`
	if _, err := s.Transport.SSH(ctx, target, prepare); err != nil {
		return "", "", fmt.Errorf("target must have an empty backup root and no staging lock: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := s.Transport.SSH(cleanupCtx, target, "rmdir -- "+lock); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("release target staging lock: %w", err))
		}
	}()
	if _, err := s.Transport.SSH(ctx, target, "mkdir -- "+stage); err != nil {
		return "", "", err
	}
	if err := s.phase(ctx, job, "target_transfer"); err != nil {
		return "", "", err
	}
	if _, err := s.Transport.Rsync(ctx, target, data, stage, false, false, settings.BandwidthKiB); err != nil {
		return "", "", err
	}
	if err := s.phase(ctx, job, "checksum_verification"); err != nil {
		return "", "", err
	}
	diff, err := s.Transport.Rsync(ctx, target, data, stage, false, true, settings.BandwidthKiB)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(diff) != "" {
		return "", "", fmt.Errorf("target staging tree differs from retained collection")
	}
	publish := `set -eu
test ! -e /data/user/common/backup_utils_in_progress
test ! -L /data/backup/data
test -d /data/backup/data
test -z "$(find /data/backup/data -mindepth 1 -maxdepth 1 -print -quit)"
mv -T -- ` + stage + ` /data/backup/data`
	if _, err := s.Transport.SSH(ctx, target, publish); err != nil {
		return "", "", fmt.Errorf("staged data was not published: %w", err)
	}
	return "staged_unverified", "History staged at /data/backup/data. Native restore has NOT run. Follow GitHub's maintenance-mode and version requirements, then run ghe-restore -s " + r.Timestamp + " on the target. Record a complete rehearsal before trusting recoverability.", nil
}

func (s *Service) checkCollectionOwnership(ctx context.Context, endpoint Endpoint) error {
	if os.Geteuid() == 0 {
		return nil
	}
	groups, err := os.Getgroups()
	if err != nil {
		return fmt.Errorf("read local groups: %w", err)
	}
	groups = append(groups, os.Getegid())
	conditions := make([]string, 0, len(groups))
	for _, group := range groups {
		conditions = append(conditions, "! -gid "+strconv.Itoa(group))
	}
	command := "find /data/backup/data \\( ! -uid " + strconv.Itoa(os.Geteuid()) +
		" -o \\( " + strings.Join(conditions, " -a ") + " \\) \\) -printf 'FOREIGN_OWNERSHIP\\n' -quit"
	output, err := s.Transport.SSH(ctx, endpoint, command)
	if err != nil {
		return fmt.Errorf("verify receiver ownership: %w", err)
	}
	if strings.TrimSpace(output) != "" {
		return fmt.Errorf("receiver files have ownership the non-root collector cannot preserve; collection refused instead of silently changing UID/GID")
	}
	return nil
}

func (s *Service) checkTargetOwnership(ctx context.Context, endpoint Endpoint, root string) error {
	output, err := s.Transport.SSH(ctx, endpoint, "id -u; id -G")
	if err != nil {
		return fmt.Errorf("read target transfer identity: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		return fmt.Errorf("unexpected target identity response")
	}
	uid, err := strconv.ParseUint(strings.TrimSpace(lines[0]), 10, 32)
	if err != nil {
		return fmt.Errorf("invalid target numeric UID: %w", err)
	}
	groups := make(map[uint64]bool)
	for _, value := range strings.Fields(lines[1]) {
		group, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid target numeric group: %w", err)
		}
		groups[group] = true
	}
	if len(groups) == 0 {
		return fmt.Errorf("target identity has no numeric groups")
	}
	if uid == 0 {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("filesystem does not expose numeric ownership")
		}
		if uint64(stat.Uid) != uid || !groups[uint64(stat.Gid)] {
			return fmt.Errorf("target admin cannot preserve retained numeric UID/GID for %s; staging refused", path)
		}
		return nil
	})
}
