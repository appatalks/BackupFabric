# GHES 3.21 integration and restore plan

## Environment

Use isolated, non-production infrastructure with three GHES 3.21 sources, one
compatible restore target, dedicated backup volumes, and a provider account
limited to snapshot/clone operations on those volumes.

## Qualification sequence

1. Register every source using its stable appliance UUID, pinned SSH host key,
   and unique provider volume ID.
2. Seed distinguishable repositories, LFS objects, releases, Actions data,
   users, settings, and secrets on each source.
3. Create a full native backup and two incrementals per source.
4. During each run, capture command status, backup history, logs, in-progress
   marker state, `current`, GHES version, and volume identity.
5. Snapshot each volume through BackupFabric. Verify no provider snapshot or
   catalog record crosses source identity.
6. Exercise interrupted provider requests, timeout, service restart, duplicate
   request, concurrent sources, and retry. Confirm per-source serialization.
7. Materialize a writable clone from each source's latest snapshot.
8. Attach each clone in turn to a clean, compatible restore target and use the
   documented existing-backup-disk procedure.
9. Run `ghe-restore -s` for the chosen timestamp and complete Management
   Console finalization.
10. Validate repositories, object counts, LFS, releases, Actions artifacts,
    users, authentication configuration, and source-specific sentinel data.
11. Record target version, commands, timestamps, checks, logs, and operator
    approval. Only then mark the snapshot restore-qualified.

## Failure acceptance criteria

- No partially created provider snapshot appears as collected.
- Retry is idempotent and never changes source ownership.
- A failed source does not block unrelated sources.
- Service restart preserves operation and audit state.
- A snapshot from source A cannot be staged as source B without an explicit,
  audited rejection.
- The retained immutable snapshot is never attached writable; restores use a
  clone.

Repeat the complete rehearsal after every supported GHES feature-version
upgrade and every provider adapter change.
