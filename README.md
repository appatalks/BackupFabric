# BackupFabric

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

Collection success means only that a storage snapshot was recorded. It does
not mean that a backup is restore-qualified.

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

### Experimental live GHES workflows

The GUI supports SSH preflight, running a native backup on a registered source,
collecting a dedicated GHES receiver's completed history, and rsync staging to
an empty restore target. Host keys are strictly pinned and keys are mounted
read-only. These integrations have not been qualified on real GHES; metadata
permission failures are explicit and never silently relaxed.

See [live workflow setup and limitations](docs/deployment/live-workflows.md).

Generate mock backup trees:

```bash
go run ./cmd/backupfabric-mock --root ./var/mock
```

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
