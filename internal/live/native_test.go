package live

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const fixtureUUID = "11111111-1111-1111-1111-111111111111"
const fixtureTimestamp = "20261008T010203"

func nativeFixture(t *testing.T) (*Service, Endpoint, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "data", fixtureTimestamp)
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"version": "v3.21.7\n", "uuid": fixtureUUID + "\n"} {
		if err := os.WriteFile(filepath.Join(snapshot, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(fixtureTimestamp, filepath.Join(root, "data", "current")); err != nil {
		t.Fatal(err)
	}
	source := Endpoint{ApplianceID: "app_11111111111111111111111111111111", Host: "source.example", Role: "source", CollectionMode: "native_push"}
	receiver := NativeReceiver{Root: root, DestinationHost: "receiver.example", SourceUUID: fixtureUUID, Route: "source-a"}
	service := &Service{nativeReceivers: map[string]NativeReceiver{source.Host: receiver}}
	return service, source, snapshot
}

func TestNativeInventoryPublication(t *testing.T) {
	service, source, path := nativeFixture(t)
	snapshot, err := service.currentNativeSnapshot(context.Background(), source)
	if err != nil || snapshot.Version != "v3.21.7" || snapshot.UUID != fixtureUUID || !snapshot.Current || snapshot.State != "received_unverified" {
		t.Fatalf("unexpected publication: %+v, %v", snapshot, err)
	}
	if err := os.WriteFile(filepath.Join(path, "incomplete"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.currentNativeSnapshot(context.Background(), source); err == nil {
		t.Fatal("accepted incomplete publication")
	}
}

func TestNativeRejectsUntrustedMetadata(t *testing.T) {
	for _, name := range []string{"wrong_uuid", "invalid_version", "oversized", "symlink", "fifo", "directory"} {
		t.Run(name, func(t *testing.T) {
			service, source, path := nativeFixture(t)
			metadata := filepath.Join(path, "version")
			var err error
			switch name {
			case "wrong_uuid":
				err = os.WriteFile(filepath.Join(path, "uuid"), []byte("22222222-2222-2222-2222-222222222222"), 0o600)
			case "invalid_version":
				err = os.WriteFile(metadata, []byte("harmless nonsense metadata"), 0o600)
			case "oversized":
				err = os.WriteFile(metadata, []byte(strings.Repeat("x", 513)), 0o600)
			default:
				if err = os.Remove(metadata); err != nil {
					t.Fatal(err)
				}
				switch name {
				case "symlink":
					err = os.Symlink("uuid", metadata)
				case "fifo":
					err = unix.Mkfifo(metadata, 0o600)
				case "directory":
					err = os.Mkdir(metadata, 0o700)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.currentNativeSnapshot(context.Background(), source); err == nil {
				t.Fatal("accepted untrusted native metadata")
			}
		})
	}
}

func TestNativeConfigAndRebinding(t *testing.T) {
	service, source, _ := nativeFixture(t)
	receiver := service.nativeReceivers[source.Host]
	config := filepath.Join(t.TempDir(), "receivers.json")
	value, err := json.Marshal(map[string]NativeReceiver{source.Host: receiver})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, value, 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := &Service{}
	if err := fresh.ConfigureNativeReceivers(config); err != nil {
		t.Fatal(err)
	}
	if err := fresh.ConfigureNativeReceivers(config); err == nil {
		t.Fatal("accepted runtime route rebinding")
	}
	source.NativeRoute = "source-b"
	if _, err := fresh.nativeReceiver(source); err == nil {
		t.Fatal("accepted changed persisted route")
	}
	duplicate := map[string]NativeReceiver{source.Host: receiver, "other.example": receiver}
	value, _ = json.Marshal(duplicate)
	if err := os.WriteFile(config, value, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&Service{}).ConfigureNativeReceivers(config); err == nil {
		t.Fatal("accepted shared receiver root/identity")
	}
}
