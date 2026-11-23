# Running the Commandos Strike Force adapter

The US PS2 adapter (`SLUS-211.03`) is experimental. On 2026-11-23, two stock
PCSX2 clients on separate LAN PCs completed account creation/login, hosting,
browsing, match entry, leave and Quick Match re-entry. Mode/map changes and
player-count changes were also observed. Full-round progression, physical
consoles and Internet/NAT combinations remain unverified. Initial Ghidra findings
and method layouts are in [online-map.md](online-map.md).

## Enable alongside the other games

For all four adapters in Compose:

```dotenv
QUETZAL_GAMES=25tolife,tdu,wcc,commandos
QUETZAL_PUBLIC_IP=203.0.113.10
QUETZAL_BIND=0.0.0.0
QUETZAL_RELAY_FIRST_PORT=30400
QUETZAL_RELAY_LAST_PORT=30527
QUETZAL_COMMANDOS_PAYLOAD_MODE=plain
QUETZAL_COMMANDOS_RELAY_MODE=adaptive
QUETZAL_COMMANDOS_RELAY_FIRST_PORT=30600
QUETZAL_COMMANDOS_RELAY_LAST_PORT=30727
```

Replace the example public address with the reachable server IPv4. Then run
`docker compose up -d --build --wait`. The relay-pool change avoids the existing
TTL/TDU/WCC overlap; Commandos uses separate backend and relay ports.
Use `QUETZAL_GAMES=commandos` for Commandos alone. Default installations still
enable only 25 to Life.

Accounts, private principal key and process lock live in
`data/state/tenants/commandos/` (Compose). Existing games' files stay in
their current locations. Rooms and peer registrations are transient; restart
or host departure removes them. Reconnect the retail game from a fresh online
session after restarting the backend.

`GET /healthz` lists the enabled tenants and bound sockets.
`GET /games/commandos/connectivity` reports room count, payload mode and relay
counters. `retail_validated` remains false while full gameplay and broader
network coverage are open. With Commandos alone, `/connectivity` also works.
Health means the listeners are running, not that a retail login succeeded.

## Network setup

Point this exact US PS2 hostname at your reachable server IPv4:

```text
cxliveSCEA.quazal.net
```

Set the console's network profile or PCSX2 DNS to your resolver. The
[revive-dns example](../../../configs/commandos.dns.yaml) uses a documentation
address; replace it with your server's address. DNAS is a separate service and
must remain separately configured.

Allow or forward UDP **30060** for authentication, **30061** for secure services,
and **30600–30727** for the default adaptive relay pool. The secure endpoint is
advertised explicitly in the login reply. A DNS redirect does not forward UDP
ports; configure those on the server's firewall/router as needed.

## Settings

These are read only when Commandos is enabled. Bind/public address inherit the
global settings unless overridden.

| Variable | Default | Meaning |
| --- | --- | --- |
| `QUETZAL_COMMANDOS_BIND` | global bind | Local IPv4 listener address |
| `QUETZAL_COMMANDOS_PUBLIC_IP` | global public IP | Advertised server IPv4 |
| `QUETZAL_COMMANDOS_AUTH_PORT` | `30060` | Authentication UDP port |
| `QUETZAL_COMMANDOS_SECURE_PORT` | `30061` | Secure UDP port |
| `QUETZAL_COMMANDOS_PAYLOAD_MODE` | `plain` | Explicit `raw`, `plain`, or `rc4` mode |
| `QUETZAL_COMMANDOS_RELAY_MODE` | `adaptive` | Direct first with relay fallback; `direct` disables relaying, `relay` forces it for testing |
| `QUETZAL_COMMANDOS_RELAY_FIRST_PORT` | `30600` | First server UDP port reserved for relay pairs |
| `QUETZAL_COMMANDOS_RELAY_LAST_PORT` | `30727` | Last server UDP relay port; two ports per pair |

`plain` uses a raw/LZO selector. The stock US account-creation attempt captured
on 2026-11-23 sent selector **0**, followed by a valid protocol **10/1** RMC
request for the shipped signup principal. The earlier `raw` default rejected
this first request and stalled before account creation. `plain` is now the
default. Subsequent retail testing completed secure startup, account creation
and named-account login on both LAN PCs.

