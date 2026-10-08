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

## Two-appliance discovery baseline: 2026-10-08

This is a read-only baseline, not remote archive or restore qualification.
Both appliances report GHES 3.21.7, build `2026-10-02 0a8367ecee`.
SSH uses `admin` on port 122; passwordless sudo was verified with `sudo -n true`.


- The native receiver predicate, `test -d /data/backup && mountpoint -q /data/backup`, passed on the source and failed on the target at approximately 13:17 UTC.
- The source has `/data/backup/data` but no `current` link. A completed snapshot is not currently available through the native archive entry point.
- No source `/data/user/common/backup_utils_in_progress` marker was present at observation time. This is not proof that every writer is quiesced.
- Neither queried `backup.remote-archive-destination-host` setting produced a destination value.
- The source archive service was inactive, with its reported result `success`. This does not establish that an archive has ever run.
- The target backup-disk service was `active (exited)` despite the missing backup path. Service state alone is not a storage readiness check.

No backup, registration, transfer, disk initialization, or configuration changes
were performed on either appliance. Operator SSH access was tested; access using
the source's dedicated backup identity was not tested.

### Native implementation observations

These findings come from reading the installed 3.21.7 scripts, not executing
the complete workflows. They are version-specific implementation details,
not stable or production-qualified integration contracts.

1. `ghe-backup-remote-add` rejects a different already-configured destination,
   creates a dedicated backup key if absent, checks receiver access using
   `ghe-version`, and requires `/data/backup` to exist and be a mountpoint before
   setting `backup.remote-archive-destination-host`.
2. A successful `ghe-backup` starts `ghe-archive-backup.service` asynchronously
   when a destination is configured. Local backup completion is not archive
   completion. The service has bounded failure restart settings.
3. The service executes `/usr/local/share/github-backup/ghe-backup-remote-archive`.
   That script requires a readable source backup key, receiver SSH access, a
   source `current` link and snapshot directory, and the same receiver mount
   predicate. It also takes a nonblocking local archive lock.
4. The archive script prepares a receiver snapshot directory with an
   `incomplete` marker, pushes the current snapshot using rsync as `admin` on
   port 122 with `-ar` and `--link-dest` pointing to receiver `current`, then
   removes the marker and replaces `current` after rsync succeeds. This is not
   proof of atomic publication, full-history transfer, or preservation of all
   native metadata and hard links.
5. The default data root is `/data/backup/data`. The receiver uses the source's
   selected snapshot path; no per-source destination namespace is exposed by
   this archive invocation. Do not share a receiver root between sources.
6. No separate receiver-enable flag was observed in registration or archive
   checks. This does not rule out other appliance configuration requirements.
   Both entry points explicitly reject the target's current missing mount.
7. The backup-disk service script does nothing when `/data/backup` is absent,
   explaining the target's misleading successful service state.
8. Registration/control SSH explicitly disables strict host-key checking in
   the inspected scripts. BackupFabric must retain its pinned-host-key policy.
9. Shared backup configuration can create the data directory during command
   initialization. Do not assume that invoking a native utility or sourcing
   its configuration is a read-only discovery operation.
### Next controlled experiments
attaching/initializing disks, running backups, or transferring data. Keep

  workflow, then verify its mount, identity, capacity, and admin permissions.
- Authorize the source backup public key and complete native remote registration.
- Create a source backup with approval, observe local completion separately
  from archive completion, and inspect receiver markers and `current`.
- Test interrupted transfer/retry and incremental transfer behavior, including
  hard links, ownership, modes, symlinks, and publication consistency.
- Complete a compatible native restore rehearsal before qualification.

For BackupFabric, retain the mount gate, distinguish an unconfigured receiver
from a ready one, and never infer remote-sync completion from local backup or
systemd disk-service success. Missing-disk onboarding should guide an explicit
operator workflow rather than bypass native checks or write to the root disk.

## Approved remote archive lab setup: 2026-10-08

Following the [GHES 3.21 remote archive procedure](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/configuring-remote-archives-for-backups),
the operator approved a temporary bind-mounted receiver and the first native
registration pass. No backup or transfer was authorized in this step.

The dedicated-disk prerequisite is deliberately replaced for this experiment
by an empty `/data/user/backupfabric-lab/receiver` directory bind-mounted at
`/data/backup` on the target. This is not the documented storage topology.
`/data/backup.tmp` would have used the root filesystem (53G available), so the

Executed setup checks confirmed:

- Native receiver mount predicate: passed after the bind mount.
- Receiver write access as `admin`: passed; receiver ownership is `admin:admin`, mode `750`.
- Receiver contents: empty immediately after setup.
- Mount source: the target user-data volume, with bind root `/backupfabric-lab/receiver`.

The source then ran `ghe-backup-remote-add` for
`gheboot-appatalks-opiwwk-0.ghe-test.net`. It generated the dedicated backup
identity and stopped with `Permission denied (publickey)`, as expected before
target authorization. Only the public key and its fingerprint were read;
private key contents were not retrieved. Post-run checks confirmed that the
key files are readable and `backup.remote-archive-destination-host` remains unset.

The next step is operator authorization of the generated public key in the
target Management Console, followed by a second registration pass. Creating
a source backup or manually invoking remote sync remains a separate approved
step. No source-to-target transfer or restore has been validated yet.

### Second registration pass completed

