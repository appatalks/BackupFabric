# Experimental native receiver deployment

This is a lab compatibility prototype, not a production-supported native GHES
receiver. The root broker is separate from the non-root control plane. Historical
appliance-specific results are recorded in `docs/testing/ghes-integration-plan.md`;
those hosts, keys, UUIDs, and filesystem layouts are not installation defaults.

## Deployment inputs

- Copy `routes.example.json` to a root-owned external configuration file. Supply
	each source's fixed container name and explicit non-root numeric `user` UID:GID.
- Copy `control-plane-receivers.example.json` to an external file. Replace example
	source/destination hosts, source UUIDs, route names, and container-visible roots.
	Pass that path to control-plane `serve --native-receivers` and mount it read-only.
- Build the receiver image with `--build-arg UID=<uid> --build-arg GID=<gid>`.
	Defaults are 10001:10001. Match the broker route user and data ownership.
	Preserve filesystem ownership rather than silently dropping metadata flags.
- Provision one isolated volume per receiver, mounted at `/data/backup`; keep
	networking disabled, root read-only, capabilities dropped, and no Docker socket.
- Provision operator keys, approved source public keys, private control keys, and
	independently pinned host keys outside this repository. No operational public
	or private key is bundled. Do not accept unknown keys automatically.

The broker takes an explicit configuration path:

```text
backupfabric-native-lab -config /etc/backupfabric/receiver-routes.json tenant-east "remote-command"
```

Example forced-command prefix before the deployment-provided public key:

```text
restrict,command="/usr/bin/sudo -n /usr/local/libexec/backupfabric-native-lab -config /etc/backupfabric/receiver-routes.json tenant-east \"$SSH_ORIGINAL_COMMAND\""
```

Key fragments must be generated/provisioned from real approved keys, not copied
from a checked-in appliance fixture. `assemble-keys.sh CONFIG_DIRECTORY` reads
the root-owned `operator_keys` and any `*.authorized_key` fragments in that
directory. It preserves operator entries, checks one restricted public key per
fragment, validates distinct source fingerprints, inserts newline separators,
and atomically installs `authorized_keys`. No fixed source count is assumed.

## SSH and recovery

`gateway-sshd_config` is a template for the shared port-122 listener. Adapt keyfile
and host-key paths to the installation, syntax-check it, and preserve verified
operator access before activation. Backup keys must have their fixed forced
commands and no host shell, PTY, or forwarding. Operator access can tunnel to
the loopback-only control API; no second externally exposed SSH port is needed.

`ssh-rollback.sh BACKUP_CONFIG ACTIVE_CONFIG [SERVICE]` requires explicit absolute
paths. Validate recovery before activating a listener. Restoring old configuration
can restore old unrestricted-key policies; stop backup traffic first.

Port 122, login `admin`, and receiver `/data/backup` are native protocol contracts,
not appliance-specific defaults. Container names, source IDs, config paths,
credentials, storage backing paths, UID/GID, and source metadata are deployment
inputs. The existing live lab was not reconfigured by this repository cleanup;
new broker builds require explicit `user` fields in their route configuration.

## Validation

```bash
go test ./cmd/backupfabric-native-lab
bash test-enrollment.sh /path/to/assemble-keys.sh
BACKUPFABRIC_RECEIVER_CONFIG=/etc/backupfabric/receiver-routes.json \
	bash test-containers.sh tenant-east tenant-west
BACKUPFABRIC_SMOKE_SOURCES=/path/to/sources.json \
	BACKUPFABRIC_API_URL=http://127.0.0.1:8088 node control-plane-smoke.mjs
```

Enrollment tests run as root only inside a disposable test environment with
generated keys. Container tests run on the deployed host and retain harmless
synthetic fixtures. Smoke fixtures are an external JSON array; every entry
provides `name`, `hostname`, `volume_id`, `appliance_uuid`, `key`, `version`, and
`route`. Adapt `smoke-sources.example.json`; never run unadapted example fixtures
against a real API. Display names and receiver route names are independent.
Optional `--archive` resends existing snapshots; `--verify-and-collect` requires
actual operator-confirmed writer quiescence. These options are not setup steps.
`--backup` creates a fresh native backup and awaits delivery; it may apply the
source's configured GHES retention policy. Use it only in an approved test
window. `BACKUPFABRIC_SMOKE_TIMEOUT_MS` sets the per-job runner deadline
(default 30 minutes; integer range 1000-86400000). A runner timeout does not
cancel the service job; inspect the existing job before retrying.

Control-plane inventory records discovery observations and distinguishes pending
collection from retained unqualified histories. Gateway provisioning, automatic
enrollment, storage boot persistence, production image/security review, full
metadata preservation, and GHES restore qualification remain separate work.