`raw` has no transform or compression selector; `rc4` adds the shared `CD&ML`
transport cipher around the selector wrapper. Those overrides remain research
options, not proven stock modes. There is no automatic protocol guessing. Each
mode still enforces the same service ticket and challenge proof.

For compatibility testing, enable `QUETZAL_LOG_LEVEL=debug` and optionally
set `QUETZAL_CAPTURE_DIR=/data/captures`. Captures are private; multi-tenant
captures go into a subdirectory per game. Leave recording disabled normally.

## Implemented surface and limits

| Area | Implemented |
| --- | --- |
| Authentication | Login 10/1, service ticket 10/3, secure CONNECT and registration 11/1 |
| Signup | Title bootstrap principal, 25/1 with parameter 0; default guest policy remains unchanged for TTL/TDU |
| Password change | Opt-in 25/4 String initial hash -> bool; persists before success and revokes previous tickets/proofs |
| Lobby write | 21/1 register, 21/4 update, 21/11 join, 21/12 leave, 21/26 publish URL, owner-only 21/27 URL update, 21/30 state |
| Discovery | 21/19 bounded retail prefix patterns, initial offset `0xffffffff` mapped to first page |
| Participants | 21/13 IDs, 21/16 URLs, 50/2 live-member IDs |
| NAT | Shared authenticated 3/1 requests and server-initiated 3/2 calls to live published hosts or same-room peers |

The concrete gathering codec preserves all title fields. The host's two reported
count fields are retained; the base maximum-participants field is not replaced
with eight. Backend membership is capped at eight as a replacement-server
policy based on the retail configuration. Empty URL publication uses the
registered connection's observed endpoint. Explicit URLs must claim the same
PID and observed IP; supplied URLs never become arbitrary server probe targets.
Host and participant URLs also include the registered RVCID needed by native
browser probes. URL publication cannot claim another registration's CID/RVCID.

The stock 2026-11-23 host request exposed a missing inner Data length in the
initial codec. Both outer and inner lengths are now validated and emitted;
the observed initial request used 600 outer bytes and 596 field-body bytes.

Host authority belongs to the exact registered connection generation. The first
adapter deletes a room on host leave/disconnect. Host election/migration,
SecureConnection 11/2, backend peer-protocol 22, notifications and voice
have not been implemented/validated end to end. Adaptive relay is implemented
and has a successful blocked-direct LAN join test. Direct peer match entry now
works on the tested LAN; wider gameplay coverage remains open. The original private/password
admission semantics and detailed signup/recovery result-code behavior also need
retail checks. Unknown RPCs fail explicitly; no friends, clans or persistent
ranking/statistics backend is advertised by this adapter.

## Validation

