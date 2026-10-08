# GHES 3.21 backup feasibility

## Decision summary

The initial supported architecture is **external collection of a completed
native backup by snapshotting its dedicated backup volume**. The BackupFabric
control plane runs on standard Linux and uses documented GHES commands plus
infrastructure storage APIs.

Native remote archives remain an experimental compatibility track. Public GHES
3.21 documentation exposes a destination host, but no destination path or
source namespace. It requires another GHES appliance as receiver. BackupFabric
will not emulate the undocumented receiver protocol.

## Evidence

GitHub documents that:

- Native backups use a dedicated volume mounted at `/data/backup`.
- `/data/backup/data` contains timestamped restore points, a `current`
  symlink, incremental metadata, and pruning metadata.
- Unchanged repositories and file-store objects are hard-linked between
  snapshots. The filesystem must preserve hard links and symbolic links.
- Remote archives use SSH and are configured by a two-pass
  `ghe-backup-remote-add` exchange with a public key installed on a receiving
  GHES appliance.
- Configuration stores one `backup.remote-archive-destination-host`.
- Successful backups trigger remote synchronization in the background.
- Storage snapshots are preferred to remote archives when the platform
  supports them.

Public documentation does not specify the receiver account, port, host-key
policy, forced command, destination path, transfer protocol, atomic publication,
retry behavior, link preservation, pruning, or multi-source collision behavior.
The legacy open-source Backup Utilities are not the native Backup Service and
cannot answer those questions.

## Feasibility matrix

| Approach | Compatibility | Isolation | Restore viability | Complexity | Upgrade risk | Decision |
|---|---|---:|---:|---:|---:|---|
| Identity-aware SSH routing | Undocumented receiver emulation | High in theory | Unproven | High | Critical | Reject initially |
| Session mount namespaces/bind mounts | Requires appliance session hooks | High in theory | Unproven | Very high | Critical | Reject |
| Dedicated GHES receiver and volume per source | Documented topology | High | Plausible; must rehearse | High cost | Medium | Supported fallback after validation |
| Snapshot completed native backup volume | Uses documented disk model and provider API | High | Strongest documented path via cloned disk | Medium | Low | Initial mode |
| File-level collection | Not documented as native workflow | Depends on exact copy semantics | Unproven | Medium | High | Research only |

## Native layout and lifecycle

The documented root resembles:

```text
/data/backup/data/
├── 20261001T010203/
├── 20261002T010203/
├── current -> 20261002T010203
├── inc_full_backup
├── inc_snapshot_data
├── inc_previous_*
└── prune_*
```

A collection consistency gate should record:

1. Successful completion of `ghe-backup`, or successful backup history when
   GHES scheduling is used.
2. Absence of `/data/user/common/backup_utils_in_progress`.
3. The timestamp resolved by `current`.
4. Source appliance UUID and GHES version.
5. Dedicated volume identity.
6. Provider snapshot request and terminal state.

These observations are not documented as one atomic transaction. Backup,
pruning, and snapshot initiation must therefore be serialized per source.

## Restore path

The defensible restore path is to create a writable volume from an immutable
provider snapshot, attach it to compatible GHES, start
`ghe-backup-disk.service` for the previously initialized disk, and run
`ghe-restore -s <timestamp>`. GHES version compatibility rules still apply.

Collection is not restore qualification. Qualification requires a complete
restore rehearsal and recorded post-restore checks.

## Required vendor validation

Before native remote archives are enabled, obtain authoritative answers about:

1. SSH account, port, host-key verification, and key rotation.
2. Receiver validation performed by `ghe-backup-remote-add`.
3. Remote command, target path, and wire protocol.
4. Hard-link and symlink preservation.
5. Atomic update of `current`.
6. Partial-transfer cleanup, retry, resume, and checksums.
7. Retention and pruning behavior.
8. Support for multiple sources per receiver or disk.
9. Whether a remotely archived disk is directly attachable for restore.
10. Recommended quiescing before an infrastructure snapshot.

## Sources

- [Configure remote archives](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/configuring-remote-archives-for-backups)
- [Configure the Backup Service](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/configuring-the-backup-service)
- [Snapshot file structure](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/understanding-the-snapshot-file-structure)
- [Create and monitor backups](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/creating-and-monitoring-backups)
- [Restore from a backup](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/restoring-from-a-backup)
- [Backup settings reference](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/backup-service-settings-reference)
- [Legacy Backup Utilities](https://github.com/github/backup-utils)