After the operator authorized the source public key through the target
Management Console, the second `ghe-backup-remote-add` invocation succeeded.
It authenticated to the target using the dedicated backup identity, reported
the target's GHES version, accepted the bind-mounted receiver, and configured
the destination. A separate read-back confirmed
`backup.remote-archive-destination-host` equals the target hostname.

Receiver mount and admin write checks still passed; the receiver remained
empty with 159G available. The source `current` link now resolves to
`20261008T134443`, which appeared between observations and was not created by
these registration commands. Snapshot completeness and transfer size have not
yet been validated. The archive service remained inactive.

No backup or transfer was started by the assistant. Registration alone did
not populate the receiver. Future successful native backups can now trigger
background archive transfers automatically because the destination is set.
The next approved experiment can manually sync an existing completed snapshot
after checking source completion and receiver capacity.

## Authorized target deprovisioning: 2026-10-08

The operator explicitly authorized running `ghes-deprovision.sh` only on
`gheboot-appatalks-opiwwk-0.ghe-test.net`. The source was not modified during
this operation. The target hostname was checked before execution.

The script was downloaded from `appatalks/ynot` at immutable commit
`e34cc13fc13d8d20daca2f8ea4566b8ca9af15d2`, passed `bash -n`, and was run
without modifications from `/home/admin/ghes-deprovision.JH2OYn`.
Its SHA-256 was
`dc8aaef8533ed5971aabbfb773d7929bb8ace379f3c8ceb1d8f71c87ecf4f911`.
The script's interactive confirmation was accepted in the terminal.

### Execution result and live checks

The script reached its end, but deprovisioning was partial, not a clean Ubuntu
conversion. Its lack of fail-fast handling allowed it to continue after errors.

- Held packages blocked Docker removal. The bulk GHES purge failed with unavailable package names. Checks afterward confirmed `docker-ce`, `containerd.io`, `enterprise-manage`, `nomad`, and `consul` remain installed.
- The build/Docker dependency installation failed, and one APT repository was unreachable. APT sources were replaced by the script.
- MariaDB installed after aptitude removed conflicting MySQL client packages. Its initial startup failed, but later checks found it active following the script's data reinitialization.
- The GHES release marker was removed. `ghe-version` still exists but exits with status 1 because `/etc/github/enterprise-release` is missing.
- A fresh SSH connection as `admin` on port 122 and passwordless sudo still worked. The SSH service remained active and enabled.
- `/data/user` and the receiver bind mount remained mounted. Native receiver mount and admin write predicates passed; the receiver remained empty with approximately 159G available.
- `ghe-user-disk.service` was disabled but active, and `ghe-user-disk.path` was still active. Storage remains dependent on GHES components; reboot behavior was not tested.
- Consul was inactive/disabled; Nomad was failed/disabled; Docker was active/enabled. No further package cleanup or service changes were attempted.
- `dpkg --audit` produced no output. This does not negate the failed package operations or demonstrate complete deprovisioning.

### Receiver experiment implications

The inspected native archive script requires a successful receiver `ghe-version`
command before transfer. The post-deprovision target fails that command even
though its storage checks pass. Therefore the inspected archive path would
reject the receiver at that check; no archive invocation was executed to test
this prediction. Do not mistake the earlier successful registration for
post-deprovision receiver readiness.

The source destination remained configured after deprovisioning. No backup,
archive transfer, reboot, version-command shim, or independent mount/SSH
reconfiguration was performed during that operation.

## Native version-gate rejection validated: 2026-10-08

At the operator's request, one direct source invocation of
`/usr/local/share/github-backup/ghe-backup-remote-archive` at 14:46:54 UTC
exited with status 1. It reported the receiver's missing
`/etc/github/enterprise-release` file and instructed the operator to rerun
`ghe-backup-remote-add`. The systemd archive service was not started.

A separate control used the same dedicated source backup key and the installed
`ghe-ssh` wrapper with strict host-key checking to execute target `hostname`
successfully. At 14:47:17 UTC the receiver mount/write checks still passed and
the receiver remained empty. Thus SSH authentication worked, while receiver
`ghe-version` failure rejected the native archive before snapshot preparation.

The inspected gate requires a successful command exit and discards version
standard output; it does not compare version strings or prove restore
compatibility. No target command or release marker was changed in this test.

## Native push with temporary lab version response: 2026-10-08

The operator approved a native archive transfer using a temporary target-only
`ghe-version` response. The response explicitly identified a BackupFabric lab
receiver, not GHES, and returned success. It did not restore the release marker
or claim a GHES version. This is an unsupported compatibility experiment.

Preflight checked snapshot `20261008T134443`: approximately 1.4 GB, no snapshot
`incomplete` marker, and no local backup-in-progress marker. The empty receiver
had approximately 170 GB available in decimal bytes. These observations alone
do not prove that the source backup is restore-qualified.

The original target version command was preserved in the existing lab working
directory, with SHA-256
`0eedf033d79e11c6172f35fbecad330c5748a6b79964c48af36d5116a3987b07`.
Source backup-key SSH successfully executed the temporary non-GHES response.

One direct native archive invocation began at 14:48:51 UTC and logged upload
completion at 14:49:10 UTC, exiting with status 0. The first transfer warned
that the receiver `current` path did not exist for `--link-dest`; this was
nonfatal. No new source backup was created and no systemd retry loop was started.

