<p align="center">
  <img src="docs/assets/quetzal-logo.png" alt="Quetzal" width="640">
</p>

<h3 align="center">Bringing Quazal games back online</h3>

<p align="center">
  A replacement for <b>Quazal Rendez-Vous / NetZ</b> online services,<br>
  with a tested <b>25 to Life PS2</b> adapter and experimental<br>
  <b>Test Drive Unlimited</b>, <b>World Championship Cards</b><br>
  <b>Commandos Strike Force</b> PS2 backends, and<br>
  experimental <b>Project: Snowblind</b> online services,<br>
  using stock client protocols.
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
  <a href="docs/games/tdu/online-map.md">TDU notes</a> ·
  <a href="docs/games/wcc/analysis.md">WCC notes</a> ·
  <a href="docs/games/commandos/running.md">Commandos guide</a> ·
  <a href="docs/games/snowblind/running.md">Snowblind guide</a> ·
  <a href="docs/games/rsvpsp/analysis.md">Rainbow Six Vegas PSP notes</a>
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
| FAQ-based skill ratings, top-ten rank fields and localized MOTD | 🧪 implemented and tested; stock-client display validation pending |

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

**World Championship Cards** (US PS2, `SLUS-213.40`) is an experimental adapter.
Up to three unmodified PCSX2 clients on one LAN have completed games:

| Feature | Status |
| --- | --- |
| Account creation and login, including names with spaces | ✅ tested in game on three PCs |
| Hosting, browsing, joining, Quick Join and in-table chat | ✅ tested in game |
| Peer card play to the end of the game | ✅ two and three players on one LAN |
| End-of-game table handoff and cleanup | ✅ handoff observed; cleanup fixed after testing |
| Hosting from any PC (server-initiated NAT probe) | ✅ tested in game |
| Three players at one table, including the host leaving mid-game | ✅ tested in game |
| Host leaving an open table and rejoining it | ✅ tested in game |
| Play across the Internet (different NATs) | 🧪 implemented; untested |
| Chat-lobby rooms (`ChatGathering`) | Not reachable in the retail menus; handled like tables if used |
| Voice, friends | Not implemented or established |

See the [WCC notes](docs/games/wcc/analysis.md).

**Commandos Strike Force** (US PS2, `SLUS-211.03`) is an experimental adapter.
Two unmodified PCSX2 clients on separate LAN PCs have verified these flows:

| Feature | Status |
| --- | --- |
| Account creation and login | ✅ tested in game |
| Hosting, browsing, filtered search and joining | ✅ tested in game |
| Map/mode updates, player counts, leave and Quick Match re-entry | ✅ tested in game |
| Adaptive relay fallback | ✅ LAN join with direct traffic blocked |
| Player host sharing the backend/relay IP | ✅ relay join tested in game |
| Password changes | 🧪 implemented and tested; stock UI unverified |
| Full-round gameplay, Internet NAT and physical consoles | 🧪 unverified |
| Host migration, voice and private/password-room semantics | Not implemented or validated end to end |
| Persistent stats, leaderboards, friends and clans | No active backend calls found in the inspected PS2 client |

Commandos uses the shared native relay with a separate UDP **30600–30727** pool.
Connections normally try direct first. When a player's gameplay IP matches the
relay IP, the adapter offers only the relay endpoint to avoid the game's browser
merging probes for different ports. See the
[Commandos run guide](docs/games/commandos/running.md) and
[service map](docs/games/commandos/online-map.md) for evidence and remaining coverage.

**Project: Snowblind** (US PS2, `SLUS-210.37`) is an experimental adapter.
Two unmodified PCSX2 clients on separate LAN PCs have verified these flows:

| Feature | Status |
| --- | --- |
| Account creation, login and Eidos profiles | ✅ tested in game |
| Hosting, browser search, joining and round entry | ✅ tested in game |
| Friend requests and acceptance | ✅ tested in game |
| Clan creation, invitations, acceptance and roster | ✅ tested in game; membership persists |
| Clan promotion and demotion | ✅ tested in game |
| Clan notices and Officer recruiting/notice permissions | 🧪 implemented and synthetically tested; retail confirmation pending |
| Round summaries and all 35 personal counters | ✅ both players' reports saved after a completed round |
| Top Players, Player Search, Friends and Clan stats | ✅ populated menus tested in game; reload and duplicate replay checks pass |
| Adaptive relay fallback | ✅ LAN join with direct peer UDP blocked; the same relay carried the completed round |
| Same-IP player hosting, host departure and Internet NAT | 🧪 unverified |
| Original ranking formula, clan kick/leave and leadership transfer | Not recovered or implemented |

Snowblind reuses shared Quazal authentication, Eidos profiles, Ranking bootstrap
and the native relay, with separate tenant state and a default UDP **30800–30927**
relay pool. Its current leaderboard order is a documented replacement policy.
See the [Snowblind run guide](docs/games/snowblind/running.md),
[protocol inventory](docs/games/snowblind/protocols.md) and
[statistics inventory](docs/games/snowblind/stats.md).

