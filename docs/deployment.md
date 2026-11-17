# Deployment and private data

Follow the [README](../README.md) for DNS, ports and initial Compose setup.
The service uses Linux host networking, runs as an unprivileged UID, drops all
container capabilities and has a read-only root filesystem. Only `data/` is
mounted writable. Its owner must match `QUETZAL_UID` and `QUETZAL_GID`.

## State and upgrades

Stop the service before backing up `data/state/`. Preserve every file together,
including `principal.key`, accounts, ranking and community records. Never start
two instances against one state directory. An exclusive lock also enforces this.
Restoring an older binary against newer state formats has not been validated;
keep a matching image and stopped-state backup for rollback.

Transient rooms are not persisted. Players must sign in again and create fresh
rooms after a restart. The stock game can cache an expired room ID, so leave
online mode completely or reboot without loading a save state.

## Rename from the development project

The earlier development name was Eyetwo. Quetzal uses `cmd/quetzal`, the
`QUETZAL_` environment prefix, and the `quetzal` Compose project/service.
Old `EYETWO_` variables are not read. Stop the old Compose service before
starting Quetzal to avoid listener conflicts. Move the contents of the old
`work/state/` directory into `data/state/` without changing keys or account files.
Keep the old `.env` private and translate its variable names.

Captures, generated probes, cached builds, extracted disc assets and Ghidra
projects are development artifacts. Archive them outside the repository if
needed; they are not required to run the server.

## Accounts and diagnostics

Retail authentication requires password-derived hashes that act as account
secrets. The Eidos profile format may also contain contact and personal fields
entered by players. Quetzal persists those records to serve the unmodified
client; keep state and backups private and use game-specific passwords.

Raw packet recording is **off by default**. For a temporary investigation, set
`QUETZAL_CAPTURE_DIR=/data/captures` in `.env` and recreate the container. The
recorder stores full inbound/outbound backend payloads in mode-0600 files,
rotating at 16 MiB with three backups. These files can contain account proofs,
profile fields and addresses. Clear the variable and recreate the container
when finished. Native relay payloads are forwarded without being recorded by
this backend recorder.

`QUETZAL_LOG_LEVEL=debug` enables packet metadata. Normal JSON logs are rotated
separately by Docker. Avoid publishing raw logs or captures in issues; share a
redacted protocol/method and the observed game behavior instead.

## Connection modes

`direct` publishes real peer addresses. `relay` publishes proved virtual
endpoints for native traffic. `adaptive` offers direct first and a native
fallback candidate. Stock direct play and forced-relay gameplay are confirmed;
blocked-direct adaptive fallback remains a field test. All players must reconnect
and create fresh rooms after a mode change. Existing direct sessions do not
migrate to the relay during a mid-game failure.

Forward the configured relay UDP pool as well as backend ports when hosting
behind NAT. DNS and a TCP-80 patch-check web server are separate services.
