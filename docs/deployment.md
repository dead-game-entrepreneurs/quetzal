# Deployment and private data

Follow the [README](../README.md) for DNS, ports and initial Compose setup.
The service uses Linux host networking, runs as an unprivileged UID, drops all
container capabilities and has a read-only root filesystem. Only `data/` is
mounted writable. Its owner must match `QUETZAL_UID` and `QUETZAL_GID`.

## State and upgrades

Stop the service before backing up `data/state/`. Preserve every file together,
including every tenant's keys and account files. 25 to Life retains
`principal.key`, accounts, ranking and community records at the state root.
TDU uses separate `principal.key`, `accounts.json` and `p2p.key` under
`tenants/tdu/`, plus versioned `title-data.json` for challenge files and reported
statistics. Enabling TDU never moves 25 to Life files. Never start
two instances against one state directory. An exclusive lock enforces ownership
for each tenant state location.
Restoring an older binary against newer state formats has not been validated;
keep a matching image and stopped-state backup for rollback.

Transient rooms are not persisted. Players must sign in again and create fresh
rooms after a restart. The stock game can cache an expired room ID, so leave
online mode completely or reboot without loading a save state.

## Selecting games and reserving ports

`QUETZAL_GAMES` defaults to `25tolife`. Set it to `tdu` or `25tolife,tdu` to
enable the experimental TDU adapter. Unknown or duplicate keys are rejected.
Only enabled adapters read their game settings. Shared HTTP and all enabled
UDP services start together; configuration, bind or runtime failure stops the
whole instance rather than leaving a partially running selection.

For both games with 25 to Life relay/adaptive mode, set an explicit nonoverlapping
relay pool in `.env`:

```dotenv
QUETZAL_GAMES=25tolife,tdu
QUETZAL_RELAY_MODE=adaptive
QUETZAL_RELAY_FIRST_PORT=30400
QUETZAL_RELAY_LAST_PORT=30527
```

The old 30200–30327 pool includes TDU's UDP 30200/30201 authentication/secure
listeners. Quetzal validates static and lazy-reserved UDP ranges before binding
and rejects overlaps; it does not automatically change ranges. Forward the new
relay pool and TDU's physical UDP 30200, 30201, 8124 and 8122 alongside the
existing 25 to Life ports. TDU's logical 9000–9004 native-service destinations
are tunnel fields, not firewall ports. Direct-only 25 to Life reserves no relay
pool.

Keep the existing 25 to Life DNS and TCP-80 patch route. TDU additionally needs
`tdulive.quazal.net` directed to its advertised IPv4, with DNAS routing separate.
See the [TDU run guide](games/tdu/running.md) for `QUETZAL_TDU_*` overrides,
generated policies and recovered startup handoff. Session/discovery, peer
introductions and title services are implemented; stock online entry, cruising
and race completion still require community validation.

### Preparing a combined hosted test

Build/test the intended commit before restarting the live service. Keep the old
image, private `.env` and a stopped-state backup together for rollback. Do not
replace 25 to Life accounts or keys with local test state. Set the combined-game
and relay values above, retaining `QUETZAL_LOG_LEVEL=debug` and the existing
capture path for this testing window. New title-data storage preserves legacy SDK
text bytes across restarts; older v1 TDU files migrate on a successful write.

On a host managed by UFW, allow the additional ports before switching the running
image. For the default TDU ports and the combined relay example:

```sh
sudo ufw allow 8122/udp comment 'Quetzal TDU native proxy'
sudo ufw allow 8124/udp comment 'Quetzal TDU native master'
sudo ufw allow 30200:30201/udp comment 'Quetzal TDU auth and secure'
sudo ufw allow 30400:30527/udp comment 'Quetzal 25 to Life native relay'
sudo ufw status
```

Existing 25 to Life listeners remain required. If the old relay rule already
allows 30200–30327, it covers TDU's 30200/30201; retain it until after the switch.
Do not remove a firewall rule before confirming no other service needs it. A
provider firewall or router must allow the same UDP destinations. TCP8082 can
remain restricted to local diagnostics; the retail patch request uses TCP80.

Add exactly `tdulive.quazal.net` → the host's advertised IPv4 to the existing DNS
service, retaining every 25 to Life and DNAS record. The included
[TDU DNS example](../configs/tdu.dns.yaml) is for revive-dns. An existing
`tsfp_online --dns` deployment instead accepts the same record through its
`--dns-static-a tdulive.quazal.net=SERVER_IPV4` option; update its persistent
Compose configuration rather than editing a generated container command.
Verify both names through the actual public resolver:

```sh
dig +short @SERVER_IPV4 ttlliveps2.quazal.net A
dig +short @SERVER_IPV4 tdulive.quazal.net A
```

Both must return the advertised server IPv4. A system resolver answer is not a
check of the game resolver. DNS/container recreation is a separate maintenance
step; preserve the existing DNAS addresses and other games' mappings.

After starting the intended build, check `/healthz` for both tenant keys,
`/games/tdu/connectivity` and the preserved `/connectivity` and `/TTL/` routes.
Confirm the four TDU listeners and existing 25 to Life listeners with `ss -lunp`.
The relay pool binds on demand, so an unused pool need not appear in `ss`.
Confirm effective debug/capture settings in the running container, then correlate
the first fresh-boot tester connection with logs. Run host/UDP probes without
claiming that they establish a playable retail session.

