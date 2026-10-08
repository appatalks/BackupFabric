package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type NativeReceiver struct {
	Root            string `json:"root"`
	DestinationHost string `json:"destination_host"`
	SourceUUID      string `json:"source_uuid"`
	Route           string `json:"route"`
}

type NativeSnapshot struct {
	SourceID       string `json:"source_id"`
	Timestamp      string `json:"timestamp"`
	Version        string `json:"version"`
	UUID           string `json:"uuid"`
	Route          string `json:"route"`
	Current        bool   `json:"current"`
	State          string `json:"state"`
	Problem        string `json:"problem,omitempty"`
	RetentionState string `json:"retention_state,omitempty"`
	RetainedJobID  string `json:"retained_job_id,omitempty"`
}

type NativeReconciliation struct {
	Observed int      `json:"observed"`
	Skipped  bool     `json:"skipped"`
	Problems []string `json:"problems"`
}

var nativeVersion = regexp.MustCompile(`^v?[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}([.-][A-Za-z0-9.-]{1,64})?$`)
var nativeUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func (s *Service) NativeEnabled() bool {
	return len(s.nativeReceivers) > 0
}

func (s *Service) ConfigureNativeReceivers(configPath string) error {
	file, err := os.Open(configPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1048576 {
		return fmt.Errorf("native configuration must be a bounded regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1048576))
	decoder.DisallowUnknownFields()
	var receivers map[string]NativeReceiver
	if err := decoder.Decode(&receivers); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("native receiver configuration must contain one JSON object")
	}
	if len(receivers) == 0 || len(receivers) > 128 {
		return fmt.Errorf("configure between 1 and 128 native sources")
	}
	roots, routes, identities := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for host, receiver := range receivers {
		resolved, err := filepath.EvalSymlinks(receiver.Root)
		if host != strings.ToLower(host) || !hostPattern.MatchString(host) ||
			!hostPattern.MatchString(receiver.DestinationHost) || !refPattern.MatchString(receiver.Route) ||
			!nativeUUID.MatchString(receiver.SourceUUID) || !filepath.IsAbs(receiver.Root) ||
			err != nil || resolved != filepath.Clean(receiver.Root) || roots[resolved] ||
			routes[receiver.Route] || identities[receiver.SourceUUID] {
			return fmt.Errorf("invalid or overlapping native receiver configuration for %s", host)
		}
		for root := range roots {
			if strings.HasPrefix(resolved+"/", root+"/") || strings.HasPrefix(root+"/", resolved+"/") {
				return fmt.Errorf("native receiver roots must not overlap")
			}
		}
		roots[resolved], routes[receiver.Route], identities[receiver.SourceUUID] = true, true, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nativeReceivers != nil || s.busy {
		return fmt.Errorf("native routes are startup-only and cannot be rebound")
	}
	s.nativeReceivers = receivers
	return nil
}

func (s *Service) nativeReceiver(source Endpoint) (NativeReceiver, error) {
	receiver, exists := s.nativeReceivers[strings.ToLower(source.Host)]
	if !exists || source.NativeRoute != "" && source.NativeRoute != receiver.Route || source.NativeSourceUUID != "" && source.NativeSourceUUID != receiver.SourceUUID {
		return NativeReceiver{}, fmt.Errorf("source has no administrator-configured native receiver route")
	}
	return receiver, nil
}

func openNativeDirectory(parent *os.File, name string) (*os.File, error) {
	var descriptor int
	var err error
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if parent == nil {
		descriptor, err = unix.Open(name, flags, 0)
	} else {
		descriptor, err = unix.Openat(int(parent.Fd()), name, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(descriptor), name), nil
}

func readNativeMetadata(directory *os.File, name string) (string, error) {
	descriptor, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(descriptor), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("native metadata must be a regular file")
	}
	value, err := io.ReadAll(io.LimitReader(file, 513))
	if err != nil || len(value) > 512 {
		return "", fmt.Errorf("native metadata exceeds its read bound")
	}
	return strings.TrimSpace(string(value)), nil
}

func nativeCurrent(data *os.File) (string, error) {
	buffer := make([]byte, 128)
	length, err := unix.Readlinkat(int(data.Fd()), "current", buffer)
	if err != nil {
		return "", err
	}
	timestamp := string(buffer[:length])
	if !validTimestamp(timestamp) {
		return "", fmt.Errorf("native current must name a timestamp directly")
	}
	return timestamp, nil
}

func nativeSnapshot(data *os.File, source Endpoint, receiver NativeReceiver, timestamp, current string) NativeSnapshot {
	snapshot := NativeSnapshot{SourceID: source.ApplianceID, Timestamp: timestamp, Route: receiver.Route, Current: timestamp == current, State: "received_unverified"}
	directory, err := openNativeDirectory(data, timestamp)
	if err == nil {
		defer directory.Close()
		var marker unix.Stat_t
		markerErr := unix.Fstatat(int(directory.Fd()), "incomplete", &marker, unix.AT_SYMLINK_NOFOLLOW)
		if markerErr == nil {
			snapshot.State = "incomplete"
			return snapshot
		}
		if markerErr != unix.ENOENT {
			err = markerErr
		} else {
			snapshot.Version, err = readNativeMetadata(directory, "version")
			if err == nil {
				snapshot.UUID, err = readNativeMetadata(directory, "uuid")
			}
			if err == nil && (!nativeVersion.MatchString(snapshot.Version) || snapshot.UUID != receiver.SourceUUID) {
				err = fmt.Errorf("snapshot version or registered source identity is invalid")
			}
		}
	}
	if err != nil {
		snapshot.State, snapshot.Problem = "invalid", err.Error()
	}
	return snapshot
}

func (s *Service) sourceNativeSnapshots(ctx context.Context, source Endpoint) ([]NativeSnapshot, error) {
	receiver, err := s.nativeReceiver(source)
	if err != nil {
		return nil, err
	}
	root, err := openNativeDirectory(nil, receiver.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := openNativeDirectory(root, "data")
	if err != nil {
		return nil, err
	}
	defer data.Close()
	current, err := nativeCurrent(data)
	if err != nil {
		return nil, err
	}
	entries, err := data.ReadDir(10001)
	if err != nil && err != io.EOF || len(entries) > 10000 {
		return nil, fmt.Errorf("native inventory is unreadable or exceeds 10000 entries")
	}
	result := make([]NativeSnapshot, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if validTimestamp(entry.Name()) {
			result = append(result, nativeSnapshot(data, source, receiver, entry.Name(), current))
		}
	}
	after, err := nativeCurrent(data)
	if err != nil || current != after {
		return nil, fmt.Errorf("native current changed during discovery")
	}
	sort.Slice(result, func(first, second int) bool { return result[first].Timestamp > result[second].Timestamp })
	return result, nil
}

func (s *Service) NativeSnapshots(ctx context.Context) ([]NativeSnapshot, error) {
	endpoints, err := s.Store.Endpoints(ctx)
	if err != nil {
		return nil, err
	}
	jobs, err := s.Store.Jobs(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]NativeSnapshot, 0)
	for _, source := range endpoints {
		if source.CollectionMode != "native_push" {
			continue
		}
		snapshots, err := s.sourceNativeSnapshots(ctx, source)
		if err != nil {
			result = append(result, NativeSnapshot{SourceID: source.ApplianceID, State: "unavailable", Problem: err.Error()})
		} else {
			for index := range snapshots {
				snapshot := &snapshots[index]
				snapshot.RetentionState = "not_eligible"
				if snapshot.State != "received_unverified" {
					continue
				}
				snapshot.RetentionState = "pending_collection"
				for _, job := range jobs {
					retained := job.NativeSnapshot
					if job.Request.Action != "collect" || job.State != "collected_unverified" ||
						job.Request.SourceID != snapshot.SourceID || retained == nil ||
						retained.UUID != snapshot.UUID || retained.Route != snapshot.Route {
						continue
					}
					path, err := s.dataPath(snapshot.SourceID, job.ID)
					if err != nil {
						continue
					}
					data, err := openNativeDirectory(nil, path)
					if err != nil {
						continue
					}
					history := nativeSnapshot(data, source, s.nativeReceivers[strings.ToLower(source.Host)], snapshot.Timestamp, "")
					data.Close()
					if history.State == "received_unverified" && history.Version == snapshot.Version && history.UUID == snapshot.UUID {
						snapshot.RetentionState, snapshot.RetainedJobID = "retained_unverified", job.ID
						break
					}
				}
			}
			result = append(result, snapshots...)
		}
	}
	return result, nil
}

func (s *Service) reconcileNativeSource(ctx context.Context, source Endpoint) (int, error) {
	snapshots, err := s.sourceNativeSnapshots(ctx, source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			receiver, routeErr := s.nativeReceiver(source)
			if routeErr == nil {
				root, rootErr := openNativeDirectory(nil, receiver.Root)
				if rootErr == nil {
					root.Close()
					return 0, nil
				}
			}
		}
		return 0, err
	}
	observed := 0
	for _, snapshot := range snapshots {
		if snapshot.State != "received_unverified" {
			continue
		}
		created, err := s.Store.ObserveNativeSnapshot(ctx, snapshot)
		if err != nil {
			return observed, err
		}
		if created {
			observed++
		}
	}
	return observed, nil
}

func (s *Service) ReconcileNativeSnapshots(ctx context.Context) (NativeReconciliation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := NativeReconciliation{Problems: make([]string, 0)}
	if s.busy {
		result.Skipped = true
		return result, nil
	}
	endpoints, err := s.Store.Endpoints(ctx)
	if err != nil {
		return result, err
	}
	for _, source := range endpoints {
		if source.CollectionMode != "native_push" {
			continue
		}
		observed, err := s.reconcileNativeSource(ctx, source)
		result.Observed += observed
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.Problems = append(result.Problems, source.ApplianceID+": "+err.Error())
		}
	}
	return result, nil
}

func (s *Service) currentNativeSnapshot(ctx context.Context, source Endpoint) (NativeSnapshot, error) {
	snapshots, err := s.sourceNativeSnapshots(ctx, source)
	if err != nil {
		return NativeSnapshot{}, err
	}
	for _, snapshot := range snapshots {
		if snapshot.Current {
			if snapshot.State != "received_unverified" {
				return NativeSnapshot{}, fmt.Errorf("receiver current is incomplete or invalid")
			}
			return snapshot, nil
		}
	}
	return NativeSnapshot{}, fmt.Errorf("receiver has no published snapshot")
}

const nativeSourceMetadata = `set -eu
test ! -e /data/user/common/backup_utils_in_progress
test -L /data/backup/data/current
timestamp=$(readlink /data/backup/data/current)
case "$timestamp" in ????????T??????) ;; *) exit 1;; esac
case "$timestamp" in *[!0-9T]*) exit 1;; esac
test ! -e "/data/backup/data/$timestamp/incomplete"
printf '%s\n' "$timestamp"
cat "/data/backup/data/$timestamp/version"
cat "/data/backup/data/$timestamp/uuid"
`

func (s *Service) verifyNative(ctx context.Context, job *Job, source Endpoint, receiver NativeReceiver) (NativeSnapshot, error) {
	snapshot, err := s.currentNativeSnapshot(ctx, source)
	if err != nil {
		return NativeSnapshot{}, err
	}
	before, err := s.Transport.SSH(ctx, source, nativeSourceMetadata)
	if err != nil {
		return NativeSnapshot{}, err
	}
	values := strings.Fields(before)
	if len(values) != 3 || values[0] != snapshot.Timestamp || values[1] != snapshot.Version || values[2] != snapshot.UUID {
		return NativeSnapshot{}, fmt.Errorf("source current/version/identity does not match receiver")
	}
	if err := s.phase(ctx, job, "native_checksum_verification"); err != nil {
		return NativeSnapshot{}, err
	}
	path := "/data/backup/data/" + snapshot.Timestamp + "/"
	command := "rsync -aHnci --numeric-ids --out-format='%i' -e " + shellQuote("ssh -F /dev/null -p122 -i /home/admin/.ssh/id_backup -o BatchMode=yes -o StrictHostKeyChecking=yes -o ControlMaster=no -o ControlPath=none") +
		" -- " + shellQuote(path) + " " + shellQuote("admin@"+receiver.DestinationHost+":"+path)
	difference, err := s.Transport.SSH(ctx, source, command)
	if err != nil {
		return NativeSnapshot{}, err
	}
	if strings.TrimSpace(difference) != "" {
		return NativeSnapshot{}, fmt.Errorf("source/receiver checksum or archive metadata differs")
	}
	after, err := s.Transport.SSH(ctx, source, nativeSourceMetadata)
	if err != nil || before != after {
		return NativeSnapshot{}, fmt.Errorf("source changed during native verification")
	}
	final, err := s.currentNativeSnapshot(ctx, source)
	if err != nil || final != snapshot {
		return NativeSnapshot{}, fmt.Errorf("receiver changed during native verification")
	}
	job.Request.Timestamp = snapshot.Timestamp
	job.NativeSnapshot = &snapshot
	return snapshot, nil
}

func (s *Service) executeNative(ctx context.Context, job *Job, source Endpoint, settings Settings) (string, string, error) {
	receiver, err := s.nativeReceiver(source)
	if err != nil {
		return "", "", err
	}
	if job.Request.Action == "backup" || job.Request.Action == "archive" {
		if _, err := s.Transport.Preflight(ctx, source); err != nil {
			return "", "", err
		}
		destination, err := s.Transport.SSH(ctx, source, "ghe-config backup.remote-archive-destination-host")
		if err != nil || strings.TrimSpace(destination) != receiver.DestinationHost {
			return "", "", fmt.Errorf("source native archive destination does not match its configured route")
		}
		if job.Request.Action == "backup" {
			if err := s.phase(ctx, job, "native_backup"); err != nil {
				return "", "", err
			}
			if _, err := s.Transport.SSH(ctx, source, "GHE_DISABLE_SSH_MUX=1 ghe-backup"); err != nil {
				return "", "", err
			}
		}
		if err := s.phase(ctx, job, "native_archive"); err != nil {
			return "", "", err
		}
		command := "GHE_DISABLE_SSH_MUX=1 /usr/local/share/github-backup/ghe-backup-remote-archive"
		if job.Request.Action == "backup" {
			command = "set -eu; sudo -n systemctl start ghe-archive-backup.service; test \"$(systemctl show ghe-archive-backup.service -p Result --value)\" = success; test \"$(systemctl show ghe-archive-backup.service -p ExecMainStatus --value)\" = 0"
		}
		if _, err := s.Transport.SSH(ctx, source, command); err != nil {
			return "", "", err
		}
		snapshot, err := s.currentNativeSnapshot(ctx, source)
		if err != nil {
			return "", "", err
		}
		metadata, err := s.Transport.SSH(ctx, source, nativeSourceMetadata)
		values := strings.Fields(metadata)
		if err != nil || len(values) != 3 || values[0] != snapshot.Timestamp || values[1] != snapshot.Version || values[2] != snapshot.UUID {
			return "", "", fmt.Errorf("native source completion does not match receiver publication")
		}
		job.Request.Timestamp, job.NativeSnapshot = snapshot.Timestamp, &snapshot
		return "received_unverified", "Native archive completed and receiver publication matches source identity/version. No content checksum or restore qualification is inferred.", nil
	}
	if job.Request.Action == "verify" || job.Request.Action == "collect" {
		if _, err := s.verifyNative(ctx, job, source, receiver); err != nil {
			return "", "", err
		}
		if job.Request.Action == "verify" {
			return "checksum_verified_unqualified", "Source/receiver content and archive metadata compared with stable publication. ACL/xattr and restore qualification are not established.", nil
		}
		return s.collectNative(ctx, job, source, receiver, settings)
	}
	return "", "", fmt.Errorf("unsupported native push action")
}

func (s *Service) collectNative(ctx context.Context, job *Job, source Endpoint, receiver NativeReceiver, settings Settings) (string, string, error) {
	snapshots, err := s.sourceNativeSnapshots(ctx, source)
	if err != nil {
		return "", "", err
	}
	for _, snapshot := range snapshots {
		if snapshot.State != "received_unverified" {
			return "", "", fmt.Errorf("native history contains invalid or incomplete provenance")
		}
	}
	root := filepath.Join(receiver.Root, "data")
	if err := validateTree(root); err != nil {
		return "", "", err
	}
	groups, err := os.Getgroups()
	if err != nil {
		return "", "", err
	}
	allowed := map[uint32]bool{uint32(os.Getgid()): true}
	for _, group := range groups {
		allowed[uint32(group)] = true
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Name() == "incomplete" {
			return fmt.Errorf("native history contains an incomplete snapshot")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Getuid()) || !allowed[owner.Gid] {
			return fmt.Errorf("native history ownership cannot be preserved by the service")
		}
		return nil
	}); err != nil {
		return "", "", err
	}
	data, err := s.dataPath(source.ApplianceID, job.ID)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		return "", "", err
	}
	args := []string{"-aHAX", "--numeric-ids", "--timeout=120"}
	if settings.BandwidthKiB > 0 {
		args = append(args, fmt.Sprintf("--bwlimit=%d", settings.BandwidthKiB))
	}
	if err := s.phase(ctx, job, "native_history_retention"); err != nil {
		return "", "", err
	}
	if _, err := s.Transport.Runner.Run(ctx, "rsync", append(args, "--", root+"/", data+"/")...); err != nil {
		return "", "", err
	}
	verification := append(append([]string{}, args...), "--dry-run", "--checksum", "--delete", "--itemize-changes", "--", root+"/", data+"/")
	difference, err := s.Transport.Runner.Run(ctx, "rsync", verification...)
	if err != nil || strings.TrimSpace(difference) != "" {
		return "", "", fmt.Errorf("native retained history differs or metadata copy failed")
	}
	if err := validateTree(data); err != nil {
		return "", "", err
	}
	current, err := s.currentNativeSnapshot(ctx, source)
	if err != nil || job.NativeSnapshot == nil || current != *job.NativeSnapshot {
		return "", "", fmt.Errorf("native receiver changed during retention")
	}
	return "collected_unverified", "Native receiver history retained in an isolated job root and checksum/metadata compared. This is NOT restore qualification.", nil
}