The original target version command was restored immediately after transfer.
Byte comparison and its SHA-256 confirmed restoration. The release marker is
still absent, so subsequent native archive attempts remain subject to the
previously demonstrated version-command failure.

### Executed transfer validation

- Receiver `current` resolved to `20261008T134443`, its snapshot directory existed, and its `incomplete` marker was absent.
- Both source and receiver contained 2,603 regular files and no symlinks inside that snapshot. The receiver's separate `current` symlink was checked explicitly.
- A read-only rsync recursive checksum comparison (`-rcn`) reported no file content differences and exited successfully.
- A separate read-only comparison using `-aHnci --numeric-ids` reported no differences in the checked content, archive metadata, or hard-link relationships and exited successfully.
- Receiver capacity remained approximately 157 GiB free after transfer. All comparisons used the dedicated source backup key and strict host-key checking.

This demonstrates that the inspected native push can transfer this snapshot
to the partially deprovisioned receiver when its version-command exit gate is
temporarily satisfied. It does not establish operation on clean Ubuntu:
GHES binaries, SSH configuration, and storage services still remain. No native
restore, interrupted transfer, incremental-link reuse, reboot persistence,
ACL/extended-attribute comparison, or arbitrary-symlink case was tested here.

## Snapshot version provenance investigation: 2026-10-08

Read-only inspection confirmed that both source and receiver snapshot
`20261008T134443/version` contain `v3.21.7`. The receiver file's SHA-256 is
`de4a251ff6c2b9eae946b8cd587f1122d92f615adfc234432c88d5583f2f0d8d`.
The installed backup script writes the source version into this file near
the start of backup, before exporting data. File presence or a valid version
alone therefore cannot establish completion.

The [documented snapshot layout](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/understanding-the-snapshot-file-structure)
identifies `version` as the GHES version at backup time and `uuid` as appliance
identity. Only the version file was read for this investigation; snapshot
settings, credentials, and secret contents were not retrieved. The version
file does not supply build ID or build date; record those separately during
source discovery when available, without assuming that a later source version
describes an older snapshot.

The installed restore script reads snapshot version and rejects a target more
than two feature releases ahead. It also checks cluster topology, database
configuration, replication, and other feature prerequisites. The
[restore documentation](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/restoring-from-a-backup)
additionally prohibits restoring newer snapshots to older releases. Version
eligibility is a necessary check, not proof of complete restore compatibility.
No restore command was executed during this investigation.

### Recommended design direction

- Keep receiver identity/readiness separate from source backup version. Do not update a global appliance release marker to match incoming snapshots or claim the Ubuntu receiver is GHES.
- Associate discovered versions with the registered source and snapshot timestamp. Parse metadata as bounded untrusted data; never source a snapshot file as shell code. Preserve the original version value alongside any normalized catalog value.
- Treat filesystem notifications and an arriving version file as candidate discovery only. Catalog collection completion after coordinated transfer success, publication checks, and integrity validation; reject missing or malformed metadata explicitly.
- Compare the selected snapshot's version with the actual GHES restore target at staging/restore time using documented rules. Check additional topology and feature requirements, and reserve qualification for a successful restore rehearsal.
- For a clean Ubuntu experiment, prefer an explicit BackupFabric-controlled pull of quiesced native backup histories from a source into an isolated source-specific root, preserving required links and metadata. This avoids the native receiver's GHES version-command gate without changing the source's native scripts, but remains an unqualified file-based preservation workflow.
- Keep native push shims as explicit lab probes only. A clean Ubuntu native-push receiver would require a separately approved revision of ADR 0003 and evidence for authentication, restricted commands, publication, incremental dependencies, failure recovery, and restoration.

Next, test collection on an actual clean Ubuntu host, then a second snapshot's
incremental dependencies and interrupted-transfer handling. A compatible
GHES restore target and end-to-end restore rehearsal are still required.
Neither product code nor appliance configuration changed in this investigation.

## Next phase: two-source native push research

The operator selected continued native-push research rather than implementing
direct pull. The intended BackupFabric host is
`gheboot-appatalks-opiwwk-0.ghe-test.net`; the first production-like source is
`gheboot-appatalks-musudl-0.ghe-test.net`. The second source is not ready yet.
This choice does not make the experimental receiver production-supported or
supersede ADR 0003. No pull-mode implementation or deployment was undertaken.

Read-only host checks confirmed Ubuntu 20.04.6, Docker Engine 27.3.1, rsync,
and approximately 157 GiB free on the user-data volume. Go and the Docker
Compose plugin were not found. The app's live workflow still requires a
dedicated receiver per source; it cannot yet represent a shared native-push
gateway with independently isolated receiver namespaces.

The active admin SSH listener uses `/etc/ssh/sshd_admin_config` on port 122,
selected through `/etc/default/ssh`; default `sshd -T` settings for port 22
do not describe that listener. Operator access and native backup access share
it. GHES user-disk service/path units still manage mounted storage. Neither
SSH nor storage is independent of the surviving GHES configuration yet.

### Proposed lab architecture, not implemented

Use each source's distinct, verified backup SSH public key as its routing
identity. Bind that identity to an administrator-assigned source ID and one
isolated receiver environment. In each environment the native fixed path
`/data/backup/data` maps to that source's own host storage root. Source
timestamps, filenames, environment variables, and self-reported UUIDs must
never select another source's root.

