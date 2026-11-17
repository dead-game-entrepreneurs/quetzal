# Quetzal architecture

Quetzal replaces Quazal online services for unmodified retail clients. The
first adapter targets 25 to Life on PS2. Real-console compatibility is a design
requirement; client patches and emulator-only changes are outside the project.
The layout follows Angelware while keeping Quazal framing, service numbers,
cryptography and traversal independent from DemonWare.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/quetzal` | Process startup and explicit game registration |
| `internal/app` | Configuration, listeners, state ownership and shutdown |
| `internal/game` | Game definitions and auxiliary service contracts |
| `internal/protocol/prudp` | Framing, checksums, connection cookies, reliability, retries, reassembly and raw/LZO payloads |
| `internal/protocol/quazal` | RMC, authentication, service tickets, secure registration and account persistence |
| `internal/quazalcrypto` | Retail key derivation and authenticated ticket containers |
| `internal/games/ttl25` | Eidos records, matchmaking, social operations, statistics and native relay policy |
| `internal/capture` | Optional bounded packet recording |

Shared packages do not import concrete games. Game-specific codecs and policies
stay with their adapter; additional shared services should be extracted when a
second title demonstrates reuse. Quetzal currently enables one game, rather than
claiming drop-in support for all Quazal titles.

## Runtime ownership

The entry point acquires an exclusive state lock and loads or creates the
private service principal key before constructing the game. The app binds all
listeners before announcing readiness. Startup failures close previously
opened sockets; cancellation closes transports, HTTP and auxiliary services.

The PRUDP transport supplies endpoint-bound SYN cookies, CONNECT admission,
reliable acknowledgements/retries and bounded reassembly. A fresh challenge and
valid application proof are required to replace an existing connection at a
reused UDP endpoint. Failed proofs leave the active connection intact.

25 to Life uses the plain raw/LZO transport wrapper with backend checksum seed
0xff. Native peer traffic uses a separate checksum seed, sockets and identity.
Ticket and nonce containers retain the SDK's authenticated RC4/HMAC format.
A transport connection alone grants no account authority.

Game callbacks receive the authenticated peer generation and PID. Account and
profile writes bind to that identity. Rooms and participation are ephemeral and
owned by the exact connection, so another connection using the same PID cannot
remove its room. Searches evaluate typed fields; incoming SQL-like text is never
executed against a database. Unknown methods fail explicitly.

## Persistence

The versioned JSON stores live under `data/state/` by default. Account names,
password-derived initial hashes, profiles and contact fields are private.
`community.json` contains friendships, blocks, clan membership/roles, notices,
preferences, statistics and report receipts. `ranking.json` stores the shared
Nonladder ranking definition. The random `principal.key` persists across
restarts. Private files use mode 0600 and the state directory uses mode 0700.

Mutations write a temporary file, sync and atomically rename it before returning
success. The process-wide state lock prevents concurrent writers. Back up the
entire state directory while the server is stopped. No runtime data belongs in
Git. See [deployment](deployment.md).

## Social and statistics services

The adapter implements the 39 documented title operations in protocol 101,
plus the supported SDK methods listed in the
[coverage inventory](games/25tolife/protocol-inventory.md). Presence and active
room references come from live connections; relationships persist separately.

Statistics combine 15 host-authoritative gathering values and 87 personal
values per player. Receipts detect replay/conflicting reports. Finished rounds
retain bounded, short-lived reporting authority so the host can leave before
the joiner's report arrives. That archive grants no browsing or joining access.
Original scoring formulas remain under investigation.

## Native connectivity

Authenticated NAT traversal uses protocol 3/1 and server-initiated 3/2 calls
on each player's existing reliable connection. The transport drains bounded
asynchronous instructions on its normal ticks. Only live, accessible published
room endpoints are admitted; private rooms require membership or invitation.

The native relay is an app-owned auxiliary service. It allocates two virtual
UDP sockets per admitted peer pair. In adaptive mode, a native SYN selecting
the alternate endpoint activates the route. Forced mode also replaces the early
host/participant URL replies, preserving each peer's CID/RVCID and native scheme.
Separate random challenge echoes prove endpoint-dependent NAT mappings before
opaque datagrams can pass.

Room exit removes listings and pending introductions immediately. Active,
fully proved routes have a fixed five-second drain for native leave DATA and
ACKs; no new handshake or mapping is admitted. Shutdown, unproved candidates
and bounded idle expiry close routes immediately. See
[connectivity](games/25tolife/connectivity.md) for limits and remaining tests.

## Diagnostics and validation

JSON logs contain operational metadata. Packet-by-packet metadata uses debug
logging; raw backend recording is disabled unless a capture directory is set.
`/healthz` and `/connectivity` expose status and relay counters without account
fields. Enabled captures are bounded and private, and capture write failures
stop the service rather than silently losing a requested recording.

Tests check protocol properties, authority, persistence and independently
recovered fixtures. The game notes distinguish implemented, statically inferred
and confirmed-in-game behavior. Passing codec tests alone does not establish
retail support.
