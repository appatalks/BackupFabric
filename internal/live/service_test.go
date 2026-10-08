package live_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/appatalks/backupfabric/internal/catalog"
	"github.com/appatalks/backupfabric/internal/live"
	"github.com/appatalks/backupfabric/internal/model"
)

const (
	sourceID   = "app_11111111111111111111111111111111"
	receiverID = "app_22222222222222222222222222222222"
	targetID   = "app_33333333333333333333333333333333"
	stamp      = "20261008T010203"
)

func TestNativePushEndpointValidation(t *testing.T) {
	endpoint := live.Endpoint{ApplianceID: sourceID, Role: "source", Host: "source.example", Port: 122, KeyRef: "test_key", CollectionMode: "native_push"}
	if err := live.ValidateEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*live.Endpoint){
		func(e *live.Endpoint) { e.ReceiverID = receiverID },
		func(e *live.Endpoint) { e.Port = 22 },
		func(e *live.Endpoint) { e.CollectionMode = "unexpected" },
		func(e *live.Endpoint) { e.Role = "receiver" },
	} {
		invalid := endpoint
		change(&invalid)
		if err := live.ValidateEndpoint(invalid); err == nil {
			t.Fatalf("accepted invalid native endpoint: %+v", invalid)
		}
	}
}

type fixtureRunner struct {
	mu               sync.Mutex
	roots            map[string]string
	calls            [][]string
	difference       bool
	failTransfer     bool
	foreignOwnership bool
	targetIdentity   string
}

func (r *fixtureRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
	if name == "ssh" {
		host := strings.TrimPrefix(args[len(args)-2], "admin@")
		root := r.roots[host]
		command := args[len(args)-1]
		if strings.HasPrefix(command, "rsync -aHnci") {
			if r.difference {
				return ">fcs....... changed\n", nil
			}
			return "", nil
		}
		if strings.Contains(command, "ghe-backup-remote-archive") || strings.HasPrefix(command, "set -eu; sudo -n systemctl start") {
			if r.failTransfer {
				return "", fmt.Errorf("native archive interrupted")
			}
			return "upload completed\n", nil
		}
		if strings.HasPrefix(command, "find /data/backup/data") && r.foreignOwnership {
			return "FOREIGN_OWNERSHIP\n", nil
		}
		if command == "id -u; id -G" && r.targetIdentity != "" {
			return r.targetIdentity, nil
		}
		if command == "ghe-backup" || command == "GHE_DISABLE_SSH_MUX=1 ghe-backup" {
			return "backup completed\n", nil
		}
		if strings.HasPrefix(command, "ghe-config ") {
			return "receiver.example\n", nil
		}
		command = strings.ReplaceAll(command, "mountpoint -q /data/backup", "test -d /data/backup")
		command = strings.ReplaceAll(command, "/data/backup", root)
		command = strings.ReplaceAll(command, "/data/user/common/backup_utils_in_progress", filepath.Join(root, "in_progress"))
		return (live.ExecRunner{}).Run(ctx, "sh", "-c", command)
	}
	if name == "rsync" {
		if r.failTransfer {
			return "", fmt.Errorf("simulated interrupted transfer")
		}
		localArgs := make([]string, 0, len(args))
		for i := 0; i < len(args); i++ {
			if args[i] == "-e" {
				i++
				continue
			}
			value := args[i]
			if strings.HasPrefix(value, "admin@") {
				parts := strings.SplitN(strings.TrimPrefix(value, "admin@"), ":", 2)
				value = strings.Replace(parts[1], "/data/backup", r.roots[parts[0]], 1)
			}
			localArgs = append(localArgs, value)
		}
		if r.difference && strings.Contains(strings.Join(args, " "), "--dry-run") {
			return ">fcs....... changed\n", nil
		}
		return (live.ExecRunner{}).Run(ctx, name, localArgs...)
	}
	return "", fmt.Errorf("unexpected executable %s", name)
}