Different SSH ports alone do not solve routing: the inspected native archive
rsync invocation hardcodes `admin` on port 122, even though control-command
SSH has more flexible host/port handling. Shared absolute paths must be
isolated for both rsync and the destination preparation/finalization shell
commands, not just rewritten for the file-copy command.

Prefer a narrowly controlled SSH gateway that dispatches authenticated source
sessions into separate unprivileged receiver containers or equivalent
isolated environments. Each receiver exposes only its own backup volume,
has no Docker socket or host secrets, and denies forwarding, PTYs, and access
to the host shell. Any privileged broker belongs outside the non-root
BackupFabric control plane and must bind a source to one fixed receiver;
it must not provide unrestricted Docker or sudo access to source sessions.
This is a design hypothesis requiring command and isolation tests, not a
claim of an implemented security boundary.

Keep the version-command admission response receiver-specific and explicitly
experimental, not a global appliance release marker. Catalog source version
per snapshot only after coordinated completion checks. Preserve the existing
`20261008T134443` transfer as baseline evidence; do not reuse its shared root
for a second source or delete it during host preparation.

### Acceptance gates and execution boundaries

1. Establish and verify independent operator SSH access before any changes to
   the port-122 listener. Confirm host-key continuity and test recovery access.
2. Verify persistent storage ownership, mounts, and boot ordering without
   removing more GHES packages or rebooting as an incidental setup step.
3. Test isolated receiver command execution and denial of cross-source access
   with harmless synthetic fixtures before routing real source credentials.
4. Route source one and verify native registration, push, checksum/metadata
   comparison, per-source publication, and snapshot version provenance.
5. When source two is ready, register its distinct key and stable identity.
   Test identical snapshot timestamps and filenames with different contents
   across both sources; neither source may overwrite or publish the other's
   history. Test concurrent sessions and unauthorized identities explicitly.
6. Exercise subsequent backups, incremental dependencies, interrupted transfer,
   retry, restart, and publication before attempting a compatible GHES restore.

Actual gateway installation and an ADR revision require approval of the
receiver isolation design. Deployment must retain the loopback-only control
API and expose remote management through a verified SSH tunnel, not an
unauthenticated public web interface. No BackupFabric service, gateway, SSH
change, or additional transfer was deployed during this planning investigation.

## Isolated receiver prototype prepared: 2026-10-08

The operator approved the lab prototype and supplied source two,
`gheboot-appatalks-juqszd-0.ghe-test.net`, running GHES 3.21.6 build
`777b52c858`. It has no `/data/backup` mount, completed snapshot, or backup key.
A temporary user-data-backed source backup and first lab backup were approved,
but no source-two changes were executed: the operator subsequently selected
container preparation/testing only while recovery access is unresolved.

### Operator access gate

A standalone `backupfabric-operator.service` and root-owned operator keyfile
were installed on the BackupFabric host, using the existing host key on port
22222. The known source-one backup key was excluded from that keyfile. Fresh
operator authentication and sudo succeeded through an SSH proxy over the
existing port-122 connection. Direct workstation access to port 22222 timed
out even though `ss` showed the listener active. This is a network-path blocker,
not evidence of working independent external recovery access.

The operator selected keeping port 122 unchanged. The original admin listener
is still active; no gateway source-key enrollment, listener takeover, source
configuration change, native backup, or native transfer occurred in this phase.

### Implemented and executed prototype

The lab broker fixes each source ID to a root-owned configured container,
rejects invalid/unregistered identities and empty/oversized commands, and
passes the remote command as one Docker argv value rather than a host shell
expression. Focused Go tests passed for two routes, malformed routes, missing
identities, empty commands, and the command-length boundary.

Two Ubuntu 24.04 receiver containers were built and started on the target:
`backupfabric-native-source-a` and `backupfabric-native-source-b`. Each has a
distinct `/data/user/backupfabric-native-lab/<source>` directory mounted at
`/data/backup`; neither uses the existing baseline receiver root. Runtime
checks confirmed user `501:501`, read-only roots, network `none`, dropped
capabilities, and no exposed Docker socket or host `/data/user` path.

The first image build failed because the default build network could not
resolve Ubuntu mirrors; rebuilding with host networking succeeded. Receiver
runtime networking remains disabled. The resulting image ID is
`57f777866bc8`; the Ubuntu base digest observed was
`sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55`.

The live shell test ran against both containers and reported PASS for
identical relative paths/timestamps with different source data, symlinks,
hard links, concurrent isolated writes, runtime restrictions, and invalid
identity/empty-command rejection. Harmless fixtures were retained at
`/data/backup/lab-fixtures/20261008T155059-2505298` inside each receiver.
These were synthetic tests, not two real GHES backup pushes or a complete
container-escape/security assessment.

The BackupFabric control-plane application has not been deployed on this host
yet, and the lab receiver is not integrated into its catalog or job journal.
Next gates are direct operator recovery connectivity, restricted source-key
SSH dispatch, source-two backup storage/creation, real two-source pushes,
incremental and failure recovery tests, then GHES restore qualification.

### Repository verification and image warning

`go vet ./...` passed. Local macOS full-suite execution failed on existing
Linux assumptions: symlinked temporary paths, then BSD `find` lacking
`-printf` after using `/private/tmp`. No archive security checks or existing
tests were changed to work around those failures.