**Tom Clancy's Rainbow Six Vegas** (US PSP, `ULUS-10206`) is an early adapter
for PPSSPP:

| Feature | Status |
| --- | --- |
| Account creation with the profile form, and login | ✅ tested in PPSSPP |
| Creating, browsing and joining a match; playing it on one LAN | ✅ two PCs played a match (PPSSPP) |
| Multi-round matches: reports, next round, return to the lobby | ✅ two PCs played three rounds (PPSSPP) |
| Friends: requests, accept or deny, delete, friends list, joining a friend's game | 🧪 implemented from the disassembly; not yet tested in PPSSPP |
| Statistics: own, friends, participants, leaderboard (rating, rank, accuracy, play time) | 🧪 kept from match reports; not yet tested in PPSSPP |
| Game invitations | 🧪 empty list only, so the menu can open |
| Play across NATs | Not implemented |

See the [Rainbow Six Vegas notes](docs/games/rsvpsp/analysis.md) and
[test guide](docs/games/rsvpsp/testing.md).

All adapters can run in one process, with separate state, sockets and captures.
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

For **Test Drive Unlimited**, point this name at its reachable server IPv4:

```text
tdulive.quazal.net
```

See [configs/tdu.dns.yaml](configs/tdu.dns.yaml) and the
[TDU network setup](docs/games/tdu/running.md#network-setup-and-ports).

For **World Championship Cards**, point this name at its reachable server IPv4:

```text
wcclive.quazal.net
```

See [configs/wcc.dns.yaml](configs/wcc.dns.yaml).

For **Commandos Strike Force** (US PS2), point this name at its reachable server IPv4:

```text
cxliveSCEA.quazal.net
```

See [configs/commandos.dns.yaml](configs/commandos.dns.yaml) and the
[Commandos network setup](docs/games/commandos/running.md#network-setup).

For **Project: Snowblind** (US PS2), point these names at its reachable server IPv4:

```text
sbusps2.quazal.net
sb1.patch.eidos.com
sb2.patch.eidos.com
sb3.patch.eidos.com
```

The patch hosts request `/psb1a/scea.txt` over **TCP 80**. Reverse proxy
`/psb1a/` to Quetzal's HTTP listener (default 8082) for the no-update manifest.
Keep the separate 25 to Life empty-404 patch route intact. See
[configs/snowblind.dns.yaml](configs/snowblind.dns.yaml) and the
[Snowblind network setup](docs/games/snowblind/running.md#dns-and-http).

All DNS examples use a documentation address; replace it with your server's
address and preserve your separate DNAS routing.

For **Rainbow Six Vegas** (PSP), redirect `rsvegaspsp.quazal.net` and point
PPSSPP's DNS server at that resolver; see
[configs/rsvpsp.dns.yaml](configs/rsvpsp.dns.yaml). The game has no DNAS.

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

Use `commandos` or `wcc` for either game alone. To include all six adapters, use
`QUETZAL_GAMES=25tolife,tdu,wcc,commandos,snowblind,rsvpsp` with the same 25 to Life relay-pool
adjustment. Commandos and Snowblind have separate relay pools and title settings.

The old relay default **30200–30327 overlaps TDU's 30200/30201 listeners**.
Startup rejects overlaps with static or lazy-reserved UDP ports and never
silently changes ranges. Unknown or duplicate game keys also fail startup.
TDU-only mode does not load 25 to Life relay settings. WCC's auth port 30280
also falls inside the old default relay pool, so combine it with 25 to Life
relay/adaptive mode only after moving the pool as above.

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

Native TDU service ports 9000–9004 are logical destinations inside the tunnel,
not listeners. They need no firewall rules. The
[TDU guide](docs/games/tdu/running.md) lists overrides and current limits.

World Championship Cards requires:

| Port | Purpose |
| --- | --- |
| UDP 30280 | WCC authentication |
| UDP 30281 | WCC ticket-authenticated services |

Commandos Strike Force requires:

| Port | Purpose |
| --- | --- |
| UDP 30060 | Commandos authentication |
| UDP 30061 | Commandos ticket-authenticated services |
| UDP 30600–30727 | On-demand Commandos native relay endpoints |

Project: Snowblind requires:

| Port | Purpose |
| --- | --- |
| UDP 13015 | Snowblind authentication |
| UDP 13016 | Snowblind ticket-authenticated services |
| UDP 30800–30927 | On-demand Snowblind native relay endpoints |
| TCP 80 | Retail no-update manifest, through your web server |

Rainbow Six Vegas (PSP) requires:

| Port | Purpose |
| --- | --- |
| UDP 30380 | Rainbow Six Vegas authentication |
| UDP 30381 | Rainbow Six Vegas ticket-authenticated services |

Compose uses host networking to preserve observed peer addresses. 25 to Life
accounts, profiles, friends, clans and statistics keep their existing private
files under `data/state/`; its transient rooms are removed on host departure or
restart. Each other game uses separate keys/accounts under
`data/state/tenants/{game}/` (`tdu`, `wcc`, `commandos`, `snowblind` or `rsvpsp`).
Stop with `docker compose down`. The `data/` directory stays on disk.

After restarting the backend or changing connection modes, leave online mode
completely and reconnect both players. Reboot the games without loading save
states if an old room ID remains cached.

### Settings

| Variable | Default | Purpose |
| --- | --- | --- |
| `QUETZAL_GAMES` | `25tolife` | Enabled adapters, comma-separated: `25tolife`, `tdu`, `wcc`, `commandos`, `snowblind`, `rsvpsp` |
| `QUETZAL_PUBLIC_IP` | bind address; set explicitly for hosting | Reachable server IPv4 |
| `QUETZAL_BIND` | `127.0.0.1` (example uses `0.0.0.0`) | Local listener address |
| `QUETZAL_HTTP_PORT` | `8082` | HTTP diagnostics and patch handler |
| `QUETZAL_UID`, `QUETZAL_GID` | `1000` | Compose process UID and GID |
| `QUETZAL_RELAY_MODE` | Compose: `adaptive`; binary: `direct` | 25 to Life connection policy |
| `QUETZAL_RELAY_FIRST_PORT`, `QUETZAL_RELAY_LAST_PORT` | `30200`, `30327` | 25 to Life relay pool; use `30400`, `30527` alongside TDU or WCC |
| `QUETZAL_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `QUETZAL_CAPTURE_DIR` | empty (disabled) | Optional private packet capture directory |
| `QUETZAL_TTL_MOTD_FILE` | unset | Optional localized 25 to Life announcement JSON; reloads on each MOTD request |
| `QUETZAL_STATE_DIR` | binary: `data/state`; Compose: `/data/state` | Persistent state directory |

TDU-specific `QUETZAL_TDU_*` settings are [listed separately](docs/games/tdu/running.md#settings).
Commandos-specific `QUETZAL_COMMANDOS_*` settings, including its default
`adaptive` mode and UDP relay pool, are in the [Commandos guide](docs/games/commandos/running.md#settings).
Snowblind-specific `QUETZAL_SNOWBLIND_*` settings, UDP 13015/13016 and the
separate adaptive relay pool are in the
[Snowblind run guide](docs/games/snowblind/running.md).
Each adapter reads its settings only when enabled. `/healthz` reports all enabled tenants.
HTTP handlers are scoped under `/games/{key}/`; 25 to Life also keeps its original
`/TTL/` patch routes and `/connectivity` diagnostics. Snowblind exposes
`/psb1a/scea.txt` for the original patch HTTP path.

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
For multi-game captures, preserve all tenant subdirectories when upgrading an
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
recorders write to `/data/captures/{game}/` for each enabled adapter; a single
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
| `internal/protocol/quazal/peerrelay` | Shared native relay, endpoint proofs and pair lifecycle |
| `internal/quazalcrypto` | Retail key derivation and ticket containers |
| `internal/games/ttl25` | 25 to Life codecs, matchmaking, friends, clans, stats and native relay |
| `internal/games/tdu` | TDU adapter, native transport, sessions/presence, peer admission and title-data services |
| `internal/games/wcc` | World Championship Cards matchmaking, migration and health checks |
| `internal/games/snowblind` | Snowblind matchmaking, friends, clans, report receipts, leaderboards and native relay policy |
| `internal/games/commandos` | Commandos account policy, gathering codec, discovery and peer/relay admission |
| `internal/games/rsvpsp` | Rainbow Six Vegas (PSP) accounts, profile records, hosted matches, friends and statistics |
| `internal/state` | Private keys and exclusive tenant state ownership |
| `internal/capture` | Optional bounded private recording |
| `tools/`, `ghidra/scripts/` | Local disc analysis helpers; not used by the server |

The layout follows [Angelware](https://github.com/princebyyourlogic/angelware).
Game-specific behavior stays in its adapter; the titles share verified
PRUDP, Quazal authentication and account primitives with explicit title policy. Go tests use synthetic accounts and isolated
fixtures. See [architecture](docs/architecture.md) and
[research instructions](docs/research.md).

### Research acknowledgments

[michal-kapala/25tl-rdv](https://github.com/michal-kapala/25tl-rdv) supplied the
research lead to the original 25 to Life statistics FAQ and useful protocol
documentation for rankings and localized messages. Quetzal implements those
published rules independently in Go; the reference project's license includes
Commons Clause terms in addition to MIT. See [rankings and MOTD](docs/games/25tolife/rankings.md)
for source attribution, rating/icon rules and announcement configuration.

### Game files

This repository contains no disc images, game executables, decompiler exports,
packet captures or live account state. Research tools read your own disc into
ignored local directories. Keep those files out of contributions.
