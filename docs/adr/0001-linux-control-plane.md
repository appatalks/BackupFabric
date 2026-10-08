# ADR 0001: Linux control-plane boundary

- Status: accepted
- Date: 2026-10-07

## Decision

Run orchestration, catalog, scheduling, policy, audit, UI, and storage-provider
integrations on a standard Linux host. Do not install BackupFabric components
or custom SSH routing inside GHES.

GHES executes only documented vendor backup and restore operations. Linux may
connect through documented administrative SSH and infrastructure APIs.

## Consequences

The product is reusable infrastructure rather than an appliance customization.
GHES upgrades do not need to preserve BackupFabric packages or hooks. Provider
adapters and source clients must remain replaceable and separately testable.
