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