func setup(t *testing.T) (*live.Service, *fixtureRunner) {
	t.Helper()
	root := t.TempDir()
	c, err := catalog.Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	for i, id := range []string{sourceID, receiverID, targetID} {
		if err := c.CreateAppliance(context.Background(), model.Appliance{
			ID: id, Name: fmt.Sprint(i), Hostname: fmt.Sprintf("host-%d", i),
			VolumeID: fmt.Sprintf("vol-%d", i), State: "enabled", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	store, err := c.LiveStore()
	if err != nil {
		t.Fatal(err)
	}
	secrets := filepath.Join(root, "secrets")
	if err := os.Mkdir(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"test_key", "known_hosts"} {
		if err := os.WriteFile(filepath.Join(secrets, name), []byte("fixture only\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &fixtureRunner{roots: make(map[string]string)}
	for _, host := range []string{"source.example", "receiver.example", "target.example"} {
		dir := filepath.Join(root, host)
		if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
			t.Fatal(err)
		}
		runner.roots[host] = dir
	}
	tree := filepath.Join(runner.roots["receiver.example"], "data")
	if err := os.Mkdir(filepath.Join(tree, stamp), 0o700); err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(tree, stamp, "object with spaces")
	if err := os.WriteFile(object, []byte("source A sentinel\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(object, filepath.Join(tree, stamp, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stamp, filepath.Join(tree, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "inc_snapshot_data"), []byte("metadata\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, e := range []live.Endpoint{
		{ApplianceID: receiverID, Role: "receiver", Host: "receiver.example", Port: 122, KeyRef: "test_key"},
		{ApplianceID: targetID, Role: "restore", Host: "target.example", Port: 122, KeyRef: "test_key"},
		{ApplianceID: sourceID, Role: "source", Host: "source.example", Port: 122, KeyRef: "test_key", ReceiverID: receiverID},
	} {
		if err := store.SaveEndpoint(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	service, err := live.NewService(store, filepath.Join(root, "archives"), secrets, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	return service, runner
}

func startAndWait(t *testing.T, s *live.Service, request live.Request) live.Job {
	t.Helper()
	job, err := s.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stored, err := s.Store.Job(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.State != "running" {
			// Wait for the operation slot to be released after its outcome is durable.
			time.Sleep(10 * time.Millisecond)
			return stored
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return live.Job{}
}

func collectRequest() live.Request {
	return live.Request{Action: "collect", SourceID: sourceID, Confirmation: "COLLECT " + sourceID, Quiesced: true}
}

func setupNative(t *testing.T) (*live.Service, *fixtureRunner, string) {
	t.Helper()
	service, runner := setup(t)
	const nativeID = "app_44444444444444444444444444444444"
	const uuid = "11111111-1111-1111-1111-111111111111"
	root := runner.roots["receiver.example"]
	for name, value := range map[string]string{"version": "v3.21.7\n", "uuid": uuid + "\n"} {
		if err := os.WriteFile(filepath.Join(root, "data", stamp, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner.roots["native.example"] = root
	config := filepath.Join(filepath.Dir(root), "native.json")
	raw, _ := json.Marshal(map[string]live.NativeReceiver{"native.example": {Root: root, DestinationHost: "receiver.example", SourceUUID: uuid, Route: "source-a"}})
	if err := os.WriteFile(config, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureNativeReceivers(config); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(filepath.Join(filepath.Dir(root), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.CreateAppliance(context.Background(), model.Appliance{ID: nativeID, Name: "native", Hostname: "native.example", VolumeID: "native-volume", State: "enabled", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveEndpoint(context.Background(), live.Endpoint{ApplianceID: nativeID, Role: "source", Host: "native.example", Port: 122, KeyRef: "test_key", CollectionMode: "native_push"}); err != nil {
		t.Fatal(err)
	}
	return service, runner, nativeID
}

func TestNativeOperationJournal(t *testing.T) {
	service, _, source := setupNative(t)
	for _, action := range []string{"archive", "verify", "backup", "collect"} {
		job := startAndWait(t, service, live.Request{Action: action, SourceID: source, Confirmation: strings.ToUpper(action) + " " + source, Quiesced: true})
		expected := map[string]string{"archive": "received_unverified", "backup": "received_unverified", "verify": "checksum_verified_unqualified", "collect": "collected_unverified"}[action]
		if job.State != expected || job.NativeSnapshot == nil || job.NativeSnapshot.Version != "v3.21.7" || job.Request.Timestamp != stamp {
			t.Fatalf("unexpected native journal outcome: %+v", job)
		}
	}
	endpoint, err := service.Store.Endpoint(context.Background(), source)
	if err != nil || endpoint.NativeRoute != "source-a" || endpoint.NativeSourceUUID == "" {
		t.Fatalf("native binding not persisted: %+v %v", endpoint, err)
	}
	endpoint.CollectionMode = ""
	endpoint.ReceiverID = receiverID
	endpoint.NativeRoute, endpoint.NativeSourceUUID = "", ""
	if err := service.SaveEndpoint(context.Background(), endpoint); err == nil {
		t.Fatal("accepted native binding mutation")
	}
}

func TestNativeFailureIsNotReceived(t *testing.T) {
	service, runner, source := setupNative(t)
	if _, err := service.Start(context.Background(), live.Request{Action: "verify", SourceID: source, Confirmation: "VERIFY " + source}); err == nil {
		t.Fatal("accepted verification without paused writers")
	}
	runner.failTransfer = true
	job := startAndWait(t, service, live.Request{Action: "archive", SourceID: source, Confirmation: "ARCHIVE " + source})
	if job.State != "failed" || job.NativeSnapshot != nil {
		t.Fatalf("failed native archive was treated as received: %+v", job)
	}
	runner.failTransfer, runner.difference = false, true
	job = startAndWait(t, service, live.Request{Action: "verify", SourceID: source, Confirmation: "VERIFY " + source, Quiesced: true})
	if job.State != "failed" {
		t.Fatalf("checksum difference accepted: %+v", job)
	}
}

func TestNativeObservationJournal(t *testing.T) {
	service, _, source := setupNative(t)
	snapshot := live.NativeSnapshot{SourceID: source, Timestamp: stamp, Version: "v3.21.7", UUID: "11111111-1111-1111-1111-111111111111", Route: "source-a", Current: true, State: "received_unverified"}
	created, err := service.Store.ObserveNativeSnapshot(context.Background(), snapshot)
	if err != nil || !created {
		t.Fatalf("first observation failed: %v %v", created, err)
	}
	snapshot.Current = false
	created, err = service.Store.ObserveNativeSnapshot(context.Background(), snapshot)
	if err != nil || created {
		t.Fatalf("repeated observation was not idempotent: %v %v", created, err)
	}
	jobs, err := service.Store.Jobs(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].Request.Action != "discover" || jobs[0].Phase != "observed" || jobs[0].Request.Quiesced {
		t.Fatalf("unexpected observation journal: %+v %v", jobs, err)
	}
	snapshot.Version = "v3.21.8"
	if _, err := service.Store.ObserveNativeSnapshot(context.Background(), snapshot); err == nil {
		t.Fatal("accepted changed provenance under an existing snapshot identity")
	}
	snapshot.Timestamp = "20261008T020304"
	snapshot.State = "incomplete"
	if _, err := service.Store.ObserveNativeSnapshot(context.Background(), snapshot); err == nil {
		t.Fatal("journaled an incomplete snapshot")
	}
}

func TestNativeReconciliationAndRetention(t *testing.T) {
	service, runner, source := setupNative(t)
	for attempt := 0; attempt < 2; attempt++ {
		result, err := service.ReconcileNativeSnapshots(context.Background())
		if err != nil || len(result.Problems) != 0 || result.Observed != 1-attempt {
			t.Fatalf("unexpected scan: %+v %v", result, err)
		}
	}
	snapshots, err := service.NativeSnapshots(context.Background())
	if err != nil || len(snapshots) != 1 || snapshots[0].RetentionState != "pending_collection" {
		t.Fatalf("new arrival was implicitly retained: %+v %v", snapshots, err)
	}
	jobs, _ := service.Store.Jobs(context.Background())
	if len(jobs) != 1 || jobs[0].Request.Action != "discover" || jobs[0].Request.SourceID != source {
		t.Fatalf("missing durable observation: %+v", jobs)
	}
	olderPath := filepath.Join(runner.roots["native.example"], "data", "20261007T010203")
	if err := os.Mkdir(olderPath, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"version": "v3.20.9\n", "uuid": "11111111-1111-1111-1111-111111111111\n"} {
		if err := os.WriteFile(filepath.Join(olderPath, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	job := startAndWait(t, service, live.Request{Action: "collect", SourceID: source, Confirmation: "COLLECT " + source, Quiesced: true})
	if job.State != "collected_unverified" {
		t.Fatalf("collection failed: %+v", job)
	}
	snapshots, err = service.NativeSnapshots(context.Background())
	if err != nil || snapshots[0].RetentionState != "retained_unverified" || snapshots[0].RetainedJobID != job.ID {
		t.Fatalf("retained history not detected: %+v %v", snapshots, err)
	}
	if len(snapshots) != 2 || snapshots[1].Timestamp != "20261007T010203" || snapshots[1].Version != "v3.20.9" ||
		snapshots[1].RetentionState != "retained_unverified" || snapshots[1].RetainedJobID != job.ID {
		t.Fatalf("older version/timestamp in full history not recognized: %+v", snapshots)
	}
	if err := os.Remove(filepath.Join(service.ArchiveRoot, source, job.ID, "data", stamp, "version")); err != nil {
		t.Fatal(err)
	}
	snapshots, err = service.NativeSnapshots(context.Background())
	if err != nil || snapshots[0].RetentionState != "pending_collection" || snapshots[0].RetainedJobID != "" {
		t.Fatalf("missing retained metadata was accepted: %+v %v", snapshots, err)
	}
}

func TestNativeReconciliationRejectsInvalidArrivals(t *testing.T) {
	service, runner, source := setupNative(t)
	path := filepath.Join(runner.roots["native.example"], "data", stamp)
	for _, name := range []string{"incomplete", "wrong_uuid"} {
		if name == "incomplete" {
			if err := os.WriteFile(filepath.Join(path, "incomplete"), []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Remove(filepath.Join(path, "incomplete")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "uuid"), []byte("22222222-2222-2222-2222-222222222222"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		result, err := service.ReconcileNativeSnapshots(context.Background())
		jobs, jobsErr := service.Store.Jobs(context.Background())
		if err != nil || jobsErr != nil || result.Observed != 0 || len(jobs) != 0 {
			t.Fatalf("invalid %s arrival was journaled: %+v %+v %v %v", name, result, jobs, err, jobsErr)
		}
	}
	if err := os.WriteFile(filepath.Join(path, "uuid"), []byte("11111111-1111-1111-1111-111111111111"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Preflight(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	jobs, _ := service.Store.Jobs(context.Background())
	if len(jobs) != 1 || jobs[0].Request.Action != "discover" {
		t.Fatalf("preflight did not reconcile discovery: %+v", jobs)
	}
	if err := os.WriteFile(filepath.Join(path, "version"), []byte("v3.21.8"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(context.Background(), live.Request{Action: "archive", SourceID: source, Confirmation: "ARCHIVE " + source}); err == nil {
		t.Fatal("operation bypassed changed-provenance pre-check")
	}
}

func TestNativeConcurrentObservations(t *testing.T) {
	service, _, source := setupNative(t)
	snapshot := live.NativeSnapshot{SourceID: source, Timestamp: stamp, Version: "v3.21.7", UUID: "11111111-1111-1111-1111-111111111111", Route: "source-a", State: "received_unverified"}
	results := make(chan bool, 6)
	errors := make(chan error, 6)
	var workers sync.WaitGroup
	for index := 0; index < 6; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			created, err := service.Store.ObserveNativeSnapshot(context.Background(), snapshot)
			results <- created
			errors <- err
		}()
	}
	workers.Wait()
	close(results)
	close(errors)
	created := 0
	for value := range results {
		if value {
			created++
		}
	}
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if created != 1 {
		t.Fatalf("concurrent discovery created %d records", created)
	}
	if err := service.Store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if created, err := service.Store.ObserveNativeSnapshot(context.Background(), snapshot); err != nil || created {
		t.Fatalf("observation was not durable across recovery: %v %v", created, err)
	}
}

func TestNativeReconciliationEmptyReceiver(t *testing.T) {
	service, runner, _ := setupNative(t)
	root := runner.roots["native.example"]
	if err := os.Remove(filepath.Join(root, "data", "current")); err != nil {
		t.Fatal(err)
	}
	result, err := service.ReconcileNativeSnapshots(context.Background())
	if err != nil || result.Observed != 0 || len(result.Problems) != 0 {
		t.Fatalf("unpublished receiver prevented initialization: %+v %v", result, err)
	}
	if err := os.Rename(root, root+".offline"); err != nil {
		t.Fatal(err)
	}
	result, err = service.ReconcileNativeSnapshots(context.Background())
	if err != nil || result.Observed != 0 || len(result.Problems) != 1 {
		t.Fatalf("missing receiver root was silently accepted: %+v %v", result, err)
	}
}

func stageRequest(collection string) live.Request {
	return live.Request{
		Action: "stage", SourceID: sourceID, TargetID: targetID, CollectionID: collection,
		Timestamp: stamp, Confirmation: "STAGE " + targetID, Quiesced: true, CompatibleTarget: true,
	}
}

func TestActualRsyncCollectAndStage(t *testing.T) {
	s, runner := setup(t)
	backup := startAndWait(t, s, live.Request{Action: "backup", SourceID: sourceID, Confirmation: "BACKUP " + sourceID})
	if backup.State != "backup_completed" {
		t.Fatalf("%+v", backup)
	}
	collection := startAndWait(t, s, collectRequest())
	if collection.State != "collected_unverified" {
		t.Fatalf("%+v", collection)
	}
	if collection.Request.Timestamp != stamp {
		t.Fatalf("timestamp not recorded: %+v", collection)
	}
	stage := startAndWait(t, s, stageRequest(collection.ID))
	if stage.State != "staged_unverified" {
		t.Fatalf("%+v", stage)
	}
	data := filepath.Join(runner.roots["target.example"], "data")
	a, err := os.Stat(filepath.Join(data, stamp, "object with spaces"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(filepath.Join(data, stamp, "hardlink"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, b) {
		t.Fatal("hard links were not preserved")
	}
	if a.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o", a.Mode().Perm())
	}
	link, err := os.Readlink(filepath.Join(data, "current"))
	if err != nil || link != stamp {
		t.Fatalf("current = %q, %v", link, err)
	}
	for _, call := range runner.calls {
		text := strings.Join(call, " ")
		if call[0] == "ssh" && (!strings.Contains(text, "StrictHostKeyChecking=yes") || !strings.Contains(text, "BatchMode=yes")) {
			t.Fatal("SSH identity controls missing")
		}
		if strings.Contains(text, "--delete") && !strings.Contains(text, "--dry-run") {
			t.Fatal("destructive rsync option")
		}
		if strings.Contains(text, "ghe-restore") {
			t.Fatal("restore executed without separate operator action")
		}
	}
}

func TestFailedTransfersAreNotCollections(t *testing.T) {
	for _, mode := range []string{"interrupted", "changed"} {
		t.Run(mode, func(t *testing.T) {
			s, r := setup(t)
			r.failTransfer = mode == "interrupted"
			r.difference = mode == "changed"
			job := startAndWait(t, s, collectRequest())
			if job.State != "failed" {
				t.Fatalf("%+v", job)
			}
			if _, err := s.Start(context.Background(), stageRequest(job.ID)); err == nil {
				t.Fatal("failed collection allowed for staging")
			}
		})
	}
}

func TestRefusesNonemptyTarget(t *testing.T) {
	s, r := setup(t)
	job := startAndWait(t, s, collectRequest())
	path := filepath.Join(r.roots["target.example"], "data", "unrelated")
	if err := os.WriteFile(path, []byte("do not overwrite"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := startAndWait(t, s, stageRequest(job.ID))
	if staged.State != "failed" {
		t.Fatalf("%+v", staged)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "do not overwrite" {
		t.Fatal("unrelated target archive changed")
	}
}

func TestApprovalsMappingsAndRecovery(t *testing.T) {
	s, _ := setup(t)
	if _, err := s.Start(context.Background(), live.Request{Action: "collect", SourceID: sourceID}); err == nil {
		t.Fatal("missing approval accepted")
	}
	source, err := s.Store.Endpoint(context.Background(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	source.ApplianceID = targetID
	if err := s.Store.SaveEndpoint(context.Background(), source); err == nil {
		t.Fatal("role rebinding accepted")
	}
	job := live.Job{ID: "job_44444444444444444444444444444444", State: "running", CreatedAt: time.Now()}
	if err := s.Store.SaveJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, err := s.Store.Job(context.Background(), job.ID)
	if err != nil || value.State != "interrupted" {
		t.Fatalf("%+v %v", value, err)
	}
}

func TestEndpointValidation(t *testing.T) {
	s, _ := setup(t)
	e, err := s.Store.Endpoint(context.Background(), receiverID)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"-oProxyCommand=x", "host;touch /tmp/no", "user@host", "host\ncommand"} {
		e.Host = host
		if err := live.ValidateEndpoint(e); err == nil {
			t.Fatalf("unsafe host accepted: %s", host)
		}
	}
	e.Host = "receiver.example"
	e.KeyRef = "../key"
	if err := live.ValidateEndpoint(e); err == nil {
		t.Fatal("path traversal key accepted")
	}
}

func TestOwnershipMismatchFailsClosed(t *testing.T) {
	s, r := setup(t)
	collection := startAndWait(t, s, collectRequest())
	if collection.State != "collected_unverified" {
		t.Fatalf("%+v", collection)
	}
	r.targetIdentity = "1048575\n1048575\n"
	job := startAndWait(t, s, stageRequest(collection.ID))
	if job.State != "failed" || !strings.Contains(job.Message, "numeric UID/GID") {
		t.Fatalf("target ownership mismatch was not rejected: %+v", job)
	}
	if os.Geteuid() != 0 {
		r.foreignOwnership = true
		job := startAndWait(t, s, collectRequest())
		if job.State != "failed" || !strings.Contains(job.Message, "ownership") {
			t.Fatalf("receiver ownership mismatch was not rejected: %+v", job)
		}
	}
}

func TestEscapingSymlinkRejectsCollection(t *testing.T) {
	s, r := setup(t)
	outside := filepath.Join(r.roots["receiver.example"], "outside")
	if err := os.WriteFile(outside, []byte("outside tree"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(r.roots["receiver.example"], "data", "escape")); err != nil {
		t.Fatal(err)
	}
	job := startAndWait(t, s, collectRequest())
	if job.State != "failed" {
		t.Fatalf("escaping/broken symlink was accepted: %+v", job)
	}
}
