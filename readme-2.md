# BackupFabric Technical Guide

For the short introduction and quick start, see [README.md](README.md).

BackupFabric is a Linux-native control plane for orchestrating, cataloging, and
managing storage for GitHub Enterprise Server (GHES) native backups.

> **Status:** experimental foundation. BackupFabric is not production-ready and
> has not yet been qualified with a complete restore rehearsal on GHES 3.21.

## Architectural boundary

BackupFabric runs on a standard Linux host, with Ubuntu 24.04 in Docker as the
initial supported deployment envelope. Its API, CLI, catalog, scheduler, and
storage-provider integrations do not run inside a GHES appliance. GHES remains
the vendor-supported data plane that creates and restores native backup volumes.

```text
                       standard Linux host
                 +-----------------------------+
 administrators -> API / CLI / web UI          |
                 | registry + catalog + audit  |
                 | orchestration               |
                 | storage-provider adapters   |
                 +---------------+-------------+
                                 |
                   documented SSH| infrastructure APIs
                                 |
              +------------------+------------------+
              |                  |                  |
          GHES source A      GHES source B      GHES source C
          /data/backup       /data/backup       /data/backup
              |                  |                  |
              +---------- immutable volume snapshots -------->
                         isolated shared storage
```

The initial supported design is **externally orchestrated native backup with
block-snapshot collection**. BackupFabric waits for a completed native backup,
then asks the infrastructure provider to snapshot the source's dedicated backup
volume. It never merges backup roots from different appliances.

Native GHES remote archives are not treated as a generic SSH protocol. The
documented receiver is another GHES appliance, and no per-source destination
path is exposed. See [the feasibility analysis](docs/architecture/feasibility.md).

## Current foundation

- Versioned HTTP API and embedded management UI
- CLI for server operation, health, appliance registration, backup inventory,
  storage inspection, and diagnostics
- SQLite appliance and backup catalog
- Explicit source identity and storage-volume mappings
- Storage-provider interface with a deterministic development provider
- Per-appliance orchestration locks and auditable backup state transitions
- Three-source synthetic filesystem generator
- Guided GUI setup, persistent transfer settings, dedicated receiver mappings,
  and explicitly approved live SSH/rsync operations
- Durable live operation journal; native restore execution remains manual
- Experimental isolated native-push inventory with per-snapshot identity/version,
  approved archive/checksum operations, and retained histories

Collection success means only that a storage snapshot was recorded. It does
not mean that a backup is restore-qualified.

## Deployment direction

Ubuntu 24.04 LTS is the recommended host baseline. The goal is a repeatable
Linux installation with deployment-provided storage, identities, and keys,
not a dependency on the appliances used during development.

The control plane can run as a Go binary or container. The current experimental
multi-source native-push gateway requires Docker Engine for per-source receiver
isolation. A container-free gateway is not implemented. Receiver containers
provide consistent Linux tools across host distributions; they do not remove
host kernel, storage, architecture, or security-policy requirements.

Planned packaging includes prebuilt Linux amd64/arm64 binaries and matching
receiver images, plus one initialization workflow for prerequisite checks,
isolated storage, trusted key enrollment, SSH 122, and service installation.
Customers should not need Go or build images during routine installation.
These release artifacts and the automated installer are not available yet.

Distribution/architecture support must be verified, not inferred from being
Linux. The intended first qualification targets are Ubuntu LTS, followed by
Debian and RHEL-family hosts. Minimal/embedded distributions, rootless engines,
alternative container runtimes, and non-systemd startup remain unqualified.
See [the roadmap](docs/roadmap.md) for the deployment work.

## Build and test

Requirements: Go 1.23 or newer and a Linux host.

```bash
go test ./...
go build ./cmd/backupfabric
```

Run the server:

```bash
go run ./cmd/backupfabric serve \
  --listen 127.0.0.1:8080 \
  --database ./var/backupfabric.db \
  --archive-root "$(pwd)/var/archives"
```

In another terminal:

