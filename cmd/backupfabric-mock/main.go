package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", "./var/mock", "mock environment root")
	flag.Parse()
	if err := generate(*root); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println("generated mock appliances at", *root)
}

func generate(root string) error {
	for index, appliance := range []string{"appliance-a", "appliance-b", "appliance-c"} {
		base := filepath.Join(root, appliance, "data")
		first := filepath.Join(base, "20261001T010203")
		second := filepath.Join(base, "20261002T010203")
		if err := os.MkdirAll(filepath.Join(first, "repositories"), 0o700); err != nil {
			return err
		}
		content := []byte(fmt.Sprintf("synthetic source %d\n", index+1))
		firstObject := filepath.Join(first, "repositories", "shared-object")
		if err := os.WriteFile(firstObject, content, 0o600); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(second, "repositories"), 0o700); err != nil {
			return err
		}
		if err := os.Link(firstObject, filepath.Join(second, "repositories", "shared-object")); err != nil {
			return err
		}
		if err := os.WriteFile(
			filepath.Join(second, "repositories", "new object with spaces"),
			[]byte("incremental\n"), 0o640,
		); err != nil {
			return err
		}
		if err := os.Symlink("20261002T010203", filepath.Join(base, "current")); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(base, "inc_snapshot_data"), []byte("synthetic\n"), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(base, ".interrupted-transfer"), []byte("resume-token\n"), 0o600); err != nil {
			return err
		}
	}
	return nil
}