All five test-bearing packages were cross-compiled for Linux and their test
binaries executed in the Ubuntu receiver image with read-only root and no
network. Every test passed, including real fixture rsync collection/staging,
interrupted/changed transfer rejection, ownership mismatch, escaping
symlinks, API workflows, broker routing, and the mock filesystem generator.

Editor image diagnostics flag one high-severity issue in the `ubuntu:24.04`
base image. The diagnostic did not identify a CVE here; no fix is claimed.
Image review/remediation remains a prerequisite for exposing the gateway or
calling this a production-ready receiver. The native gateway remains inactive.

## Single-port cutover and two real pushes: 2026-10-08

The operator required all external SSH to remain on port 122 and authorized
proceeding with that design. The previous extra-port recovery gate was replaced
by preservation of operator keys on the same listener plus a timed rollback.
The separate operator service was disabled, and `ss` confirmed the sole SSH
listener port was 122 on IPv4 and IPv6. Docker receivers expose no network ports.

### SSH policy activation

The active `ssh.service` still uses `/etc/ssh/sshd_admin_config`, now containing
the reviewed lab configuration with existing host keys and a root-owned
`/etc/backupfabric-native-lab/authorized_keys` file. Seven pre-existing operator
entries were preserved, including legitimate duplicates. Two distinct native
backup keys have `restrict` and source-specific forced broker commands.

The original config was saved, OpenSSH syntax checked, and a three-minute
rollback timer armed before HUP. A fresh pinned-host-key operator login and
sudo succeeded; the timer was then stopped. Port 22222 was retired without
requiring any customer network opening. Fresh operator logins also succeeded
after enrollment of the second key.

One enrollment attempt exposed a missing-newline error: source B's appended
key became part of source A's comment. Raw substring checks did not detect
that; OpenSSH parsed fingerprints did. The repaired assembler inserts explicit
separators, validates both parsed backup identities and their uniqueness,
preserves existing operator duplicates, and atomically installs the candidate.
The failed validation candidates were retained, not published. Both actual
backup keys subsequently authenticated to their fixed containers.

### Source A native push

Source A directly invoked its native archive command with SSH multiplexing
disabled, uploading `20261008T134443` from 16:05:54 to 16:06:16 UTC. Receiver A
published its own `current` and removed the incomplete marker. Its version was
`v3.21.7` and it contained 2,603 regular files. A real source-key rsync dry-run
comparison using `-aHnci --numeric-ids` reported no differences. Receiver B
and the original baseline receiver were unchanged by that push.

### Source B preparation, backup, and archive

The previously approved temporary source backup storage was created at
`/data/user/backupfabric-source-lab`, owned by `501:501` and bind-mounted at
`/data/backup`. Capacity checks showed approximately 159 GiB free. No block
device was formatted, and the bind mount is not persistent across reboot.

Native two-pass registration generated source B's dedicated key and configured
the same target hostname only after its distinct forced-key enrollment.
The receiver host-key fingerprint in source B's known_hosts matched the host's
independently read public key. Its fresh backup-key connection executed the
explicit non-GHES lab version response inside receiver B, not on the host.

The approved first `ghe-backup` created `20261008T161027`, logging completion
at 16:11:25 UTC and starting its archive in the background. A synchronous
`systemctl start ghe-archive-backup.service` awaited the operation; service
evidence afterward was `Result=success`, `ExecMainStatus=0`, and inactive.
The source backup logged no-routes warnings for repository/storage exports,
so this is empty-appliance data rather than a representative seeded workload.

Receiver B published `current -> 20261008T161027`, removed its incomplete
marker, and contained `v3.21.6`, 557 regular files, and approximately 12.7 MB
allocated. Its `-aHnci --numeric-ids` source-key comparison reported no
differences. UUID file hashes matched their respective sources and were
different between A and B. Neither receiver contained the other's snapshot
directory. The original baseline `current` was preserved.

### Actual credential isolation checks

Fresh source-key connections could not see host `/data/user`, the root-owned
keyfile/routes, host sudo, or Docker socket. Both keys were denied forwarding
to the loopback API with `administratively prohibited`. Identical harmless
paths written via the actual keys at
`/data/backup/lab-fixtures/ssh-key-collision/same-name` contained different
source-specific values in the two receivers. The synthetic fixtures remain
outside native histories. Native SSH multiplexers were retired on both idle
sources after cutover to avoid reuse of pre-policy authenticated sessions.

### Remaining qualification boundaries

This establishes two real GHES native pushes into separate Ubuntu container
namespaces using one port-122 endpoint, not production readiness. The host
still has GHES SSH startup hooks and storage remnants. Storage initialization,
boot persistence, image vulnerability review, least-privilege broker operation,
simultaneous real pushes, subsequent incremental chains, interrupted transfer,
ACL/xattr comparisons, representative seeded data, and a compatible GHES
restore remain untested. The control-plane service/catalog/UI are not yet
deployed or integrated with native receiver arrivals. Nothing was pruned.

## Control-plane integration deployed: 2026-10-08

The control plane now runs on the receiver host at loopback `127.0.0.1:8080`.
The local management URL is `http://127.0.0.1:8088` through SSH 122. Receiver
roots, startup source bindings, and secrets are read-only mounts; the catalog
and retained histories use separate writable paths. No Docker socket or host
sudo is exposed to the app.

