# Running Test Drive Unlimited in Quetzal

The `tdu` adapter targets US PS2 **SLUS-214.90**. It implements account/ticket
flows, native startup, directory/resources, session registration and discovery,
nearby-player/map queries, direct-peer admission, reported statistics and
challenge-file persistence. Two synthetic drivers exercise the combined flow
through real UDP sockets. **One local PCSX2 client has completed stock online
entry**, including account creation, login and configuration downloads.
Two-client cruising, racing, physical consoles and public NAT combinations
remain unverified.

## Enable the adapter

Use Quetzal's normal [Compose setup](../../../README.md#running-the-server).
Set the reachable IPv4 and choose the enabled games in `.env`:

```dotenv
QUETZAL_PUBLIC_IP=203.0.113.10
QUETZAL_BIND=0.0.0.0
QUETZAL_GAMES=tdu
```

Replace the example address. Then start the single Quetzal process:

```sh
docker compose up -d --build --wait
docker compose logs -f quetzal
```

`QUETZAL_GAMES` defaults to `25tolife`; accepted keys are `25tolife` and `tdu`.
Unknown or duplicate keys fail startup. TDU settings and state are loaded only
when `tdu` is enabled. A TDU-only deployment does not load 25 to Life relay
configuration.

To run both games on the same host, explicitly choose a nonoverlapping 25 to Life
relay pool whenever its relay or adaptive mode is enabled:

```dotenv
QUETZAL_GAMES=25tolife,tdu
QUETZAL_RELAY_MODE=adaptive
QUETZAL_RELAY_FIRST_PORT=30400
QUETZAL_RELAY_LAST_PORT=30527
```

The existing relay default **30200–30327 overlaps TDU authentication ports
30200/30201**. Quetzal validates static listeners and lazy relay reservations
before binding and rejects conflicts; it does not silently move either range.
Direct-only 25 to Life does not reserve a relay pool. Keep all existing 25 to Life
DNS/patch-check routing when enabling TDU alongside it.

## Network setup and ports

Add `tdulive.quazal.net` → the server's reachable IPv4 to the existing resolver.
Keep the user's working DNAS routing separate. Quetzal does not change DNS,
router rules, certificates, game files or another revival service.

| Physical listener | TDU purpose |
| --- | --- |
| UDP 30200 | Quazal authentication and service-ticket issuance |
| UDP 30201 | Ticket-authenticated secure connection, registration, signup and bootstrap lookup |
| UDP 8124 | Native US/revision-4392 master assignment |
| UDP 8122 | Native signed DH handshake, encrypted transport activation and registered local services |
| TCP 8082, shared | Quetzal HTTP diagnostics; `/healthz` reports enabled tenants |

Allow/forward the four UDP listeners. Preserve the stock ports for retail tests:
the client has fixed native destinations and the master response does not
advertise an alternative proxy port. Port overrides, including ephemeral port 0,
are useful for diagnostics and integration tests, not proof that an unmodified
client can follow a changed listener.

**9000–9004 are logical edNet destinations inside the encrypted proxy. They are
not additional UDP listeners and need no firewall forwarding.** The native master
reply supplies the directory IPv4 used with the compiled logical directory port
9000. This handoff is reproduced by executing the stock reader and startup
continuation. See [startup evidence](startup-handoff.md).

## Settings

TDU inherits `QUETZAL_BIND` and `QUETZAL_PUBLIC_IP` unless overridden below.
HTTP, logging, capture and state-root configuration remain shared process settings.

| Variable | Default | Meaning |
| --- | --- | --- |
| `QUETZAL_TDU_BIND` | global bind | TDU's local IPv4 |
| `QUETZAL_TDU_PUBLIC_IP` | global public IP | Reachable TDU IPv4 advertised to the client |
| `QUETZAL_TDU_AUTH_PORT` | `30200` | Authentication UDP listener |
| `QUETZAL_TDU_SECURE_PORT` | `30201` | Secure-service UDP listener |
| `QUETZAL_TDU_MASTER_PORT` | `8124` | Native master UDP listener |
| `QUETZAL_TDU_PROXY_PORT` | `8122` | Native proxy UDP listener |
| `QUETZAL_TDU_DIRECTORY_PORT` | `9000` | Logical directory port |
| `QUETZAL_TDU_RESOURCE_PORT` | `9001` | Logical resource port; directory-advertised |
| `QUETZAL_TDU_SESSION_PORT` | `9002` | Logical SessionMaster/time/activity port |
| `QUETZAL_TDU_DENSITY_PORT` | `9003` | Logical nearby-player and map-density port |
| `QUETZAL_TDU_USERMAP_PORT` | `9004` | Logical player-position update port |
| `QUETZAL_TDU_ACTIVITY_CATALOG` | unset | Optional private activity-name/slot metadata; see [presence](presence.md) |
| `QUETZAL_TDU_NETCONFIG` | generated policy | Optional private `netconfig.xml` override path |
| `QUETZAL_TDU_PRIZE` | generated policy | Optional private `prize.xml` override path |

Set directory and resource ports to 0 to disable the native directory and its
services for diagnostics. A resource port requires a directory port; XML overrides
require resources. With the directory enabled, session, density and USERMAP ports
are required. Enabled logical ports must be distinct.

With resources enabled, Quetzal generates network XML containing all **39
recovered compiled client defaults**, prize XML declaring **no pending awards**,
and a persistent sixteen-byte `p2pkey`. No original XML assets are required.
These are explicit server-owned policies based on the exact readers. Only implemented service actions are advertised. Only the three named resources
are served; client filenames do not open host paths.

