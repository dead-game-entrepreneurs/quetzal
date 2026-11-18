<p align="center">
  <img src="docs/assets/quetzal-logo.png" alt="Quetzal" width="640">
</p>

<h3 align="center">Bringing Quazal games back online</h3>

<p align="center">
  A replacement for <b>Quazal Rendez-Vous / NetZ</b> online services,<br>
  with a tested <b>25 to Life PS2</b> adapter and an experimental<br>
  <b>Test Drive Unlimited PS2</b> backend, using stock client protocols.
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white">
  <img alt="Docker Compose" src="https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white">
  <img alt="PlayStation 2" src="https://img.shields.io/badge/PlayStation%202-online-003791?logo=playstation&logoColor=white">
</p>

<p align="center">
  <a href="#status">Status</a> ·
  <a href="#dns-redirects">DNS redirects</a> ·
  <a href="#running-the-server">Running the server</a> ·
  <a href="docs/games/25tolife/analysis.md">25 to Life notes</a> ·
  <a href="docs/games/tdu/online-map.md">TDU notes</a>
</p>

---

## Status

**25 to Life** (US PS2, `SLUS-210.16`, v1.01) is the retail-tested adapter.
These results apply to 25 to Life:

| Feature | Status |
| --- | --- |
| Account creation, recovery and login | ✅ tested in game |
| Hosting, browsing, filtered search and joining | ✅ tested in game |
| Direct peer-to-peer gameplay | ✅ two players, including next-round transition |
| Native relay gameplay | ✅ joining, match start, movement and shooting |
| Host quitting and session cleanup | ✅ tested through the relay |
| Friends and friend requests | ✅ tested in game |
| Game invite notification and joining | ✅ tested in game |
| Clans | ✅ tested in game; individual operations still need wider coverage |
| Aggregate and personal round statistics | ✅ both players' reports stored |
| Adaptive relay fallback | 🧪 implemented; blocked-direct field test pending |
| Full-match reporting and original ranking/scoring formulas | 🧪 further validation needed |

Retail testing used two unmodified PCSX2 Sockets clients. Real consoles are a
project target; physical-console testing remains open. No PNACH, modified disc
or emulator patch is required. Both players ready up before starting; the stock
game's Force Start restrictions remain in place.

See the [25 to Life protocol inventory](docs/games/25tolife/protocol-inventory.md)
for implemented operations and remaining SDK coverage.

**Test Drive Unlimited** (US PS2, `SLUS-214.90`) is an experimental adapter
ready for community compatibility testing. Local PCSX2 testing has confirmed
account creation, login, configuration download and online entry in the stock game.
Multiplayer driving and racing still need validation:

| Feature | Status |
| --- | --- |
| Account creation, login and native startup | ✅ online entry confirmed with one local PCSX2 client |
| Cruising groups, discovery and nearby players | 🧪 implemented; two-player driving unverified |
| Race hosting, browsing, restrictions and session cleanup | 🧪 implemented; race start/finish unverified |
| Direct peer lookup and NAT introductions | 🧪 implemented; real NAT combinations unverified |
| Challenge upload/download and reported statistics | 🧪 persistence and queries tested; stock UI unverified |
| Activity-map density | 🧪 implemented; optional US-build metadata enables mission counters |
| TDU relay and full social/SDK coverage | Not implemented or established |

Tests exercise two synthetic drivers over real UDP and selected retail routines
in native-code harnesses. **Two-player cruising and racing remain unverified.** The
stock clients own driving and race simulation. See the
[TDU testing guide](docs/games/tdu/testing.md),
[public-endpoint startup notes](docs/games/tdu/nat-probe.md),
[run guide](docs/games/tdu/running.md) and
[protocol map](docs/games/tdu/online-map.md).

Both adapters can run in one process, with separate state, sockets and captures.
Other Quazal titles still need their own adapters and verification.

## How it fits together

25 to Life's tested connection path:

```text
 PS2 / PCSX2 ──DNS──▶ your resolver ──▶ Quetzal's server address
      │
      ├── DNAS ────────────────────▶ separately configured DNAS service
      ├── account / lobby / social ▶ Quetzal (Go, UDP)
      ├── 25 to Life patch check ─▶ HTTP no-update response on port 80
      └── gameplay ────────────────▶ peer directly, or Quetzal's native relay
```

DNS and DNAS are configured separately. Gameplay stays in the retail client's
native protocol; Quetzal handles accounts, matchmaking, social services,
statistics and optional peer forwarding.

## DNS redirects

For **25 to Life**, point these exact names at your Quetzal server's reachable
IPv4 address:

```text
ttlliveps2.quazal.net
ttl1.quazal.net
ttl2.quazal.net
ttl3.quazal.net
ttl1.patch.eidos.com
ttl2.patch.eidos.com
ttl3.patch.eidos.com
```

