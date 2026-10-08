package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestGeneratePreservesSyntheticFilesystemSemantics(t *testing.T) {
	root := t.TempDir()
	if err := generate(root); err != nil {
		t.Fatal(err)
	}
	for _, appliance := range []string{"appliance-a", "appliance-b", "appliance-c"} {
		base := filepath.Join(root, appliance, "data")
		target, err := os.Readlink(filepath.Join(base, "current"))
		if err != nil {
			t.Fatal(err)
		}
		if target != "20261002T010203" {
			t.Fatalf("current for %s = %q", appliance, target)
		}
		first, err := os.Stat(filepath.Join(base, "20261001T010203", "repositories", "shared-object"))
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.Stat(filepath.Join(base, "20261002T010203", "repositories", "shared-object"))
		if err != nil {
			t.Fatal(err)
		}
		firstStat := first.Sys().(*syscall.Stat_t)
		secondStat := second.Sys().(*syscall.Stat_t)
		if firstStat.Ino != secondStat.Ino || firstStat.Nlink < 2 {
			t.Fatalf("shared object for %s is not hard-linked", appliance)
		}
	}
}
