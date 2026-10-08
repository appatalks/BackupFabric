# Experimental live GHES workflows

## Topology

```text
GHES source A -> native remote archives -> dedicated GHES receiver A
GHES source B -> native remote archives -> dedicated GHES receiver B
                                              |
                              BackupFabric rsync collection
                                              |
                         isolated local retained histories
                                              |
                              approved rsync restore staging
                                              |
                                  separate GHES restore target
```

The native source-to-receiver topology follows GitHub's documented workflow.
BackupFabric's file collection and restore staging are **experimental** and
have not been qualified on real GHES. A normal Ubuntu SSH server is not a
native receiver.

## Server preparation

The image includes SSH and rsync. The process remains non-root. Mount:

- A writable archive root, owned and accessible to service UID/GID `10001`.
- A read-only secret directory containing one private key per configured key
  reference and a `known_hosts` file.

Private keys must be regular files, readable by UID `10001`, and mode `0600`
or stricter. Host-key files must not be group/world writable. Symlinked secret
files are rejected. Obtain and independently verify each GHES SSH host key.
For nonstandard port 122, known-host entries use `[hostname]:122`.

Example layout:

```text
/srv/backupfabric/ssh/
├── known_hosts
├── source_a
├── receiver_a
└── restore_target
```

The GUI stores filename references, never private-key content. Key files are
loaded by OpenSSH at operation time; agent forwarding and interactive password
authentication are disabled. No automatic host-key enrollment is performed.

```bash
export BACKUPFABRIC_ARCHIVES=/srv/backupfabric/archives
export BACKUPFABRIC_SSH_SECRETS=/srv/backupfabric/ssh
docker --context default compose -f compose.yml -f compose.live.yml up --build -d
```

Use your intended Docker Engine context; `default` is the system engine on our
test host. Directories must already exist. No Docker socket or privileged mode
is required.

Each source gets a generated directory under the archive root. This is
filesystem namespace isolation, **not automatic volume provisioning**. An
administrator can mount a separate logical volume at each source-ID directory.
The registry's volume IDs are inventory mappings; the service does not create
or verify storage-provider volumes.

## GUI setup

1. Register source, receiver, and restore target separately in **Appliances &
   SSH**. Use distinct hostnames and volume identifiers.
2. Configure the receiver's SSH profile with role **Dedicated receiver**,
   port 122, and its key reference.
3. Configure the source's profile and select its receiver.
4. Configure a separate target with role **Restore target**.
5. Run read-only preflight for each.
6. Configure native remote archives directly on GHES, following
   [GitHub's instructions](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/configuring-remote-archives-for-backups).

Host, role, and receiver mapping are immutable once configured. Key reference
and port may be updated while no live operation is running. One receiver may
belong to only one source. Login is fixed to `admin`; no sudo wrapper or managed
GHES configuration changes are introduced.

## Backup and collection

**Run native backup** verifies that the source's
`backup.remote-archive-destination-host` matches its registered receiver, then
executes the documented `ghe-backup` command. Command success is recorded as
`backup_completed`, not as an archived or recoverable backup.

Remote synchronization is asynchronous. Before **Collect receiver history**,
the operator must confirm it completed and pause source backups/pruning and
receiver writers for the entire collection. The local backup-in-progress
marker alone does not prove remote synchronization completed.

Collection:

1. Checks pinned SSH identity, mounted backup disk, rsync availability, and
   absence of the local backup marker.
2. Records the receiver's `current` timestamp.
3. Copies the entire `/data/backup/data/` root into a new source/job directory
   using `rsync -aHAX --numeric-ids`.
4. Runs a checksum dry-run comparison, including detection of extra entries.
5. Checks that `current` has not changed and rejects escaping/broken symlinks
   and special files.
6. Records `collected_unverified`.

The source history is not deleted or modified. Failed or interrupted copies
remain outside usable collection states. A retry creates a new operation and
does not silently resume or publish an old partial copy.

**Permission limitation:** non-root local rsync cannot preserve arbitrary
foreign UID/GID ownership. The GHES admin session must also be able to read and
write the required metadata. The service explicitly checks receiver ownership
against its local UID/groups before and after collection, and checks retained
ownership against the target admin UID/groups before staging. This is necessary
because non-root rsync can silently skip ownership preservation.
Rsync errors or comparison differences fail the
operation; the service does not drop ownership/ACL/xattr flags, use `--fake-super`,
or escalate privileges as a fallback. This must be tested with real GHES data
and may require a separately designed, narrowly privileged transfer mechanism.

## Restore staging

The wizard requires a completed collection, selected native timestamp, separate
restore target, explicit target approval, paused target writers, and operator
confirmation of GHES version compatibility.

The target must have a mounted, previously initialized backup disk and an
**existing, empty, non-symlink `/data/backup/data` directory**. Never format a
disk containing backups.

The service obtains a staging lock, transfers the whole history into a unique
sibling directory on the backup disk, performs checksum dry-run comparison,
and publishes with a same-filesystem rename only if the active root is still
empty and no local backup marker is present. The service lock coordinates
BackupFabric only; the operator must prevent other GHES writers.

Failed transfers do not replace the active root. Partial sibling directories
remain for operator inspection. If shutdown or connectivity loss prevents lock
release, inspect the target before manually removing the empty staging lock.
Nothing is recursively deleted.

Success is `staged_unverified`. Follow GitHub's maintenance-mode, HA, version,
and restore procedures, then execute `ghe-restore -s <timestamp>` manually.
BackupFabric does not execute native restores or mark restore qualification.

## Settings and operation journal

Settings support a bandwidth cap (KiB/s; zero means unlimited) and an operation
timeout (1–1440 minutes). Only one live operation runs at a time. Settings are
captured when an operation starts.

Jobs persist the requested action, source/target, typed approval, attestations,
phase, timestamps, state, and outcome. Restart converts unfinished jobs to
`interrupted`; it never infers success from files left on disk.

The same workflows are exposed to the CLI:

```bash
backupfabric live endpoints
backupfabric live settings
backupfabric live jobs
backupfabric live preflight --endpoint "$RECEIVER_ID"
backupfabric live run --action backup --source "$SOURCE_ID" \
  --confirm "BACKUP $SOURCE_ID"
```

Set `RECEIVER_ID` and `SOURCE_ID` to IDs returned by registration. Collection also
requires `--quiesced`; staging requires `--target`, `--collection`, `--snapshot`,
`--quiesced`, `--compatible-target`, and the exact `STAGE <target-id>` approval.

The API remains loopback-only and unauthenticated. Live endpoints reject
cross-origin requests, non-loopback Host headers, and non-JSON writes. This is
not a replacement for authentication/authorization: only trusted local
administrators should access the service. Use SSH tunneling for remote access.
