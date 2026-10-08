# Threat model

## Protected assets

Native backup volumes contain repositories, databases, configuration, appliance
secrets, administrative keys, SAML keys, SSH host keys, and certificates.
Catalog integrity, provider credentials, audit history, and restore approvals
are also security-sensitive.

## Trust boundaries

- Administrator to BackupFabric API and UI
- BackupFabric to GHES administrative SSH
- BackupFabric to infrastructure provider API
- Catalog to external secret manager
- Snapshot storage to restore target

The native-push lab adds source-key-to-isolated-receiver and receiver-history-
to-local-retained-copy boundaries. A proposed third tier, independently
protected recovery storage, is described in
[ADR 0004](../adr/0004-independent-recovery-retention.md) and is not implemented.

Per-source receiver containers do not make incoming histories immutable. The
broker executes the original command in a writable source-specific container,
so a compromised source/key can corrupt that source's incoming history. Local
retained copies are not mounted into receiver containers, but remain writable
by the control plane and share the receiver host/storage failure boundary.
They are not proof of off-host recovery or retention-lock enforcement.

Client-supplied hostnames and snapshot labels are untrusted metadata. Trusted
identity comes from administrator-approved registration, pinned SSH identity,
and provider volume binding.

The experimental live SSH transport reads mounted private-key references and
an independently verified `known_hosts` file. It forbids automatic key
enrollment, interactive authentication, and agent forwarding. Endpoint role,
host, and source-to-receiver binding are immutable. The unauthenticated API
is restricted to trusted local administrators; loopback Host and same-origin
JSON checks reduce browser-based request attacks but do not provide user
authentication.

Rsync staging uses an empty destination and a separate transfer directory.
Ownership is checked explicitly because non-root rsync can skip UID/GID
preservation without failing. No privileged fallback or actual deletion is
performed. Dry-run deletion flags are used only to detect extra files during
comparison. A BackupFabric staging lock cannot block independent GHES writers;
the operator must quiesce them.

## Principal threats and controls

| Threat | Required control |
|---|---|
| Cross-source overwrite | Immutable source IDs, unique volume constraint, no merged roots |
| Path traversal or symlink escape | Never derive host paths from client values; canonical provider IDs |
| Partial backup published as valid | Explicit state machine and consistency gate |
| Compromised source impersonation | Pinned SSH host identity and approved source mapping |
| Compromised source corrupts its own history | Independently protected earlier generations; source credentials excluded from recovery storage |
| Receiver/control-plane compromise destroys retained copies | Separate storage authority, retention enforcement, recovery credentials, and independent audit/catalog recovery |
| Provider credential theft | External secret references, least privilege, rotation, no secret logging |
| Unauthorized restore or deletion | Role-based authorization, explicit approval, immutable audit event |
| Catalog tampering | Restricted file permissions, transactional DB, backups, integrity monitoring |
| Snapshot disclosure | Provider encryption, access policy, private networking, audited reads |
| Replay or confused deputy | Bind every operation to source and volume IDs; idempotency keys |
| Denial of service | Per-source locks, global concurrency and rate limits, bounded requests |

## Destructive operations

Deletion and pruning are outside the current foundation and disabled by
omission. Future implementations must default to disabled, support retention
locks, require explicit policy plus operator authorization, and refuse to
delete the last restore-qualified copy.

## Residual risks

Provider snapshots may be crash-consistent rather than application-consistent.
GHES does not document an atomic completion-and-quiesce API. Only repeated
restore rehearsals can qualify the end-to-end workflow.

Checksums establish the comparison performed, not source cleanliness. A faithful
copy of corrupted data can pass comparison. Missing provider references,
untested policy enforcement, same-host copies, or development-provider records
must not be promoted to protected recovery evidence.