When ordinary mission activity counters are desired, install the private
0600 activity catalog under the data mount and configure
`QUETZAL_TDU_ACTIVITY_CATALOG=/data/resources/activity-catalog.json`. This is
optional protocol metadata, not an ISO/asset dependency; see
[the catalog notes](games/tdu/presence.md). FreeRide and Drive-In discovery do not
require the catalog. Keep captures/backups outside Git.

### Adding Snowblind to an existing host

Enable `snowblind` alongside the existing game selection. Preserve every live
tenant's keys, accounts and state; do not copy local emulator-test state to the
host. Snowblind creates its own `tenants/snowblind/` containing accounts,
principal key, Ranking metadata, friends, clans and receipt-based statistics.
Keep the matching pre-upgrade image, private environment/configuration and a
stopped-state backup before replacing the service.

Its defaults are UDP 13015/13016 and adaptive relay UDP 30800–30927. Check
other hosted services as well as Quetzal tenants before choosing a pool. For
example, a host with Commandos already using 30800–30927 can retain that setting
and assign Snowblind a separate pool:

```dotenv
QUETZAL_GAMES=25tolife,tdu,wcc,commandos,snowblind
QUETZAL_SNOWBLIND_PAYLOAD_MODE=plain
QUETZAL_SNOWBLIND_RELAY_MODE=adaptive
QUETZAL_SNOWBLIND_RELAY_FIRST_PORT=31000
QUETZAL_SNOWBLIND_RELAY_LAST_PORT=31127
```

Allow UDP 13015–13016 and the chosen relay pool through the host/provider
firewall. Relay sockets bind on demand. Add all four names from
[the Snowblind DNS example](../configs/snowblind.dns.yaml) to the persistent
resolver configuration; retain the other games and separate DNAS answers.
Route TCP80 requests for the three patch hosts to the Quetzal manifest:

```nginx
server {
    listen 80;
    server_name sb1.patch.eidos.com sb2.patch.eidos.com sb3.patch.eidos.com;
    location /psb1a/ {
        proxy_pass http://127.0.0.1:8082;
    }
    location / { return 404; }
}
```

Test nginx configuration before reloading. Check `/psb1a/scea.txt` returns
HTTP200 with the two-line no-update manifest in the
[run guide](games/snowblind/running.md#dns-and-http), and that the existing
25 to Life `/TTL/final-version.txt` still returns an empty HTTP404.
Verify all four A records through the actual game resolver, then `/healthz`
for all five tenants and `/games/snowblind/connectivity` for `adaptive` mode.
Check the selected relay range in the running container's `QUETZAL_SNOWBLIND_RELAY_*` settings. UDP handshake probes establish endpoint reachability;
retail Internet join/gameplay still needs a client test. Keep existing capture
preferences; raw capture remains disabled by default for new installations.

The 2026-11-24 RackNerd deployment retained the existing 25 to Life
30400–30527 and Commandos 30800–30927 adaptive pools, adding Snowblind on
31000–31127. Public resolver checks passed for all fourteen Quetzal names and
the three preserved regional DNAS answers. Both games' patch HTTP responses,
all five adapters' auth/secure SYN replies, and UDP reachability at both ends
of the Snowblind relay pool passed. Production state was backed up while stopped;
existing state-file checksums matched after startup. These deployment probes
complement the LAN retail tests; Internet gameplay still needs client validation.

### Moving a standalone TDU research installation

Stop the old standalone process and Quetzal before moving state. Preserve its
TDU principal key, account store and peer key together under
`QUETZAL_STATE_DIR/tenants/tdu/` (`./data/state/tenants/tdu/` with Compose).
Do not mix those records with the 25 to Life files at the state root. Keep a
stopped-state backup before replacing any already-created TDU tenant files.

The Ghidra projects, extracted disc files, captures and research scripts stay in
the sibling `tdu-server` workspace. Quetzal has no runtime dependency on it.
No disc images or original XML assets are required to run the generated default
network/no-awards policy.

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
when finished. 25 to Life native relay payloads are forwarded without being
recorded by this backend recorder. TDU's native master/proxy endpoints do use the recorder,
including handshake and encrypted traffic in both directions.

With multiple tenants, recordings live in `/data/captures/25tolife/` and
`/data/captures/tdu/`, each with its own rotation limit. With one tenant, files
stay directly under the configured capture directory for compatibility. A
requested capture write failure stops the instance. `/healthz` lists enabled
tenants; HTTP handlers have scoped `/games/{key}/` mounts, while 25 to Life keeps
its original `/TTL/` and `/connectivity` routes.

`QUETZAL_LOG_LEVEL=debug` enables packet metadata. Normal JSON logs are rotated
separately by Docker. Avoid publishing raw logs or captures in issues; share a
redacted protocol/method and the observed game behavior instead.

Connection-close reasons and 25 to Life room/search summaries also identify
which filters excluded a room and which room IDs were returned. See
[hosted diagnostics](observability.md) for event names and correlation fields.

## 25 to Life connection modes

`direct` publishes real peer addresses. `relay` publishes proved virtual
endpoints for native traffic. `adaptive` offers direct first and a native
fallback candidate. Stock direct play and forced-relay gameplay are confirmed;
blocked-direct adaptive fallback remains a field test. All players must reconnect
and create fresh rooms after a mode change. Existing direct sessions do not
migrate to the relay during a mid-game failure.

Forward the configured relay UDP pool as well as backend ports when hosting
behind NAT. DNS and a TCP-80 patch-check web server are separate services.