Source profiles persist native mode, UUID, and fixed route. Startup roots must
be distinct; profile mode/route/UUID cannot be rebound. Dedicated control keys
were generated on the host and only public keys enrolled on the sources.
Private key contents were not retrieved. Known_hosts entries came from the
workstation's existing pinned records.

The first deployment failed SSH preflight because `--user 501` had no matching
passwd entry. Rebuilding with UID/GID build arguments set to 501 corrected it
without root escalation or changing native file ownership.

### Executed live API workflows

Both sources were registered and passed pinned SSH/storage preflight. Inventory
reported matching route/UUID and current `v3.21.7` and `v3.21.6` snapshots.
Missing-quiescence verification requests were rejected before job creation.
Both existing-snapshot archive jobs completed as `received_unverified` with
matching source/receiver publication metadata in the durable journal.

After the operator explicitly confirmed writer quiescence, both verify jobs
completed as `checksum_verified_unqualified`, and both full-history retention
jobs completed as `collected_unverified` after `-aHAX` copy/comparison. No new
source backup, restore, pruning, or deletion ran in this phase. Six jobs are
terminal and the restore-qualified count remains zero.

Replacing the container with the final UI image preserved all six completed
jobs, both retained histories, and both immutable source bindings. Fresh
inventory/preflight smoke checks passed after restart.

### Tests and browser evidence

All test-bearing packages passed on Linux, including legacy collection/staging
and native journal success/failure, quiescence approval, incomplete publication,
wrong UUID, malformed/oversized metadata, symlink/FIFO rejection, root overlap,
and binding immutability. Go vet and JavaScript syntax checks passed.

Browser checks confirmed both versions/routes, native action approval phrases,
topology fields, six journal rows, zero qualification, and source-filtered
retained collections. A narrow header fix separates title and health status.
Final nominal 390px/1440px contexts reported 312px/1152px layout widths under
preview zoom, without page overflow; mobile header rectangles did not overlap.
Integrated screenshot/click resizing had coordinate limitations; interaction
checks used the visible page and layout checks used reported renderer widths.

### Remaining boundaries

The control plane does not provision the privileged gateway, manage incoming
keys, pause external writers, or automate persistent mounts. Its new-backup path
was exercised in service tests, not by another real appliance backup in this
phase. Restore qualification, real staging, incremental chains, crash/boot
recovery, foreign ownership, full native ACL/xattr preservation, representative
seeded data, and production image/security review remain outstanding.

## Obsolete baseline cleanup: 2026-10-08

At the operator's request, the obsolete host `/data/backup` bind mount was
verified to reference `/data/user/backupfabric-lab/receiver`, unmounted, and
removed along with its approximately 1.4 GB baseline history and empty parent
directories. No active receiver or retained-history path was deleted.

Read-only checks confirmed both isolated receiver mounts and existing current
snapshots remained intact, both retained job-history directories existed,
and API health and two-source native inventory still passed. No backup,
transfer, restore, or pruning was initiated; the operator will test manually.

## Operator manual resend verified: 2026-10-08

After the operator ran native remote archive on both sources, target SSH logs
showed fresh authentication by both dedicated backup keys. Read-only checks
found both expected current publications and no incomplete markers. Both
source-to-receiver checksum/archive-metadata/hard-link comparisons reported
no differences, including extra-entry detection in explicit dry-run mode.
Source current links and local backup-marker absence were checked before and
after comparison. No transfer, backup, deletion, or pruning was initiated.

The control-plane API recognized source A `20261008T134443` / `v3.21.7` and
source B `20261008T161027` / `v3.21.6` on their separate routes. Resending
current reused those timestamps as expected. The journal still contained the
six prior control-plane jobs, newest created at 16:45:46 UTC. External native
commands are discovered as inventory but do not automatically produce a new
arrival/transfer audit record or refresh retained job copies. This is a known
observability gap, not proof of restore qualification.

## Fresh operator-created snapshots verified: 2026-10-08

After the operator created new native backups and manually archived both,
read-only checks at approximately 17:38 UTC found source A current
`20261008T173429` / `v3.21.7` and source B current `20261008T173434` /
`v3.21.6`. Both receiver current links matched; incomplete markers were absent
and UUID hashes matched their respective sources.

Both source-to-receiver checksum/archive-metadata/hard-link comparisons passed,
including extra-entry detection strictly in dry-run mode. Source current links
and backup-marker absence were stable before and after comparisons. Receiver
publications were checked again afterward, with no cross-source snapshot paths.

The control-plane inventory automatically discovered both new snapshots as
current and preserved the previous `20261008T134443` and `20261008T161027`
entries as non-current. The journal still held six prior jobs, newest created
at 16:45:46 UTC; external-run audit records and automatic retained-history
refresh were not inferred. No backup, transfer, collection, deletion, or
pruning was initiated by this verification. Incremental dependency completeness
and restore qualification were not established by these comparisons.

## Native discovery reconciliation deployed: 2026-10-08

Startup, UI refresh, source preflight, and native-operation pre-checks now
reconcile valid receiver snapshots into durable `discover` / `observed`
journal entries. These are metadata observations, not authenticated transfer
events or evidence of transfer origin, content integrity, or qualification.
An explicit JSON POST API and `live reconcile` CLI expose the same operation.
GET endpoints remain read-only. No transfer or collection is triggered.