```bash
go run ./cmd/backupfabric status
go run ./cmd/backupfabric appliances add \
  --name production-a \
  --hostname ghe-a.example.com \
  --volume vol-0123456789
go run ./cmd/backupfabric appliances list
```

The CLI uses `http://127.0.0.1:8080` by default. Override it with
`--api-url` or `BACKUPFABRIC_API_URL`.

### Supported Ubuntu container

On a Linux host with Docker Engine and the Compose plugin:

```bash
docker compose up --build -d
curl --fail http://127.0.0.1:8080/api/v1/health
docker compose exec backupfabric backupfabric status
```

The supplied configuration uses Linux host networking while retaining the
application's loopback-only binding. See the
[container deployment guide](docs/deployment/docker.md).

### Remote GUI access over SSH

For a host configured with operator SSH access on port 122, run this on your
workstation, replacing `backupfabric.example.com` with your host:

```bash
ssh -N \
  -L 127.0.0.1:8088:127.0.0.1:8080 \
  -o StrictHostKeyChecking=yes \
  -o ExitOnForwardFailure=yes \
  -o ServerAliveInterval=30 \
  -p122 admin@backupfabric.example.com
```

Independently verify and enroll the host's SSH key before connecting. Use an
authorized operator key that permits forwarding to `127.0.0.1:8080`; backup
source keys must not permit forwarding.

Open **http://127.0.0.1:8088** and keep the SSH session running. Closing it
closes GUI access; rerun the command to reconnect. Local port `8088` can be
changed if occupied, including by an existing tunnel. Remote port `8080` must
match the service's loopback listener.

Only SSH port 122 needs external access. The current unauthenticated API must
remain bound to `127.0.0.1`; do not expose port 8080 to the network. This example
contains no deployment-specific hostname, credentials, or appliance identity.

### Experimental live GHES workflows

The GUI supports SSH preflight, running a native backup on a registered source,
collecting a dedicated GHES receiver's completed history, and rsync staging to
an empty restore target. Host keys are strictly pinned and keys are mounted
read-only. These integrations have not been qualified on real GHES; metadata
permission failures are explicit and never silently relaxed.

See [live workflow setup and limitations](docs/deployment/live-workflows.md).

The two-source lab exposes isolated native-push inventory and explicit
operations through the GUI. Gateway provisioning, persistent storage, and
restore qualification remain separate requirements; this is not production
receiver compatibility.

Generate mock backup trees:

```bash
go run ./cmd/backupfabric-mock --root ./var/mock
```

## Generic server backups: proposed extension

The isolated receiver model can support other backup producers, such as a
LAMP server sending database dumps and application files. This is not
implemented: current discovery expects GHES timestamp/version/UUID metadata.
Arbitrary uploads must not be passed off as GHES backups or bypass its checks.

Each approved producer would get an isolated receiver namespace; it may see
`/data/backup/data` internally, but sources must never share a writable host
root. A generic format adapter needs bounded manifests for source identity,
backup type, producer/version, timestamp, checksums, and filesystem metadata,
plus an explicit completion protocol that excludes partial uploads.

BackupFabric would catalog, verify, and retain completed generations through
that adapter. Producers remain responsible for application/database-consistent
backups, and each application supplies its own restore procedure. Receipt does
not imply recoverability or immutable protection. Trusted key enrollment,
storage provisioning, and generic discovery are still future work.

## Documentation

- [Feasibility analysis](docs/architecture/feasibility.md)
- [Architecture](docs/architecture/overview.md)
- [Threat model](docs/security/threat-model.md)
- [Unsupported and unverified behavior](docs/unsupported.md)
- [GHES integration test plan](docs/testing/ghes-integration-plan.md)
- [Roadmap](docs/roadmap.md)
- [Ubuntu container deployment](docs/deployment/docker.md)
- [Live SSH/rsync workflows](docs/deployment/live-workflows.md)
- [Architecture decisions](docs/adr/)

## License

Apache-2.0. See [LICENSE](LICENSE).
