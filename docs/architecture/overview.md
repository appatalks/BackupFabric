# Architecture

## Deployment model

BackupFabric is an independently deployable Linux service. Ubuntu 24.04 in a
Docker container is the initial supported packaging and runtime. The control
plane must not depend on packages, files, accounts, or processes that exist
only inside a GHES appliance.

| Plane | Owner | Responsibilities |
|---|---|---|
| Control | BackupFabric on Linux | Registration, scheduling, catalog, audit, policy, storage APIs |
| Backup data | GHES | Create and restore the native backup format |
| Infrastructure | Storage provider | Crash-consistent snapshots, clones, encryption, replication |

The current boundary is represented by:

- The live transport executes fixed SSH preflight/native-backup commands and
  rsync operations against registered endpoints.
- The `Provider` interface creates snapshot references; infrastructure
  inspection/materialization adapters are future work.
- The catalog stores provider references and evidence, not a reinterpreted
  copy of the native backup tree.

## Identity and isolation

An administrator registers each source and binds it to a trusted hostname and
storage volume. BackupFabric generates an immutable internal identifier. User
input is never used as a filesystem path.

Every snapshot record is keyed by the internal appliance identifier, appliance
UUID when available, native snapshot timestamp, and provider snapshot ID. One
storage volume may belong to only one enabled appliance.

## Collection state machine

```text
requested -> checking -> snapshotting -> collected
     |            |             |
     +------------+-------------+-> failed

collected -> structurally_valid -> restore_qualified
```

The states are intentionally distinct. A provider reporting a completed volume
snapshot proves neither native structure validity nor successful restoration.
Only a completed rehearsal on compatible GHES can set `restore_qualified`.

Collection for one source is serialized with backup and prune activity.
Different sources may run concurrently within configured resource limits.

## Restore staging

Restore staging is an explicit operator workflow:

1. Select a restore-qualified or deliberately overridden snapshot.
2. Materialize a new writable volume; never modify the retained snapshot.
3. Attach the volume to a compatible non-production GHES target.
4. Mount it using the documented existing-backup-disk procedure.
5. Run `ghe-restore -s <timestamp>`.
6. Validate the restored appliance and record evidence.

Block clone attachment and native restore execution remain future/manual
work. An alternate experimental file-based workflow now collects full
histories from a dedicated GHES receiver per source and stages them using
rsync onto an empty target backup root. Volume clones are not required for
that staging path. See [live workflows](../deployment/live-workflows.md).

File collection uses separate job/source directories and fail-closed rsync
metadata preservation. It does not provision logical volumes or replace the
block-snapshot canonical production design without GHES qualification.

## Container boundary

The supported container runs as an unprivileged user with a read-only root
filesystem and a persistent catalog volume. It needs outbound access to GHES
SSH and storage-provider APIs, but it does not need the Docker socket,
privileged mode, host storage mounts, or access to GHES filesystems.

Until application authentication and TLS are implemented, the container uses
Linux host networking and the service remains bound to host loopback.

## Persistence

SQLite is the initial catalog. It provides transactions, crash recovery,
constraints, and simple single-host operations. The service configures foreign
keys, WAL mode, and a busy timeout. Provider credentials are references to an
external secret store and must not be stored in catalog columns or logs.

## API and UI

The versioned JSON API is `/api/v1`. The CLI and embedded web interface consume
the same API. Mutating workflows will require authentication, authorization,
and audit records before production use; the current unauthenticated local
interface must remain bound to loopback.