The running host backfilled four source/timestamp observations alongside its
six existing operation records. Repeated scans and four concurrent live API
scans reported zero new observations. A service restart and CLI scan preserved
exactly ten total journal records with no duplicates or interrupted jobs.

The inventory and rendered UI identify the two new `173429`/`173434` snapshots
as pending collection and the two older snapshots as retained (unqualified).
Retained status was checked against completed collection evidence and actual
metadata in the corresponding source/job archive. Qualification remains zero.

Linux live-service and API suites passed, including concurrent insert
idempotency, recovery durability, incomplete/wrong-UUID rejection, changed
provenance blocking native operations, preflight detection, lost retained
metadata, and HTTP JSON/loopback guards. The additional empty-receiver test
passed: an unpublished configured root is allowed, but missing storage is
reported. Go vet, JavaScript syntax checks, and editor diagnostics passed.

Observation de-duplication is per source and snapshot timestamp. Identical
resends are not individually audited, and detection runs only at the stated
triggers, not through an independent background watcher. Writer coordination
and an explicitly approved collect remain necessary to refresh retained data.
No source backup, native transfer, retention copy, restore, or deletion was
initiated while deploying and validating this detection change.

## Topology information and operation clarification: 2026-10-08

The appliance GUI tab now displays information/topology instead of registration
and SSH-profile forms. Backup actions were renamed to distinguish creating a
backup, receiving an existing one, verification, and retaining history. Restore
staging is labeled preparation of a separate GHES target, with retained-history
direction and an explicit warning that native restore does not execute.

Browser checks verified both topology rows, absent registration controls,
disabled submission without a selected source, matching action/approval labels,
writer-pause requirements only for verify/collect, and source-filtered restore
context. Source latest and receiver current are not presented as identical.
Friendly journal labels retain the original machine action values.

The final embedded GUI was built/deployed with state and receiver mounts
preserved. JavaScript syntax and whitespace checks passed. A prior fixed-count
smoke assertion found twelve rather than ten records; inspection showed an
operator-initiated successful backup `20261008T180848` and its discovery entry,
which were preserved. No appliance operation was submitted during GUI testing.

Automatic customer enrollment remains backend work, not a completed feature:
trusted incoming-key acceptance, isolated route/storage provisioning, source
identity binding, and optional reverse-control authorization must be designed.
The official exchange alone cannot safely provision these from an unknown peer.

## Deployment inputs separated from lab fixtures: 2026-10-08

Operational authorized-key fragments and real host/UUID route files were
removed from the workspace. Generic `.example.json` templates now document
external installation inputs; historical observations above are not defaults.
Enrollment accepts an explicit root-owned directory and any source count,
preserves operator entries, and rejects duplicate source keys or unsafe modes.
The broker takes a configuration path and explicit non-root UID:GID per route;
receiver image identity is configurable through build arguments. Smoke tests
require external source fixtures with independent display names and route IDs.
Recovery paths and container-test source names are explicit inputs.

Executed disposable-key enrollment tests passed with three arbitrary sources,
duplicate-key rejection, writable-fragment rejection, and atomic preservation.
Broker tests and Go vet passed. A disposable image built/run as 24001:24002
confirmed actual image identity configurability. Missing smoke fixtures failed
before network activity, and relative recovery paths failed before host writes.
Runtime assets contained no real lab hosts, UUIDs, or key material on scan.

The active lab broker/keyfiles/routes were not replaced. New broker builds
require adding explicit `user` values to deployment routes and provisioning
image/file ownership consistently. Port 122, login admin, and `/data/backup`
remain native protocol contracts. This cleanup is not production qualification
or automatic installation/enrollment.

## Current-design E2E run: 2026-10-08

The operator approved one fresh backup per source and confirmed no competing
source backup/pruning or receiver writers. No separate disposable restore
appliance was available, so actual GHES restoration was excluded explicitly.
No independent protected-storage backend was introduced.

### Executed automated checks

Current Linux live-service, API, and portable broker suites passed, plus the
unchanged mock-filesystem and development-provider suites. Coverage included
actual fixture rsync collection/staging, nonempty-target rejection, ownership,
symlink/FIFO safety, interrupted/changed transfers, quiescence/approval guards,
concurrent observation de-duplication, and binding invariants. Go vet passed.

The current portable broker was also executed against the two receiver
containers using a separate root-owned E2E route file and test binary. Identical
synthetic paths, hard links, symlinks, concurrent writes, runtime restrictions,
and invalid/empty routes passed. The active older lab broker and its credential
routes were not replaced. Harmless fixtures remained outside native histories.

### Executed live operations

| Source | Fresh snapshot | Version | Verified outcome |
|---|---|---|---|
| B | `20261008T203632` | `v3.21.6` | Backup, delivery, explicit resend, checksum comparison, full-history retention |
| A | `20261008T203856` | `v3.21.7` | Backup, delivery, explicit resend, checksum comparison, full-history retention |

Eight real managed operations completed successfully and two new discoveries
were recorded. Source A's successful backup/archive took approximately 194
seconds and exceeded the runner's previous 180-second wait, with native archive
`Result=success`, `ExecMainStatus=0`, and zero restarts. The runner deadline was
made configurable (default 30 minutes); the completed backup was inspected,
not repeated, before resuming resend/verification/retention.

