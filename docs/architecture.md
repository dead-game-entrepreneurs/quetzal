# Quetzal architecture

Quetzal replaces Quazal online services for unmodified retail clients. The
retail-tested adapter targets 25 to Life on PS2; an experimental TDU PS2 adapter
shares the runtime. Real-console compatibility is a design requirement; client patches and emulator-only changes are outside the project.
The layout follows Angelware while keeping Quazal framing, service numbers,
cryptography and traversal independent from DemonWare.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/quetzal` | Process startup and explicit game registration |
| `internal/app` | Configuration, listeners, state ownership and shutdown |
| `internal/game` | Tenant definitions, grouped/per-socket service factories, HTTP mounts and lazy UDP reservations |
| `internal/protocol/prudp` | Framing, checksums, connection cookies, reliability, retries, reassembly and raw/LZO payloads |
| `internal/protocol/quazal` | RMC, authentication, service tickets, secure registration and account persistence |
| `internal/quazalcrypto` | Retail key derivation and authenticated ticket containers |
| `internal/games/ttl25` | Eidos records, matchmaking, social operations, statistics and native relay policy |
| `internal/games/tdu` | TDU tenant composition, native transport, sessions/presence, peer admission and title data |
| `internal/state` | Private keys, state files and exclusive tenant locks |
| `internal/capture` | Optional bounded packet recording |

Shared packages do not import concrete games. Verified transport, ticket,
registration and account primitives are reused with explicit title policy.
TDU's access key/checksum seed and `prudps` registration policy remain separate
from 25 to Life's plain-wrapper/backend policy. Native proxy and peer cryptography
live entirely within the TDU adapter. Sharing Quazal does not make the titles'
gameplay protocols interchangeable.

## Runtime ownership

`QUETZAL_GAMES` selects `25tolife`, `tdu`, or both; the default stays `25tolife`.
Unknown and duplicate keys fail before tenant state is created. Only enabled
adapters load their settings, stores and keys. Each tenant owns a separate state
lock and principal key.

`app.RunTenants` validates every static UDP listener and lazy `ReservedUDP` range
before binding. It binds all tenant sockets and shared HTTP before invoking
factories or starting services. `Definition.NewUDP` supports an independent
service per socket; `NewServices` receives the complete ordered socket set for
protocols sharing endpoint state. TDU receives auth/secure/master/proxy sockets
and creates two PRUDP services plus one native service using both app-bound
native sockets. Factories do not start background listeners.

Any startup failure closes acquired sockets and services. Cancellation or a
runtime failure stops the whole instance, joins handlers and only then releases
state locks. The app supplies each tenant's capture observer to its transports;
requested capture failures stop service rather than silently dropping evidence.

25 to Life reserves its configured native relay pool only in relay/adaptive
mode. Its historical 30200–30327 pool overlaps TDU's 30200/30201 listeners.
Combined deployments must explicitly choose another pool, such as 30400–30527;
validation rejects conflicts and performs no automatic remapping.

The PRUDP transport supplies endpoint-bound SYN cookies, CONNECT admission,
reliable acknowledgements/retries and bounded reassembly. A fresh challenge and
valid application proof are required to replace an existing connection at a
reused UDP endpoint. Failed proofs leave the active connection intact.

25 to Life uses the plain raw/LZO transport wrapper with backend checksum seed
0xff. Native peer traffic uses a separate checksum seed, sockets and identity.
Ticket and nonce containers retain the SDK's authenticated RC4/HMAC format.
A transport connection alone grants no account authority.

25 to Life account callbacks receive the authenticated peer generation and PID.
Account and profile writes bind to that identity. Rooms and participation are ephemeral and
owned by the exact connection, so another connection using the same PID cannot
remove its room. Searches evaluate typed fields; incoming SQL-like text is never
executed against a database. Unknown methods fail explicitly.

## Persistence

25 to Life keeps its existing versioned JSON files directly under
`QUETZAL_STATE_DIR` (`data/state/` by default). TDU uses
`QUETZAL_STATE_DIR/tenants/tdu/`, even when it is the only enabled game. Each
location has its own `principal.key`, accounts and lock; TDU additionally persists
`p2p.key` for its peer-wrapper resource and atomic `title-data.json` for reported
statistics and challenge files. Enabling TDU does not move or merge
25 to Life's working state.

25 to Life account names, password-derived initial hashes, profiles and contact
fields are private.
`community.json` contains friendships, blocks, clan membership/roles, notices,
preferences, statistics and report receipts. `ranking.json` stores the shared
Nonladder ranking definition. The random `principal.key` persists across
restarts. Private files use mode 0600 and the state directory uses mode 0700.

Mutations write a temporary file, sync and atomically rename it before returning
success. Each tenant state lock prevents concurrent writers to that store. Back
up the entire state directory while the server is stopped. No runtime data belongs in
Git. See [deployment](deployment.md).

## 25 to Life social and statistics services

The adapter implements the 39 documented title operations in protocol 101,
plus the supported SDK methods listed in the
[coverage inventory](games/25tolife/protocol-inventory.md). Presence and active
room references come from live connections; relationships persist separately.

Statistics combine 15 host-authoritative gathering values and 87 personal
values per player. Receipts detect replay/conflicting reports. Finished rounds
retain bounded, short-lived reporting authority so the host can leave before
the joiner's report arrives. That archive grants no browsing or joining access.
Original scoring formulas remain under investigation.

## 25 to Life native connectivity

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

## TDU native services

TDU defaults to physical UDP 30200/30201 for Quazal auth/secure services and
8124/8122 for native master/proxy. Logical ports 9000–9004 travel inside the
encrypted native proxy and create no extra sockets. Protocol 104/1 supplies the
bootstrap IPv4. The master returns both proxy and directory IPv4 addresses; the
proxy performs signed DH and encrypted transport activation. Native transport
ownership is scoped to an observed endpoint and a nonreused connection generation.
Native connection-ID and USERMAP account claims do not independently prove account
ownership.

The directory advertises implemented endpoints. Resource handlers serve network
default XML, a no-pending-awards prize policy and a persistent peer-wrapper key.
SessionMaster owns bounded, expiring session records and reservations. Presence
owns transient positions and density/nearby queries. Both remove transport-owned
state on disconnect; reliable native calls receive separate business and generic
completion datagrams where the retail dispatcher requires them.

TDU-specific secure lookup bridges verified Quazal registrations to live native
session membership, then initiates direct-peer NAT instructions using native
engine endpoints. Generic relay forwarding is not enabled. Protocols 102/103
persist authenticated challenge uploads and reported statistics in the tenant's
atomic title-data store; native claims cannot write that store. Ranking and
resource policies are documented replacement-server choices where the original
algorithm is unknown. Clients retain driving physics and peer gameplay.

Synthetic two-driver UDP tests cover these services together. They do not prove
retail online entry or a completed race. See the
[TDU evidence map](games/tdu/online-map.md),
[session contract](games/tdu/sessionmaster.md),
[peer admission](games/tdu/peer-connectivity.md) and
[title services](games/tdu/title-services.md).

## Diagnostics and validation

JSON logs contain operational metadata. Packet-by-packet metadata uses debug
logging; raw backend recording is disabled unless a capture directory is set.
`/healthz` reports all enabled tenants, modes and bound UDP endpoints without
account fields. Each handler is mounted under `/games/{key}/`; 25 to Life also
retains `/TTL/` and `/connectivity` for original retail routing and relay counters.
A single tenant keeps its legacy root HTTP mount. TDU's
`/games/tdu/connectivity` reports session/player counts and its experimental
validation status without exposing identities or positions. Health reports
listening services, not successful retail gameplay.

Multi-tenant captures use a recorder subdirectory per tenant; a single tenant
keeps the legacy capture directory itself. Enabled captures are bounded and
private, and capture write failures stop the service rather than silently losing
a requested recording.

Tests check protocol properties, authority, persistence and independently
recovered fixtures. The game notes distinguish implemented, statically inferred
and confirmed-in-game behavior. Passing codec tests alone does not establish
retail support.
