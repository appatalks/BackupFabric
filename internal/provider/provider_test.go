package provider

import (
	"context"
	"testing"
)

func TestDevelopmentProviderIsIdempotent(t *testing.T) {
	p := NewDevelopmentProvider()
	request := SnapshotRequest{
		ApplianceID:     "app_one",
		VolumeID:        "vol-one",
		NativeTimestamp: "20261001T010203",
		IdempotencyKey:  "app_one_20261001T010203",
	}
	first, err := p.CreateSnapshot(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.CreateSnapshot(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("snapshots differ: %#v != %#v", first, second)
	}
}