Independent newest-snapshot comparisons passed checksums, archive metadata,
hard links, and extra-entry detection strictly in dry-run mode with stable
source publications. Actual backup keys could not see host data, credentials,
sudo, or Docker socket, and forwarding to the API was denied. Missing approval,
cross-origin reconciliation, and invalid settings requests were rejected; valid
settings were unchanged. No separate pruning/deletion command ran. Native
backup creation retains its normal GHES retention behavior.

### Defect found and fixed

Full-history retention initially labeled older snapshots pending even when
they existed in the latest retained archive. The lookup incorrectly required
the collection header's current timestamp/version to match each history entry.
It now checks source/route provenance and the individual archived timestamp's
metadata. A Linux regression includes an older timestamp on a different GHES
version, and still rejects missing retained metadata. The fix was deployed
without recopying or deleting histories. All seven received snapshots now
correctly report retained, unqualified histories.

### UI and persistence evidence

The GUI showed both fresh snapshots, information-only topology, proper action
approval/pause controls, source-filtered retained histories, and no available
real restore target. No operation was submitted during browser checks. The
control plane was restarted; all 22 journal records, settings, both current
retained histories, and idempotent discovery persisted. After the retention
fix, the UI rendered seven retained snapshot rows and 22 journal rows, with
zero restore-qualified copies. API remained loopback-only and external SSH 122.

### Explicit untested boundaries

No actual GHES restore or seeded restored-data validation ran: a separate
approved target is required. Host reboot/persistent mount recovery, protected
storage/immutability, automatic onboarding/installation, alternative runtime,
ARM64 qualification, provider-specific snapshots, image-vulnerability clearance,
and malicious-content detection were not established by this run. Source-key
isolation does not make its own writable incoming history immutable. The result
is a validated current receiving/verification/retention pipeline, not complete
production or disaster-recovery qualification.

## Approved source-A retained-copy restore: 2026-10-08

The operator explicitly confirmed source A as the target, authorized data
overwrite, selected its retained `20261008T203856` / `v3.21.7` snapshot, and
selected restore without `-c`. This is a manual lab exception: the control-plane
guard against staging back onto a registered source was not relaxed.

Read-only preflight confirmed target GHES 3.21.7, dedicated ext4 backup disk,
approximately 465 GiB available, no replication flag, no active backup/archive,
and no identified backup schedule/timer. Actions was already enabled; existing
configuration was retained rather than reconfigured. External Actions data
and runner operation are not qualified by this rehearsal.

Maintenance was enabled. Because the backup-disk parent is root-owned, sudo
was used only to create the selected sibling staging directory and publish
it by rename. The retained full history was copied with `-aHAX --numeric-ids`
and passed checksum/metadata/extra-entry comparison in explicit dry-run mode.
It contained three timestamps, 7,914 regular files, and one current symlink;
logical size was approximately 4.28 GB with hard-link deduplication preserved.

The control plane was stopped with no active job, preventing GUI-triggered
writes during the manual restore. Source-local original history was preserved
at `/data/backup/data.before-backupfabric-restore-20261008T203856`; the verified
retained copy was renamed to `/data/backup/data`. No volume was formatted or
history deleted, and the recovery copy on BackupFabric stayed unchanged.

`ghe-restore -f -s 20261008T203856` was invoked on the confirmed target at
21:01:33 UTC. Output reported standalone restore, binary MySQL, MSSQL/Actions,
Redis, repositories, Pages, storage, and other native components, then data
restoration complete at 21:05:00 UTC followed by appliance configuration.
The native workflow restores secrets, UUID, and authorized SSH keys even
without `-c`; omitting that flag does not mean all authentication data is
left untouched. It warned about self-hosted Actions runner reconfiguration
and missing Gist backup. No private secret contents were retrieved.

Repository checks after data restoration found 21 repositories, matching the
baseline and retained snapshot. The normalized relative-path/reference digest
was `182c5ff4b8c2a33cb396096301edc1881aa3ecb1ba43a666ef8cfb001f5d5305`
before/staged/after, and all 21 passed `git fsck --full --no-reflogs`.

At the latest observation, native configuration was still running, Nomad jobs
were being brought up, SSH remained available, maintenance was still enabled,
and the control plane remained paused. The restore log had zero ERROR/FATAL
matches. This entry records progress, not terminal restore success or
restore-qualified status; final service and native exit checks remain pending.

### Terminal restore result and service validation

The native command completed at 21:28:11 UTC with exit status 0, reporting a
1,580-second runtime. It released the Replication Controller restore fence,
restarted cron, restored SSH host keys, and cleaned up the restore marker.
The data-only native path also clears GitHub Connect settings; no claim is
made that omitting `-c` preserves every authentication/integration setting.

Post-completion checks confirmed no restore/backup marker, active cron/Nomad/
Consul/Docker, and passing web health. Maintenance was unset only after these
checks. Local HTTPS `/status` returned 200, and `/api/v3/meta` reported 3.21.7.
The original local backup tree remained preserved, and the control-plane
container was restarted and returned health/status OK. No additional backup,
transfer, pruning, or deletion was initiated during final validation.

This demonstrates one successful same-appliance GHES 3.21.7 native data restore
from a BackupFabric-retained history, including the earlier 21-repository
reference/object checks. It does not establish full production qualification,
authenticated user/org validation, external Actions data or runner recovery,
cross-appliance/version restoration, or independent-host disaster recovery.
No automatic restore-qualified catalog state was assigned. Operators should
review restored settings and validate authenticated application workflows.