Set the console's network profile or PCSX2 DNS to your resolver. Keep your DNAS
routing configured separately. [configs/25tolife.dns.yaml](configs/25tolife.dns.yaml)
is an example for revive-dns; replace its documentation address.

The game requests `/TTL/final-version.txt` over **TCP 80**. Serve an empty HTTP
404 for that path through your existing web server, or reverse proxy it to
Quetzal's HTTP listener. Quetzal's default HTTP port is 8082; DNS alone does not
redirect port 80 to it. The tested setup used the empty-404 no-update path.

For **TDU**, redirect `tdulive.quazal.net` to its reachable server IPv4 and keep
DNAS separate. See the [TDU network setup](docs/games/tdu/running.md#network-setup-and-ports).

## Running the server

You need Docker with Compose on a host supporting Linux host networking.

```sh
git clone https://github.com/dead-game-entrepreneurs/quetzal.git
cd quetzal
cp .env.example .env
chmod 600 .env
mkdir -p data
chmod 700 data
```

Edit `.env`: set `QUETZAL_PUBLIC_IP` to the address players reach this server on
(your LAN address for a home test, public IPv4 for an Internet deployment).
Set `QUETZAL_UID` and `QUETZAL_GID` to the owner of `data/` if different from
1000. Then:

```sh
docker compose up -d --build --wait
docker compose logs -f quetzal
```

`QUETZAL_GAMES` defaults to `25tolife`, preserving existing installations.
Use `tdu` for TDU alone. For both games with 25 to Life relay/adaptive mode,
set these values in `.env` before starting:

```dotenv
QUETZAL_GAMES=25tolife,tdu
QUETZAL_RELAY_FIRST_PORT=30400
QUETZAL_RELAY_LAST_PORT=30527
```

The old relay default **30200–30327 overlaps TDU's 30200/30201 listeners**.
Startup rejects overlaps with static or lazy-reserved UDP ports and never
silently changes ranges. Unknown or duplicate game keys also fail startup.
TDU-only mode does not load 25 to Life relay settings.

Allow or forward these **25 to Life** ports when that adapter is enabled:

| Port | Purpose |
| --- | --- |
| UDP 30120 | Authentication |
| UDP 30121 | Ticket-authenticated services |
| UDP 10100, 10105, 10110 | Alternate authentication endpoints |
| UDP 30200–30327 by default; 30400–30527 in the combined example | On-demand 25 to Life native relay endpoints |
| TCP 80 | Retail patch check, served by your web server |
| TCP 8082 | Quetzal HTTP health and connectivity diagnostics |

TDU additionally requires these physical UDP listeners:

| Port | Purpose |
| --- | --- |
| UDP 30200 | TDU authentication |
| UDP 30201 | TDU ticket-authenticated services |
| UDP 8124 | TDU native master assignment |
| UDP 8122 | TDU encrypted native proxy and local services |

Native service ports 9000–9004 are logical destinations inside the tunnel, not
listeners. They need no firewall rules.
The [TDU guide](docs/games/tdu/running.md) lists overrides and current limits.

Compose uses host networking to preserve observed peer addresses. 25 to Life
accounts, profiles, friends, clans and statistics keep their existing private
files under `data/state/`; its transient rooms are removed on host departure or
restart. TDU uses separate keys/accounts under `data/state/tenants/tdu/`.
Stop with `docker compose down`. The `data/` directory stays on disk.

After restarting the backend or changing connection modes, leave online mode
completely and reconnect both players. Reboot the games without loading save
states if an old room ID remains cached.

### Settings

| Variable | Default | Purpose |
| --- | --- | --- |
| `QUETZAL_GAMES` | `25tolife` | Enabled adapters: `25tolife`, `tdu`, or both comma-separated |
| `QUETZAL_PUBLIC_IP` | bind address; set explicitly for hosting | Reachable server IPv4 |
| `QUETZAL_BIND` | `127.0.0.1` (example uses `0.0.0.0`) | Local listener address |
| `QUETZAL_HTTP_PORT` | `8082` | HTTP diagnostics and patch handler |
| `QUETZAL_UID`, `QUETZAL_GID` | `1000` | Compose process UID and GID |
| `QUETZAL_RELAY_MODE` | Compose: `adaptive`; binary: `direct` | 25 to Life connection policy |
| `QUETZAL_RELAY_FIRST_PORT`, `QUETZAL_RELAY_LAST_PORT` | `30200`, `30327` | 25 to Life relay pool; use `30400`, `30527` alongside TDU |
| `QUETZAL_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `QUETZAL_CAPTURE_DIR` | empty (disabled) | Optional private packet capture directory |
| `QUETZAL_STATE_DIR` | binary: `data/state`; Compose: `/data/state` | Persistent state directory |

TDU-specific `QUETZAL_TDU_*` settings are [listed separately](docs/games/tdu/running.md#settings)
and are read only when TDU is enabled. `/healthz` reports all enabled tenants.
HTTP handlers are scoped under `/games/{key}/`; 25 to Life also keeps its original
`/TTL/` patch routes and `/connectivity` diagnostics.

### 25 to Life connection modes

- **`direct`**: introduce players and let native gameplay connect directly.
- **`relay`**: return only virtual peer endpoints and forward gameplay through
  the server. This path is confirmed with two stock clients.
- **`adaptive`**: offer the direct host first and a relay alternative. Selecting
  the alternative activates forwarding after both endpoints prove their NAT
  mappings. Fallback with direct traffic blocked still needs field testing.

The relay uses two UDP ports per admitted peer pair. It does not migrate an
already connected direct match to the relay after a mid-game network failure.
See [connectivity notes](docs/games/25tolife/connectivity.md) for bounds and
protocol details.

### Community testing and debug logs

For a hosted testing window, keep these values in the server's private `.env`:

```dotenv
QUETZAL_LOG_LEVEL=debug
QUETZAL_CAPTURE_DIR=/data/captures
```

Recreate the service after changing environment settings. Follow structured logs
with `docker compose logs --since 10m --timestamps -f quetzal`. Each entry carries
a tenant/game label. Quazal RPC logs identify the protocol, method, call ID,
account/connection IDs and peer; TDU native debug logs identify the logical port, envelope, action,
transaction and transport generation. Distinct rejected actions remain visible
at debug level. Payloads and account secrets are not added to these log events.

Docker rotates JSON logs at 10 MiB with three files. Optional packet captures
rotate separately and contain private full payloads; see the privacy notes below.
For multi-game captures, preserve both tenant subdirectories when upgrading an
existing single-game capture setup. Operators should correlate a tester's exact
time, build/region, menu action and error with server logs. Players can follow
[TDU's testing/reporting steps](docs/games/tdu/testing.md). Physical-console,
two-client gameplay and public-network compatibility still need validation.

### Accounts and privacy

Players create accounts from the game's online menu. The legacy protocol sends
a password-derived hash that serves as an authentication secret. Account files
also retain the contact and profile fields supplied by the client. Keep
`data/`, `.env` and backups private; they are ignored by Git.

Raw capture is disabled by default. For protocol research, set
`QUETZAL_CAPTURE_DIR=/data/captures` in Compose. With multiple games, separate
recorders write to `/data/captures/25tolife/` and `/data/captures/tdu/`; a single
game keeps the legacy path directly under `/data/captures/`. Captures include
full backend payloads and can contain account secrets and profile information.
Each recorder rotates at 16 MiB with three backups. Routine JSON logs contain
operation metadata;
packet-level address/length events require `QUETZAL_LOG_LEVEL=debug`.

See [deployment and backup notes](docs/deployment.md) before moving an existing
installation. Quetzal is an independent compatibility project, unaffiliated
with Quazal, Eidos, Atari or the games' publishers.

## Development

The pinned Go toolchain runs in Docker; a local Go install is optional.

```sh
scripts/go test ./...
scripts/go vet ./...
scripts/go build ./cmd/quetzal
```

| Path | Contents |
| --- | --- |
| `cmd/quetzal` | Entry point and explicit game registration |
| `internal/app` | Configuration, listeners, state lock and shutdown |
| `internal/game` | Game adapter contract |
| `internal/protocol/prudp` | Framing, checksums, reliability, retries and raw/LZO payloads |
| `internal/protocol/quazal` | RMC, authentication, tickets, registration and account storage |
| `internal/quazalcrypto` | Retail key derivation and ticket containers |
| `internal/games/ttl25` | 25 to Life codecs, matchmaking, friends, clans, stats and native relay |
| `internal/games/tdu` | TDU adapter, native transport, sessions/presence, peer admission and title-data services |
| `internal/state` | Private keys and exclusive tenant state ownership |
| `internal/capture` | Optional bounded private recording |
| `tools/`, `ghidra/scripts/` | Local disc analysis helpers; not used by the server |

The layout follows [Angelware](https://github.com/princebyyourlogic/angelware).
Game-specific behavior stays in its adapter; the two titles share verified
PRUDP, Quazal authentication and account primitives with explicit title policy. Go tests use synthetic accounts and isolated
fixtures. See [architecture](docs/architecture.md) and
[research instructions](docs/research.md).

### Game files

This repository contains no disc images, game executables, decompiler exports,
packet captures or live account state. Research tools read your own disc into
ignored local directories. Keep those files out of contributions.
