<p align="center">
  <img src="docs/assets/quetzal-logo.png" alt="Quetzal" width="640">
</p>

<h3 align="center">Bringing Quazal games back online</h3>

<p align="center">
  A replacement for <b>Quazal Rendez-Vous / NetZ</b> online services,<br>
  starting with <b>25 to Life on PlayStation 2</b>, using unmodified retail clients.
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
  <a href="docs/games/25tolife/analysis.md">Protocol notes</a>
</p>

---

## Status

Supported game: **25 to Life** (US PS2, `SLUS-210.16`, v1.01).

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

Quetzal currently supports this game's backend variant. Other Quazal titles
need their own adapters and verification. See the
[protocol inventory](docs/games/25tolife/protocol-inventory.md) for implemented
operations and remaining SDK coverage.

## How it fits together

```text
 PS2 / PCSX2 ──DNS──▶ your resolver ──▶ Quetzal's server address
      │
      ├── DNAS ────────────────────▶ separately configured DNAS service
      ├── account / lobby / social ▶ Quetzal (Go, UDP)
      ├── patch check ─────────────▶ HTTP no-update response on port 80
      └── gameplay ────────────────▶ peer directly, or Quetzal's native relay
```

DNS and DNAS are configured separately. Gameplay stays in the retail client's
native protocol; Quetzal handles accounts, matchmaking, social services,
statistics and optional peer forwarding.

## DNS redirects

Point these exact names at your Quetzal server's reachable IPv4 address:

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

## Running the server

You need Docker with Compose on a host supporting Linux host networking.

```sh
git clone https://github.com/princebyyourlogic/quetzal.git
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

Allow or forward the following ports to the server:

| Port | Purpose |
| --- | --- |
| UDP 30120 | Authentication |
| UDP 30121 | Ticket-authenticated services |
| UDP 10100, 10105, 10110 | Alternate authentication endpoints |
| UDP 30200–30327 | On-demand native relay endpoints |
| TCP 80 | Retail patch check, served by your web server |
| TCP 8082 | Quetzal HTTP health and connectivity diagnostics |

Compose uses host networking to preserve observed peer addresses. Accounts,
profiles, friends, clans and statistics persist in private JSON files under
`data/state/`; transient rooms are removed on host departure or restart.
Stop with `docker compose down`. The `data/` directory stays on disk.

After restarting the backend or changing connection modes, leave online mode
completely and reconnect both players. Reboot the games without loading save
states if an old room ID remains cached.

### Settings

| Variable | Default | Purpose |
| --- | --- | --- |
| `QUETZAL_PUBLIC_IP` | bind address; set explicitly for hosting | Reachable server IPv4 |
| `QUETZAL_BIND` | `127.0.0.1` (example uses `0.0.0.0`) | Local listener address |
| `QUETZAL_HTTP_PORT` | `8082` | HTTP diagnostics and patch handler |
| `QUETZAL_UID`, `QUETZAL_GID` | `1000` | Compose process UID and GID |
| `QUETZAL_RELAY_MODE` | Compose: `adaptive`; binary: `direct` | Connection policy |
| `QUETZAL_RELAY_FIRST_PORT`, `QUETZAL_RELAY_LAST_PORT` | `30200`, `30327` | Native relay UDP pool |
| `QUETZAL_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `QUETZAL_CAPTURE_DIR` | empty (disabled) | Optional private packet capture directory |
| `QUETZAL_STATE_DIR` | binary: `data/state`; Compose: `/data/state` | Persistent state directory |

### Connection modes

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

### Accounts and privacy

Players create accounts from the game's online menu. The legacy protocol sends
a password-derived hash that serves as an authentication secret. Account files
also retain the contact and profile fields supplied by the client. Keep
`data/`, `.env` and backups private; they are ignored by Git.

Raw capture is disabled by default. For protocol research, set
`QUETZAL_CAPTURE_DIR=/data/captures` in Compose. Captures include full backend
payloads and can contain account secrets and profile information. They rotate
at 16 MiB with three backups. Routine JSON logs contain operation metadata;
packet-level address/length events require `QUETZAL_LOG_LEVEL=debug`.

See [deployment and backup notes](docs/deployment.md) before moving an existing
installation. Quetzal is an independent compatibility project, unaffiliated
with Quazal, Eidos or the game's publishers.

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
| `internal/games/ttl25` | Title codecs, matchmaking, friends, clans, stats and native relay |
| `internal/capture` | Optional bounded private recording |
| `tools/`, `ghidra/scripts/` | Local disc analysis helpers; not used by the server |

The layout follows [Angelware](https://github.com/princebyyourlogic/angelware).
Game-specific behavior stays in its adapter; generic SDK services are extracted
when reuse is demonstrated. Go tests use synthetic accounts and isolated
fixtures. See [architecture](docs/architecture.md) and
[research instructions](docs/research.md).

### Game files

This repository contains no disc images, game executables, decompiler exports,
packet captures or live account state. Research tools read your own disc into
ignored local directories. Keep those files out of contributions.
