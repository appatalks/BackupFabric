# ADR 0004: Independent recovery retention

- Status: proposed; storage backend not selected
- Date: 2026-10-08

## Scope

Preserve native-push receiving on SSH 122, isolate sources, protect historical
recovery copies from source-held credentials, and validate recoverability.
Keep platform block snapshots and native-push collection as complementary
paths. Actions external-storage coverage and appliance-side restore setup
are deferred; they are not added to receiver admission.

This is a design, not an implemented immutability feature. The current local
retained-copy workflow and reference-only provider cannot establish independent
recovery protection. No existing history is moved, pruned, or reformatted.

## Proposed storage boundaries

| Tier | Purpose | Source credential access | Current implementation |
|---|---|---|---|
| Incoming receiver | Native publication and per-source isolation | Writes its own namespace only | Experimental isolated receiver containers |
| Local retained copy | Validated history capture and staging convenience | No direct mount/access from receiver sessions | Existing source/job archives, writable by the control plane |
| Protected recovery store | Historical copies surviving source or receiver-host compromise/loss | No overwrite, delete, retention bypass, or policy access | Not implemented; independent backend required |

The existing broker permits arbitrary shell execution inside the assigned
receiver. That confines host/cross-source access but allows alteration of the
source's own incoming data. Receiver isolation must never be marketed as
immutable retention. A local copy is not independent disaster recovery simply
because it occupies another directory or container.

## Identities and authority

- Source backup identity: accepted on SSH 122, bound to one receiver; no recovery
  store credentials, host shell, forwarding, policy management, or retention
  administration. Compromise of this key must not destroy older protected copies.
- Capture worker: reads a coordinated receiver history or platform volume
  snapshot. It has no authority to delete or rewrite protected history.
- Recovery-store writer: creates independent recovery generations through a
  provider-enforced interface. Do not infer append-only behavior from client
  code merely omitting deletion. Restrict overwrite, version deletion, and
  retention bypass as well as ordinary delete operations.
- Retention administrator: separate credentials and trust boundary, not stored
  on GHES, in receiver containers, or in the routine control-plane service.
  Destructive policy changes require explicit authorization and durable audit.
- Recovery reader: can retrieve a selected protected generation to new writable
  staging storage, never materialize the retained original in place.

Protection must survive the threats claimed for it. Same-host root compromise,
shared administrator credentials, shared storage loss, and deletion of catalog
or encryption keys must be assessed separately. Encryption alone is not
immutability, and an append-only repository alone does not prove off-host
durability or resistance to storage-administrator deletion.

## Backend selection contract

Before implementation, select an independent backend and document:

1. Host/account/region or storage failure boundary and its supported durability.
2. Actual retention-lock or append-only enforcement, expiration rules, and which
   principals can bypass/change them. Never claim immutability without evidence.
3. Whole-history metadata fidelity: hard links, symlinks, ownership, ACLs,
   extended attributes, case sensitivity, and native database dependencies.
4. Resumable transfer semantics, atomic publication, idempotency, corruption
   checks, quotas, and incomplete-generation handling.
5. Encryption-key custody, rotation, and recovery without the original receiver.
6. Independent audit, catalog export/reconstruction, recovery access, and
   capacity/retention monitoring. Loss of a SQLite catalog must not orphan copies.

Candidate backends are independently locked object storage, a separate vault
host with storage-enforced protection, and platform snapshots with independent
retention/replication. An object store must retain a reconstructable filesystem
representation, not flatten a native history into disconnected file objects.
Platform snapshots must be materializable as usable independent recovery disks;
a reference or dependent chain alone is not proof of recovery readiness.

## Capture and evidence

Receipt, copy integrity, retention, protection, and restore qualification are
independent evidence dimensions, not aliases or an automatic linear promotion.

| Evidence | Required proof | Must not imply |
|---|---|---|
| Received | Identity-bound publication observed; managed sends also record command outcome | Trusted content or a witnessed external transfer |
| Integrity compared | Defined comparison with stable source/receiver publication | Source cleanliness, independent protection, or recoverability |
| Locally retained | Completed capture and matching retained-history evidence | Off-host durability or immutability |
| Protected | Backend generation plus verified protection policy and independent access boundary | Restore compatibility or malicious-content detection |
| Restore-qualified | Completed seeded rehearsal with recorded checks and compatible GHES target | Indefinite qualification after format/version/policy changes |

Bind evidence to registered source UUID, native timestamps/versions, the exact
captured generation, backend references, comparison method, policy evidence,
and observation times. Discovery timestamps are not transfer completion times.
Do not replace the existing unqualified states or fabricate protected records.

Capture must coordinate native backup, publication, transfer, and pruning.
Absence of a marker or stable current link alone is insufficient. Preserve the
complete native history and required incremental dependencies. If a consistent
capture cannot be established, fail without promoting the generation. Current
file collection still relies on operator-confirmed writer quiescence; automatic
collection requires additional coordination, not merely a directory watcher.

New generations never modify earlier protected generations. Validation failures
or newly corrupted incoming data must not expire the last known recovery copy.
Storage protection preserves history; it cannot make a compromised source's
new backup trustworthy. Restore only to a disposable rehearsal environment
until source/content trust and recovery results have been assessed.

## Implementation sequence

1. Select backend and failure boundary; approve concrete protection/access tests.
2. Implement the provider/export contract and evidence persistence. Keep the
   development reference provider outside all protected-success decisions.
3. Capture and export one source generation without deleting or moving its
   existing backups; validate publication, metadata fidelity, and recovery read.
4. Exercise harmless source-key and writer-credential denial probes against
   disposable fixtures, interrupted exports, retry, and concurrent sources.
5. Reconstruct catalog and materialize recovery data on a separate host with
   the original receiver unavailable. Keep incoming credentials out of recovery.
6. Complete seeded GHES restore rehearsals for both sources, record recovery
   point/time, and repeat after supported GHES feature upgrades.

## Acceptance gates

- A source key cannot access another source, local retained archives, or the
  protected recovery namespace. A writer cannot mutate an earlier generation.
- Disposable test fixtures demonstrate overwrite/delete/bypass restrictions
  without using destructive probe payloads against actual backup data.
- Incomplete, altered, or interrupted exports never appear protected or usable.
  Retry is idempotent; quotas cannot trigger deletion of protected history.
- Matching source/receiver checksums never automatically claim clean content.
- The original receiver and its catalog are unnecessary for retrieving a
  documented recovery generation; independent audit/key recovery is exercised.
- Native histories retain filesystem semantics and database dependencies when
  materialized, and the selected copy restores with source-specific seed data.
- Qualification records include source/version, generation, target, checks,
  timings, and outcome. No broad version compatibility is inferred from two
  successful lab transfers or metadata format acceptance.

No protection rollout or real restore occurs until storage and rehearsal
resources are explicitly approved. This ADR does not authorize a provider,
account, bucket, disk, host, or appliance configuration change.