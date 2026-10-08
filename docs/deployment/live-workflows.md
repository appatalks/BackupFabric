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

### Isolated native push lab mode

The approved compatibility prototype supports multiple sources pushing to one
port-122 host, each key forced into a separate Ubuntu receiver container. This
is not GitHub's documented receiver topology or a production-supported
replacement. See [the lab](../../tools/native-receiver-lab/README.md) and ADR 0003.

Start the control plane with `--native-receivers <configuration.json>`. The
startup-only JSON maps source hostnames to `root`, `destination_host`,
`source_uuid`, and `route`; the lab example is
`tools/native-receiver-lab/control-plane-receivers.example.json`. Copy and adapt
it outside the repository; no real appliance identities or keys are bundled. Mount configuration,
receiver roots, and SSH secrets read-only; use separate writable catalog and
retained-archive paths. Never give the control plane the Docker socket.

Source SSH profiles use `collection_mode: "native_push"`, port 122, a
control-plane key reference, and no `receiver_id`. The administrator-configured
route and source UUID are persisted and cannot be rebound. Control-plane SSH
keys and incoming-backup keys are separate credentials.

`GET /api/v1/live/snapshots` (CLI `live snapshots`) discovers received timestamp,
version, UUID, route, publication state, and metadata problems. Bounded,
no-follow reads reject incomplete snapshots, wrong identities, malformed
versions, and symlink/FIFO metadata. Arrival remains `received_unverified`.

Detection now reconciles on startup, UI refresh, native preflight, and before
approved native operations. `POST /api/v1/live/reconcile` with `{}` or CLI
`backupfabric live reconcile` explicitly runs the same scan. GET inventory
and journal endpoints remain read-only. Scans skip while a managed live
operation is running and report inaccessible roots or changed provenance.

Each valid source/timestamp gets one durable `discover` journal entry with
phase `observed`. Existing snapshots are backfilled on first scan. Duplicate
scans, concurrent scans, and restarts do not create duplicate observations.
Observation time is not transfer time, and the entry does not claim to have
witnessed transfer completion, determined origin, or verified checksums.
Resends of the same unchanged snapshot are not distinct transfer events;
per-transfer auditing requires additional gateway evidence.

Received inventory includes `retention_state`: `pending_collection`,
`retained_unverified`, or `not_eligible`, plus a matching `retained_job_id`
when available. Retained status requires a completed source-matching collect
job and matching on-disk timestamp/version/UUID metadata. It is not a new
checksum or restore check. New arrivals do not update existing retained
copies; collection still requires explicit approval and paused writers.

A completed full-history collection can retain multiple timestamps and GHES
versions. Retention lookup validates each timestamp's archived version/UUID
instead of treating the collection job's current timestamp as its only member.

| Action | Approval | Successful state |
|---|---|---|
| `backup` | `BACKUP <source-id>` | `received_unverified` after backup, archive service completion, and matching publication |
| `archive` | `ARCHIVE <source-id>` | `received_unverified` after sending current and matching publication |
| `verify` | `VERIFY <source-id>` and paused writers | `checksum_verified_unqualified` after stable source/receiver `-aH` comparison |
| `collect` | `COLLECT <source-id>` and paused writers | `collected_unverified` after isolated full-history `-aHAX` retention/comparison |

Jobs persist approval, phase, source timestamp/version/identity, and outcome.
Checksums do not confer restore qualification. Collection rejects ownership
the service cannot preserve and makes a fresh source/job archive usable by
the existing manual staging workflow. The receiver host cannot be that source's
restore target. No native restore is executed automatically.

Quiescence relies on operator attestation; source schedules and external writers
are not automatically paused. Native push bandwidth is not governed by the
local retention bandwidth setting. Gateway/key provisioning and persistent
storage remain administrator tasks.

Set the image's `UID` and `GID` build arguments to match required native
ownership (the historical lab used 501:501). These are installation inputs,
not universal GHES or BackupFabric defaults. Merely overriding `docker run --user` leaves no passwd entry for
OpenSSH and fails preflight. Other ownership limitations must fail closed.

The current management URL is `http://127.0.0.1:8088`, through:

```bash
ssh -N -L 127.0.0.1:8088:127.0.0.1:8080 -p122 \
   admin@backupfabric.example.com
```

The API remains unauthenticated and loopback-only; no external port other than
SSH 122 is needed. `node tools/native-receiver-lab/control-plane-smoke.mjs`
requires `BACKUPFABRIC_SMOKE_SOURCES` pointing to external deployment fixtures
and checks that installation's inventory. `--archive` sends existing snapshots;
`--verify-and-collect` requires real operator-confirmed writer quiescence.
Neither smoke option creates new source backups or restores.

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

## Appliance information and onboarding

**Appliances & topology** is now an information page: registered appliance
identity, hostname, SSH role/port, control-key reference, receiver mapping,
current snapshot/version, and retention state. Manual registration/profile
forms were removed from the GUI. Source connection checks remain available;
their SSH checks are non-mutating, while discovery may journal observations.

Full automatic onboarding is not implemented yet. Today administrators still
provision authorized receiver routes and register source/SSH profiles through
the API/CLI as part of lab setup. The intended customer workflow is the
documented GHES remote-archive exchange with trusted receiver-side key
authorization, followed by automatic isolated route/catalog provisioning.
The information page must not imply that deleting forms implements enrollment.

Follow [GitHub's setup procedure](https://docs.github.com/en/enterprise-server@3.21/admin/backing-up-and-restoring-your-instance/configuring-remote-archives-for-backups)
for the source's two-pass key exchange. In the inspected implementation its
receiver commands do not provide a trustworthy source hostname/UUID at the
initial connection, and incoming key authorization does not grant reverse SSH
control access to the source. Safe onboarding still needs approved key identity,
isolation/storage provisioning, identity binding from validated metadata, and
separate control credentials for source-side GUI operations. Unknown SSH keys
must not be silently trusted. Restore targets also need trusted enrollment.

Host, role, and receiver mapping are immutable once configured. Key reference
and port may be updated while no live operation is running. One receiver may
belong to only one source. Login is fixed to `admin`; no sudo wrapper or managed
GHES configuration changes are introduced.

## Operation labels and direction

| GUI operation | Behavior |
|---|---|
| Receive latest existing backup | Invokes the source's native archive command; source current goes to its BackupFabric receiver, without creating a backup |
| Create new backup and receive it | Runs source `ghe-backup`, awaits native archive completion in native-push mode, and checks matching publication |
| Verify latest received backup | Compares source and receiver checksums/archive metadata; no backup or copy is created |
| Save retained copy of received history | Copies received history into a new source/job archive, with explicit writer quiescence and integrity checks |
| Prepare restore target | Copies a retained history to a separate compatible GHES target's empty backup root; does not run `ghe-restore` |

Operation context shows source and receiver direction, with receiver-current
metadata displayed separately from source latest. The pause attestation is
shown for verification/retention, not backup creation/delivery. Machine API
actions and typed approval phrases are unchanged. Legacy dedicated-receiver
backup completion still does not establish asynchronous receiver sync success.

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
