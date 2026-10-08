# Roadmap

## Experimental foundation

- Linux-native service, API, CLI, embedded UI, and SQLite catalog
- Explicit appliance and volume registration
- Storage-provider interface and deterministic development adapter
- Synthetic multi-source filesystem tests
- Guided GUI registration, SSH profiles, settings, and restore-staging wizard
- Experimental dedicated-receiver SSH/rsync workflows with explicit approvals
- Durable live operation journal and fail-closed ownership checks

## Provider prototype

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
