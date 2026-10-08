package live

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Runner interface {
	Run(context.Context, string, ...string) (string, error)
}

type ExecRunner struct{}

type boundedOutput struct {
	bytes.Buffer
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 64*1024 - b.Len(); remaining < n {
		b.exceeded = true
		if remaining > 0 {
			b.Buffer.Write(p[:remaining])
		}
	} else {
		b.Buffer.Write(p)
	}
	return n, nil
}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if err != nil {
		return output.String(), fmt.Errorf("%s failed: %w: %s", name, err, output.String())
	}
	if output.exceeded {
		return output.String(), fmt.Errorf("%s output exceeded 64 KiB; refusing incomplete verification", name)
	}
	return output.String(), nil
}

type Transport struct {
	Runner     Runner
	SecretsDir string
}

func (t Transport) sshArgs(e Endpoint) ([]string, error) {
	if err := ValidateEndpoint(e); err != nil {
		return nil, err
	}
	key := filepath.Join(t.SecretsDir, e.KeyRef)
	hosts := filepath.Join(t.SecretsDir, "known_hosts")
	for _, path := range []string{key, hosts} {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("read SSH secret %s: %w", filepath.Base(path), err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("SSH secrets must be regular files, not symlinks")
		}
		if info.Mode().Perm()&0o022 != 0 {
			return nil, fmt.Errorf("SSH secret %s must not be group/world writable", filepath.Base(path))
		}
		if path == key && info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("private key permissions must be 0600 or stricter")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("SSH secret is not readable by service user: %w", err)
		}
		if _, err := io.Copy(io.Discard, io.LimitReader(file, 1)); err != nil {
			file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	return []string{
		"-F", "/dev/null", "-p", strconv.Itoa(e.Port), "-i", key,
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + hosts,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3", "-o", "ForwardAgent=no",
		"-o", "ClearAllForwardings=yes", "-o", "UpdateHostKeys=no",
	}, nil
}

func (t Transport) SSH(ctx context.Context, e Endpoint, command string) (string, error) {
	args, err := t.sshArgs(e)
	if err != nil {
		return "", err
	}
	args = append(args, "--", "admin@"+e.Host, command)
	return t.Runner.Run(ctx, "ssh", args...)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (t Transport) Rsync(ctx context.Context, e Endpoint, local, remote string, pull, verify bool, bandwidth int) (string, error) {
	ssh, err := t.sshArgs(e)
	if err != nil {
		return "", err
	}
	command := "ssh"
	for _, arg := range ssh {
		command += " " + shellQuote(arg)
	}
	args := []string{"-aHAX", "--numeric-ids", "--protect-args", "--timeout=120", "-e", command}
	if verify {
		// Delete is dry-run only, to detect extra entries without removing anything.
		args = append(args, "--dry-run", "--checksum", "--delete", "--itemize-changes")
	}
	if bandwidth > 0 {
		args = append(args, "--bwlimit="+strconv.Itoa(bandwidth))
	}
	host := e.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	remoteSpec := "admin@" + host + ":" + remote + "/"
	local = filepath.Clean(local) + "/"
	if pull {
		args = append(args, "--", remoteSpec, local)
	} else {
		args = append(args, "--", local, remoteSpec)
	}
	return t.Runner.Run(ctx, "rsync", args...)
}

const readiness = `set -eu
test ! -e /data/user/common/backup_utils_in_progress
mountpoint -q /data/backup
test -d /data/backup/data
test ! -L /data/backup/data
command -v rsync >/dev/null
df -Pk /data/backup
`

const receiverTimestamp = `set -eu
test ! -e /data/user/common/backup_utils_in_progress
test -L /data/backup/data/current
readlink /data/backup/data/current
`

func (t Transport) Preflight(ctx context.Context, e Endpoint) (Preflight, error) {
	evidence, err := t.SSH(ctx, e, readiness)
	if err != nil {
		return Preflight{}, fmt.Errorf("SSH/storage preflight: %w", err)
	}
	return Preflight{
		EndpointID: e.ApplianceID, Role: e.Role, Evidence: evidence,
		Warning: "SSH identity is pinned, backup disk is mounted, and no local backup marker is present. This does not prove native remote-sync completion, permissions for all metadata, version compatibility, or sufficient transfer capacity. Quiesce remote writers before collection/staging.",
	}, nil
}
