package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

type route struct {
	Container string `json:"container"`
	User      string `json:"user"`
}

var routeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func receiverArgs(routes map[string]route, source, command string) ([]string, error) {
	receiver, exists := routes[source]
	if !routeName.MatchString(source) || !exists || !routeName.MatchString(receiver.Container) {
		return nil, fmt.Errorf("unknown or invalid receiver identity")
	}
	if command == "" || len(command) > 16384 {
		return nil, fmt.Errorf("an explicit bounded remote command is required")
	}
	parts := strings.Split(receiver.User, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("receiver user must specify a non-root numeric UID:GID")
	}
	for _, part := range parts {
		value, err := strconv.ParseUint(part, 10, 32)
		if err != nil || value == 0 || value == 4294967295 {
			return nil, fmt.Errorf("receiver UID/GID must be valid non-root identities")
		}
	}
	return []string{"exec", "-i", "--user", receiver.User, "--workdir", "/data/backup", "--",
		receiver.Container, "/bin/bash", "-c", command}, nil
}

func run() error {
	flags := flag.NewFlagSet("backupfabric-native-lab", flag.ContinueOnError)
	config := flags.String("config", "/etc/backupfabric-native-lab/routes.json", "root-owned receiver route configuration")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if os.Geteuid() != 0 || flags.NArg() != 2 {
		return fmt.Errorf("root broker requires fixed source identity and remote command")
	}
	info, err := os.Lstat(*config)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("route configuration must be a root-owned immutable regular file")
	}
	file, err := os.Open(*config)
	if err != nil {
		return err
	}
	defer file.Close()
	var routes map[string]route
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&routes); err != nil {
		return err
	}
	args, err := receiverArgs(routes, flags.Arg(0), flags.Arg(1))
	if err != nil {
		return err
	}
	command := exec.Command("/usr/bin/docker", args...)
	command.Env = []string{"PATH=/usr/bin:/bin", "DOCKER_HOST=unix:///var/run/docker.sock"}
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		return err
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