`scripts/go test ./...` covers title isolation, signup/password policy, native
gathering fixtures, malformed records, host authority, search ranges, cleanup,
probe admission and synthetic two-account login/host/browse/join over real UDP.
Separate startup tests exercise each configurable payload codec; they do not
establish which one the stock client chooses. Native fixture reproduction is
documented in the [reuse plan](reuse-plan.md#reproducing-the-research).

### Retail observations, 2026-11-23

| Flow | Evidence |
| --- | --- |
| Account creation and named login | Successful stock UI flow on both LAN PCs; 10/1, 10/3, 11/1 and 25/1 replies |
| Hosting and metadata | 21/1, 21/11, 21/26, 21/30 succeeded; 21/4 accepted mode 0 -> 1 and changed map details |
| Browser visibility and joining | Tester confirmed match entry after RVCID correction; 21/19, 21/13, 21/16 and 3/1 requests plus host acknowledgement |
| Leave and Quick Match re-entry | Tester confirmed both; capture shows 21/12 followed by search and 21/11 participation |
| Player counts | Tester observed updates in game; backend membership follows accepted join/leave calls |
| Extra backend services | No stats uploads, ranking/friends/clan calls or backend error replies in the observed successful session |

The capture inventory contains only protocols 3, 10, 11 and 21 for that session.
This is observed coverage, not proof that every linked SDK method is unnecessary.
Next checks: finish a complete round and advance maps, host departure with a
connected guest, private/password rooms, password change, and clients behind
different Internet NATs. Host migration remains unimplemented.

## Adaptive relay

Commandos and 25 to Life share `internal/protocol/quazal/peerrelay`; account and
room admission stay in each adapter. Commandos normally returns direct then
virtual URLs in 21/16, keeping each player's PID, CID and RVCID. The SDK chooses
a fallback after the direct attempt. If a player's direct IP equals the relay's
advertised IP, the adapter returns only the virtual candidate for that player
to avoid the browser's same-IP probe merging. Those connections use the relay
immediately; other addresses retain direct-first adaptive behavior.

The browser probes candidates before permitting a join, so Commandos enables
NAT replies and authenticated counterpart instructions for inactive candidates.
Probes can prove endpoint-dependent mappings, but cannot activate gameplay
forwarding. A native stream-1 SYN selecting the virtual endpoint activates the
pair; both endpoints must pass separate nonce challenges before any opaque
gameplay datagram is forwarded. The relay does not decrypt LZSS/RC4 payloads.

Permit/forward UDP **30600–30727 on the server** for the default pool. Clients
establish mappings outbound. Keep the pool separate from TTL, TDU and other
services; startup rejects overlapping tenant reservations. Candidate expiry,
bounded buffers/rates, exact connection-generation ownership, cleanup and a
short proved-route leave drain use the shared engine.

`/games/commandos/connectivity` includes `relay` mode, candidate, active,
proved-mapping and traffic counters. A candidate or successful probe alone does
not establish gameplay forwarding. Check active pairs, two proved mappings per
pair and increasing forwarded counters during the test.

`relay` mode tests forwarding independently of fallback selection; restore
`adaptive` for normal use. Native capture confirmed stream-1 framing and checksum
seed 0; Ghidra `0x8daf10`/`0x8db0b0` confirms the shared 13-byte NAT layout.
### Successful adaptive fallback, 2026-11-23

With the second PC (`192.168.2.46`) hosting and the backend PC
(`192.168.2.141`) joining, a temporary pre-connection-tracking firewall rule
blocked direct UDP 9103 from the second PC to the backend PC. The rule was
reported active during the successful join; it was then removed.

The stock client first attempted direct connection, then selected the relay
with a native alternate SYN at 23:28:45 UTC, roughly 20 seconds after its
participation request. The caller's mapping had already passed its browser
probe; the host's second mapping passed at 23:28:46 UTC. The tester confirmed
joining. Diagnostics recorded one active pair, two proved mappings and growing
forwarded traffic (491 packets / 15,142 bytes at one observation). This establishes
retail relay fallback and session joining for the tested LAN topology, not just
candidate publication or synthetic forwarding.

The server remains in adaptive mode. Internet NAT combinations, relay gameplay
over a full round and host departure require further coverage.
Forced-relay mode remains available as a separate diagnostic option.

### Same-IP browser compatibility

The first blocked-direct retail attempt hosted on the backend PC. Both the
direct host and virtual relay URL therefore had address `192.168.2.141` and
the same RVCID, with different ports. The client requested NAT probing only
for the direct URL and removed the room when that path was blocked. No relay
mapping was proved or activated. `0x8db6e0` matches probe records on address and
RVCID, without the port. Two same-IP URLs are not independent browser probe
targets in this SDK. Do not count that attempt as a successful fallback test.

The adapter now handles this by advertising only the virtual endpoint when a
remote player's direct and relay IPs match. It preserves the registered
PID/CID/RVCID and the existing native mapping proofs; lookup or browser probing
still cannot activate gameplay without a native SYN. This applies to either
host or guest candidates and leaves the caller's own registration unchanged.
An explicit `direct` configuration still disables relaying. If relay allocation
fails in adaptive mode, the direct endpoint remains available.

Backend and relay services can share one IP normally. This workaround is needed
only when a player's advertised gameplay endpoint also shares that IP. Synthetic
tests cover publication in both directions, counterpart introductions and the
real-UDP lobby response.

The stock same-IP join succeeded on 2026-11-23 after deploying the workaround:
the host was `192.168.2.141`, alongside the backend and relay, and the guest was
`192.168.2.46`. Both mappings passed their native challenges at 23:41:54–55 UTC;
the guest selected the virtual endpoint with a native SYN at 23:42:04 UTC. The
tester confirmed joining. Logs showed traffic in both directions (152 packets
each at 23:42:25 UTC), and diagnostics subsequently reported one active pair,
two proved mappings and 309 forwarded packets / 12,001 bytes. This validates
same-IP room discovery and relay joining on the tested LAN; a full gameplay
round and Internet NAT combinations remain unverified.