Overrides must be private regular files (0600), contain exactly one XML document,
and fit 1..4096 bytes. In Compose, put them under `./data/` and use their container
paths, for example `QUETZAL_TDU_NETCONFIG=/data/tdu/netconfig.xml`. No extra asset
mount is needed. XML validity alone does not establish correct game semantics;
follow the [recovered schema and defaults](native-services.md#configuration-resources-and-server-owned-policies).

Activity density always supports the recovered Drive-In aggregate. Ordinary
activity slots require the optional private US-build catalog generated by the
research tool; absent metadata leaves those slots empty. The runtime loads only
bounded name/index facts, never game assets or an ISO. Set the catalog path to a
private 0600 JSON file inside the Compose data mount. See
[activity catalog evidence](presence.md).

## Tenant state and capture

TDU state lives in **`QUETZAL_STATE_DIR/tenants/tdu/`**, including when TDU is the
only enabled game. Compose maps that to `./data/state/tenants/tdu/` on the host:

- `principal.key`: private random sixteen-byte service-ticket key.
- `accounts.json`: TDU accounts/PIDs and signup-derived initial hashes.
- `p2p.key`: persistent sixteen-byte peer-wrapper resource.
- `title-data.json`: atomic, private reported statistics and uploaded challenge files.
- The tenant's exclusive state lock.

25 to Life keeps its original state location at the state root; enabling TDU
does not move its accounts or principal key. The two games never share account
stores or keys. Back up the entire stopped state tree. A restart discards tickets,
transport sessions, positions, reservations and replay windows; start a fresh
client connection afterward.
For a migration from the earlier standalone research server, stop it before
copying its state files together into the TDU tenant directory. Do not merge them
with 25 to Life's state or replace already-created tenant keys independently.

Set `QUETZAL_CAPTURE_DIR=/data/captures` for a private investigation. With both
games enabled, recorder files live in `/data/captures/25tolife/` and
`/data/captures/tdu/`. With one enabled game, the legacy path remains directly
`/data/captures/`. Each recorder rotates at 16 MiB with three backups. TDU records
both directions on authentication, secure, native master and native proxy sockets;
failed capture writes stop the process. Raw packets may contain account proofs
and personal information. Recording is off by default.

`GET /healthz` lists the tenants, modes and bound UDP addresses. TDU reports an
experimental backend mode. `GET /games/tdu/connectivity` reports current
session/player counts, direct-peer policy and retail-validation status. It is not
a gameplay-completeness check. Logs carry tenant/game
labels, and `QUETZAL_LOG_LEVEL=debug` enables packet metadata.

## Validation and retail testing

From Quetzal's root:

```sh
scripts/go test ./...
scripts/go vet ./...
```

Tests cover ticket/crypto formats, signup/account login, secure proof and
bootstrap, native master/DH/activation, encrypted directory/resources, session
ownership/expiry, nearby-player/map data, peer lookup/NAT instructions, challenge
uploads/downloads and private statistics persistence. Native-executed fixtures
independently check selected retail writers/readers. Invalid input, replay,
shutdown and capture failures have separate coverage.

See the [dated validation report](validation.md) for native-fixture scope, the
isolated multi-tenant smoke and remaining limits.

For retail compatibility testing:

1. Back up state, enable private captures, and fresh-boot the documented US disc.
   Retain the existing DNAS route and redirect only `tdulive.quazal.net` to Quetzal.
2. Create/login an account and record the first rejected or unsupported exchange.
   Confirm master assignment, directory discovery and resource callbacks before
   treating the online world as connected.
3. Use two distinct accounts/clients to test nearby discovery, a shared group and
   bidirectional car movement. A room appearing in a list is insufficient.
4. Test a Drive-In/custom race, start, finish and return to cruising; then test
   host departure, fresh rejoin and differing NAT conditions separately.

The server does not simulate driving or decide an authoritative race winner.
Clients own peer gameplay; the backend owns discovery/admission and stores their
reports. Direct-peer NAT support is implemented; a validated TDU relay path is
not. Original ranking formulas, full social-service coverage and some activity
semantics remain unproven. See [sessions](sessionmaster.md),
[presence](presence.md), [peer connectivity](peer-connectivity.md),
[title operations](title-services.md) and [remaining milestones](roadmap.md).

Preserve the Ghidra project and private evidence in the sibling `tdu-server`
workspace. Quetzal never loads that workspace, an ISO or game executable at
runtime. No DNS, game-file or existing revival-service changes are performed by
starting this adapter.

## First retail startup correction

The first tester completed account login and registration but stalled before the
native master exchange. The missing initial NAT/public-URL probe is now answered
on the existing secure UDP listener. It uses a separate checksum from RPC and a
raw UDP response; see [the recovered exchange](nat-probe.md). No additional port
or game patch is required. The startup friend refresh also has its recovered
read-only [roster response](social.md); friendship mutations remain unsupported.
After this server update, fresh-boot or fully reconnect the game so its expired
public-URL job starts again.


## Stock online-entry confirmation

Local PCSX2 testing reached the game after two further startup corrections:
protocol 104/1 now returns raw network-order IPv4 bytes, and configuration
Open requests accept the retail callers' facility 2. See
[bootstrap evidence](startup-handoff.md#quazal-bootstrap-address-byte-order) and
[resource evidence](native-services.md#resource-wire-schema). The operator
confirmed successful online entry after these fixes; this is a single-client
result, with multiplayer acceptance still pending. After updating the server,
fresh-boot or fully reconnect the game. Existing accounts and keys are retained.
