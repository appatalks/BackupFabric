package live_test

import (
	"context"
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
		if strings.HasPrefix(command, "find /data/backup/data") && r.foreignOwnership {
			return "FOREIGN_OWNERSHIP\n", nil
		}
		if command == "id -u; id -G" && r.targetIdentity != "" {
			return r.targetIdentity, nil
		}
		if command == "ghe-backup" {
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
