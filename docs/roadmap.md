# Roadmap

## Experimental foundation

- Linux-native service, API, CLI, embedded UI, and SQLite catalog
- Explicit appliance and volume registration
- Storage-provider interface and deterministic development adapter
- Synthetic multi-source filesystem tests
- Read-only appliance/topology GUI, approved operations, settings, and restore staging
- Experimental dedicated-receiver SSH/rsync workflows with explicit approvals
- Durable live operation journal and fail-closed ownership checks

## Generic backup producers

- Add format-specific discovery/verification adapters alongside GHES, rather
	than requiring other applications to create fake GHES metadata.
- Bind trusted producer credentials to isolated receiver roots. Define a
	bounded source/format/timestamp/checksum manifest and completion protocol.
- Catalog and retain application-consistent database/file backups supplied by
	producer tooling; application-specific restore and qualification stay separate.

Generic LAMP/server ingestion is proposed, not implemented. Arbitrary file
uploads to a shared backup root must not be accepted as complete backups.

## Portable deployment

Recommended baseline: Ubuntu 24.04 LTS. The initial packaging goal is Linux
amd64 and arm64; qualification is required for each distribution/architecture.

- Publish versioned prebuilt control-plane binaries and multi-architecture
	receiver/control-plane images with checksums, provenance, and image review.
	Do not require customers to install Go or build from source.
- Provide one idempotent initialization workflow with a non-mutating check
	mode and explicit approval before host changes. Keep deployment inputs and
	credentials outside the source tree.
- Detect architecture, supported container engine, init system, free capacity,
	filesystem links/ownership/ACL/xattrs, persistent mounts, and existing SSH
	listeners. Fail clearly on unsupported combinations rather than relaxing
	metadata or isolation checks.
- Make Docker Compose optional for the guided install. Native-push isolation
	currently requires Docker Engine; support for another runtime requires a
	tested broker adapter, not a renamed executable.
- Provision the non-root control plane separately from the restricted host
	broker and per-source receivers. Retain the single external SSH port 122,
	verified operator access, and loopback-only management until authentication
	and TLS are implemented.
- Test persistent mounts, boot ordering, upgrades/rollback, identity ownership,
	and trusted source enrollment on clean Ubuntu, then Debian and RHEL-family
	hosts including SELinux/firewall differences. Never format disks, overwrite
	keys, or replace an occupied SSH listener without explicit approval.
- Provide optional native systemd packaging for the control plane. Embedded
	Linux, non-systemd hosts, rootless engines, and alternative runtimes are
	separate unqualified tracks, not advertised as universally supported.

The automated installer, released multi-architecture artifacts, and broad
distribution certification are planned work, not implemented capabilities.

## Provider prototype

Priority design: [independent recovery retention](adr/0004-independent-recovery-retention.md).
The operator has not selected a protected-storage backend. Keep native push and
platform snapshots complementary; defer Actions external-storage coverage and
appliance-side restore setup from receiver admission.

- Select independent recovery storage and separate writer, retention-admin,
	and recovery-reader authority; prove protection with disposable fixtures.
- Distinguish receipt, comparison, local retention, independently protected
	generations, and successful restore qualification in evidence and UI.
- Validate recovery retrieval/catalog reconstruction without the original host,
	then complete seeded per-source GHES restore rehearsals and record RPO/RTO.

- Implement one real provider snapshot/clone adapter
- Qualify the pinned GHES SSH client and capture authoritative completion evidence
- Add controlled retries, rate limits, and attributable audit events
- Add authentication, authorization, TLS, and external secret integration
- Resolve metadata privileges without weakening isolation or modifying GHES

## GHES qualification

- Execute the non-production GHES 3.21 test plan
- Rehearse complete restores for three isolated sources
- Document recovery time and recovery point measurements
- Obtain GitHub Support answers for remaining native archive questions

## Production candidate

- Retention locks and guarded lifecycle policies
- Catalog backup and disaster recovery
- Monitoring, alerting, capacity trends, and upgrade tooling
- Security review and threat-model validation
- Repeated restore qualification across supported GHES versions

BackupFabric will not be called production-ready until independent histories
from multiple real GHES appliances have been restored successfully through the
documented workflow.
