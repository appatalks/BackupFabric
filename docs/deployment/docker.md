# Ubuntu container deployment

Ubuntu 24.04 LTS in Docker is the initial supported BackupFabric deployment
envelope. The container is still a standard Linux deployment: it does not run
inside GHES and has no dependency on a GHES appliance filesystem.

## Host requirements

- Linux host with Docker Engine and the Compose plugin
- Persistent local or block-backed storage for the catalog volume
- Network access to registered GHES administrative SSH endpoints
- Network access to infrastructure-provider APIs
- Host-level TLS reverse proxy or an SSH tunnel before remote administration

The supplied Compose configuration uses host networking intentionally.
BackupFabric continues to bind to `127.0.0.1:8080`, so its unauthenticated
experimental API is not published to the LAN. Host networking is supported by
Docker Engine on Linux; this Compose configuration is not intended for Docker
Desktop.

## Start the service

```bash
docker compose up --build -d
docker compose ps
curl --fail http://127.0.0.1:8080/api/v1/health
```

Open `http://127.0.0.1:8080` from the Linux host.

The SQLite catalog is stored in the `backupfabric-data` named volume at:

```text
/var/lib/backupfabric/backupfabric.db
```

The container runs as UID and GID `10001`, drops all Linux capabilities, uses a
read-only root filesystem, and enables `no-new-privileges`.

## Operate the CLI

Run short-lived CLI commands in the existing service container:

```bash
docker compose exec backupfabric backupfabric status

docker compose exec backupfabric backupfabric appliances add \
  --name production-a \
  --hostname ghe-a.example.com \
  --volume vol-0123456789

docker compose exec backupfabric backupfabric appliances list
```

The image includes OpenSSH and rsync for experimental live workflows. No SSH
private keys should be baked into the image. Production credentials must be
provided at runtime through a secret manager or a narrowly scoped read-only
secret mount.

For the dedicated-receiver GUI workflows, use the optional live Compose
overlay and follow [the live workflow guide](live-workflows.md).

## Remote administration

Until API authentication and TLS are implemented, use an SSH tunnel:

```bash
ssh -L 8080:127.0.0.1:8080 backupfabric-host
```

Then browse to `http://127.0.0.1:8080` on the administrator workstation.

Do not change the application listen address to `0.0.0.0`. The current binary
rejects non-loopback binding deliberately.

## Upgrade

Before replacing the container:

1. Stop collection operations.
2. Back up the `backupfabric-data` volume.
3. Build or pull the new immutable image.
4. Run database migrations by starting the new container.
5. Confirm the health endpoint and catalog contents.

The catalog volume is independent of the container lifecycle. Removing the
container does not remove the named volume; `docker compose down -v` does.

## Production boundary

The container provides process packaging, not backup-data isolation. Source
separation is enforced by dedicated storage volumes and provider permissions.
The Docker socket must never be mounted into BackupFabric.
