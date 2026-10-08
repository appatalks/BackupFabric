# Unsupported and unverified behavior

The following are explicitly unsupported:

- Treating a generic SSH server as a native GHES remote-archive receiver.
- Multiple source appliances writing to one native receiver backup root.
- Modifying GHES binaries, managed configuration, SSH forced commands, or
  mount namespaces.
- File-level copying as a production-qualified canonical preservation format.
  Experimental, explicitly approved receiver collection and restore staging
  are available; real GHES restore qualification remains outstanding.
- Automatic deletion or pruning.
- Marking a backup restore-qualified after transfer or snapshot creation.
- Exposing the unauthenticated foundation API beyond loopback.
- Running the supported Compose configuration on Docker Desktop or a non-Linux
  Docker host; it relies on Linux host networking.
- Mounting the Docker socket or running BackupFabric as a privileged container.

The following remain unverified until tested on non-production GHES 3.21:

- Application consistency of a provider snapshot requested immediately after
  native backup completion.
- Complete correctness of the consistency observations.
- Attaching and mounting a cloned volume on a restore target.
- End-to-end restoration and post-restore validation.
- All provider-specific snapshot and clone behavior.
- Native remote archive wire protocol and multi-source behavior.
- SSH admin access to every required native backup file and metadata attribute.
- Preservation of foreign UID/GID ownership by the non-root container.
- Experimental rsync collection/staging against real GHES, including the
  native restore rehearsal. Synthetic tests are not proof of compatibility.

The development snapshot provider records references only. It does not capture
data and must never be used as evidence of backup completion.

The live SSH workflows are separate from this development provider. Their
states (`backup_completed`, `collected_unverified`, `staged_unverified`) do
not confer restore qualification. See [live workflows](deployment/live-workflows.md).
