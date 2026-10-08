# ADR 0003: No proprietary receiver emulation

- Status: accepted
- Date: 2026-10-07

## Decision

Do not emulate `ghe-backup-remote-add` or the native remote-archive receiver.
Treat one source to one dedicated GHES receiver and disk as the only allowable
native remote-archive topology until GitHub documents another.

## Rationale

Receiver authentication, host-key behavior, commands, target paths, transfer
protocol, publication semantics, and multi-source behavior are undocumented.
Guessing would put recoverability and source isolation at risk.

## Approved lab exception: 2026-10-08

The operator approved experimental native-push receiver research on isolated,
non-production infrastructure. The prototype under
`tools/native-receiver-lab` and `cmd/backupfabric-native-lab` may test fixed-key
routing into per-source unprivileged receiver containers. Its admission
response explicitly identifies a non-GHES lab receiver. This is not a supported
production topology, and the original production decision remains in force.

The root-owned lab broker is separate from the non-root control plane. Source
sessions must not receive a host shell, forwarding, Docker socket, or ability
to select another receiver. Real key-routing, interrupted-transfer,
incremental-dependency, persistent-storage, and restore tests remain required
before any broader architectural decision. Container isolation tests alone
do not qualify backup recoverability or establish a hardened gateway.
