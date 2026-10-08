# ADR 0002: Block-snapshot collection

- Status: accepted
- Date: 2026-10-07

## Decision

Use a whole-volume storage snapshot of a completed native GHES backup disk as
the initial canonical retained form. Key each snapshot by an immutable
BackupFabric source ID and never merge roots from different sources.

## Rationale

This preserves the filesystem graph without reverse-engineering GHES remote
archives. GitHub recommends storage snapshots when available. A cloned,
previously initialized backup disk has the clearest documented restore path.

## Consequences

The first production adapter must target an infrastructure platform capable of
snapshotting and cloning attached volumes. File-level exports require a later
ADR and full restore qualification.

## Experimental extension

The operator-requested dedicated-receiver test track supports full-tree rsync
collection and restore staging without block clones. It is not yet a
production-qualified canonical preservation format. Metadata permissions,
quiescing, incremental dependencies, and complete native restores must be
validated before reconsidering the canonical-format decision.
