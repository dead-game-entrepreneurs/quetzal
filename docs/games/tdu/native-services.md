# Native directory and read-only resources

Research provenance: these findings were produced in the sibling `tdu-server`
workspace. Runtime package paths below refer to Quetzal; `tools/`, private
`work/` evidence and Ghidra projects refer to that original research workspace.
Keep those private files there; Quetzal has no runtime dependency on them.
See [running.md](running.md) for current Quetzal operation.

This describes the US SLUS-214.90 build identified in [analysis.md](analysis.md).
The action encoders and decoders are established from the executable. The Go
service tests compare synthetic packets with native-executed resource sender and
reply-consumer fixtures. No retail client online-entry session has validated
these handlers yet.

## Runtime boundary

`internal/games/tdu/ednet` plugs into `nativeproxy.Options.Services` by logical destination
port. It opens no sockets, forwards no arbitrary destinations and establishes no
account identity. The proxy must finish its transport handshake and activation
before invoking these handlers. Only the proxy's configured public IPv4 and
explicitly registered local logical ports are accepted.

`ednet.New` accepts a public IPv4, an explicit directory logical port, an optional
resource logical port, immutable private resource bytes, and optional additional
implemented action-to-port registrations. `Handlers` supplies the local routing
map. Additional registrations are an integration responsibility: do not advertise
a port without actually installing its handler. The adapter configures logical directory/resource ports 9000/9001 by default.
Port 9000 is recovered from the stock descriptor; 9001 is server policy.
The stock directory IPv4 handoff is now proven: the master reply's second
IPv4 passes through native connection `+0x20`, then online-entry routine
`0x377b80` writes it into directory address `0x5eb5cc`. See
[startup-handoff.md](startup-handoff.md) for instruction addresses, byte order
and native execution checks. No zero-address alias or control-30 payload is
needed.

The label “Checking server configuration” waits for the directory batch:
UI routine `0x377a00` polls object `0x5f092c`, equal to manager
`0x5eaebc + 0x5a70`, the batch-result object used by `0x460d60`/`0x4610e0`.
Manager constructor `0x462b80` and reset `0x461fb0` initialize the directory
as `0.0.0.0:9000`; refresh skips its IPv4 at manager `+0x710` while zero.
The online-entry write supplies the missing address before that refresh.

`0x462060` can separately obtain a debug ORB override from `GServiceOnline`
getter `0x3a0c90` (base `0x5eac80`, string `+0x4a`, port `+0x72`). Its
constructor clears the override. Commands 32/33 use callbacks `+0x50`/`+0x54`,
separate from those installed by `0x377b80`; they are not an invented directory
advertisement. The ordinary recovered activation path is 29 → 30 → timed
state 6, followed by the online-entry descriptor write.

## Action envelope

Every integer below is big endian. This layer is inside the ten-byte proxy
forwarded-data envelope; it is not a bare RPC signature immediately after the
proxy header.

| Kind | ID | Body after the kind |
| --- | --- | --- |
| Reliable call | `0x509a` | transaction u32, timeout milliseconds u32, object |
| Unreliable call | `0xd572` | object |
| Reply | `0x984e` | matching transaction u32, object |
| Reply acknowledgement | `0x08e7` | matching transaction u32 |

An object is a u16 byte length followed by that many bytes, including the nested
action's own u16 signature. A zero object length denotes no object. The package
accepts an empty object only in a reply, rejects unknown envelope kinds and
requires the length to consume exactly the packet. The maximum frame is 1378
bytes, leaving the native proxy's ten-byte header inside its 1388-byte plaintext
limit.

Evidence: constructor `0x12bfa0` hashes strings `0x50e98d`, `0x50e9d8`,
`0x50ea2f`, `0x50eaec` into globals `0x5760b8`, `0x5760c0`, `0x5760c8`,
`0x5760d8`. Reliable builder `0x12b170` writes transaction, timeout and action;
receiver `0x12b520` reads them, matches replies against the pending transaction,
and sends the acknowledgement. Object writer `0x139f20` emits length and complete
nested bytes; reader `0x138900` performs the reverse. Primitive writers
`0x13a530`, `0x13a2b0`, `0x13a0f0` implement signature, u32 and counted string.
The i32 writer is `0x13a1f0`; both integer writers preserve all 32 bits.

The Go service does not send unsolicited replies for unreliable calls or calls
with transaction zero, matching `0x12b520`'s reply gate. ACKs cause no response.
Unknown operations return a descriptive error to the proxy's logging path.
Immutable directory/resource operations and stable file IDs make retries
idempotent; this service does not claim the more general mutation/deduplication
contract of the original backend.

## Directory

Request `0xc7bc` has exactly five u16 action IDs. Reply `0xee87` contains one
8-byte row per requested slot: original action u16, IPv4 u32, logical port u16.
There is no row-count field. Unsupported actions retain their slot and return
zero address and port. Supported actions return only explicitly registered local
services. The adapter now also registers the implemented SessionMaster and
presence handlers; unsupported actions still receive zero endpoints. See
[sessionmaster.md](sessionmaster.md) and [presence.md](presence.md).

`0x460b90` proves the five-slot request; `0x460960` reads the reply into a capacity
of sixteen slots, discards each returned action ID, and therefore requires the
original order. `0x460d60` installs the first five endpoints. Refresh builder
`0x4610e0` requests log, get-time, density, user-update and resource-open service
IDs. Its fifth slot is global `0x59bfb8`, initialized by `0x15c2f0` from the open
signature at `0x5120e1`: **resource discovery ID `0x3be2`**.

`0xee87` also identifies an unrelated peer physics action. It is valid only in
the directory transaction/service context here.

## Resource wire schema

| Operation | ID | Fields after signature |
| --- | --- | --- |
| Open | `0x3be2` | facility u8, user u32, filename string, upload u8, upload size u32 |
| Open reply | `0xb715` | error u8, file ID u32, total size u32, max retries u32, timeout u32 |
| Get | `0xd976` | file ID u32, byte offset u32 |
| Get reply | `0x49a3` | error u8, file ID u32, byte offset u32, wait u16, data buffer |

Strings and buffers both have a u16 byte length, followed by bytes; strings have
no terminator. Constructor `0x15c2f0` establishes these four hashes from the exact
signatures at `0x5120e1`, `0x512155`, `0x5121b7`, `0x5121ec`. Open builder
`0x15bfd0` and response path `0x15b5d0` establish the fields. Buffer primitives
`0x13a000`/`0x138b80` establish the counted bytes.

The implemented path accepts only facility 2, user 0, download mode and upload
size zero, matching the configuration request callers `0x461ed0`, `0x4614b0`,
`0x461c80`. Each caller invokes virtual `+0x18(this, 1, 2)`, resolving to
setter `0x15c190`, which writes **2** into job field `+8`; user setter
`0x1569d0` writes zero at `+0x218`. Open builder `0x15bfd0` serializes those
values. Constructor `0x15c2f0` initially writes facility 1, but the configuration
callers override it before sending. The initial server incorrectly checked the
constructor default and rejected all three retail configuration downloads.
The native fixture now executes the actual option/user setters before building
Open requests for each configuration filename. Facility 1, other facilities,
nonzero users, uploads and nonzero upload sizes remain unsupported.
No upload or account file operations are enabled. Successful opens
return the real supplied length, a stable file ID, retry policy 10 and timeout
1000 milliseconds. Gets return up to 1024 bytes at the requested offset, wait
zero, with the actual counted bytes. This chunk size and retry policy are server
policy within the recovered wire fields, not recovered original backend values.
An unknown filename/file ID or out-of-range offset returns error 1; the client
ends the job on error 1. Native error 2 instead schedules a new Open after
the returned u16 wait; this optional retry path is checked by the native fixture.
The original symbolic name of error 1 is not established.

## Configuration resources and server-owned policies

Original retail XML assets are **not required for the recovered default-network,
no-pending-awards policy**. `ednet.DefaultNetworkConfig()` generates the exact
39 numeric constructor defaults using the recovered schema;
`ednet.NoAwardsConfig()` generates an empty `PRIZES` collection. These are new
server-authored documents with explicit semantics, not copied game assets or
claims that missing gameplay services are available. Operators can supply private
XML files to override these policies as their own reconstruction develops.

`netconfig.xml` and `prize.xml` must each contain 1..4096 bytes. `p2pkey` must be
exactly sixteen persistent server-chosen bytes; it is separate from proxy and
Quazal keys and must be shared by clients exchanging native peer packets.

### Network configuration schema

Parser `0x409300` initializes the XML parser from the downloaded NUL-terminated
bytes, selects **case-sensitive root `NETCONFIG`**, enters it, and iterates the
registered `CConfigVariable` list. For each variable it finds a matching direct
child and reads its **`value` attribute**. An absent child or empty/missing value
leaves that variable's existing values unchanged. Unknown children cause no
setting update. There is no mandatory numeric child or address/port field.
Root and attribute strings are `0x5357e0` / `0x5357ea`; attribute lookup is
`0x2b0da0` → `0x3c2f60` → `0x3c3060`.

The XML traversal is forward through siblings (`0x3c41b0` / `0x3c3500`), so
**preserve constructor registration order when emitting multiple settings**.
The generator below follows that order. Native XML parser `0x3c3880` explicitly
supports both self-closing elements and paired start/end tags.

Constructor `0x4095c0` establishes the following values at client object
`0x65d5f0`, using 0x34-byte setting records. Each setting caches an integer
(`+0x20`), float (`+0x24`), u16 (`+0x28`) and byte (`+0x2a`) conversion from the
same numeric text; there is no single XML-level integer/float declaration.
The consuming code chooses its representation. Units are not inferred solely
from a field name.

| Setting, in registration order | Default numeric text |
| --- | --- |
| `QOS_BANDWIDTH_RETIME_MINIMAL` | `1300` |
| `QOS_BANDWIDTH_GAMEOBJECTS` | `8000` |
| `CORRECTION_ACTIVE` | `3` |
| `CORRECTION_DT` | `0.220` |
| `MAX_CORRECTION_DT` | `0.5` |
| `RETIME_DT` | `50` |
| `CORRECTION_RANGE` | `0.5` |
| `MINIMALUPDATE_DT` | `0.25` |
| `FORCEUPDATE_DT` | `0.33` |
| `ORB_REFRESH_DT` | `10000` |
| `ORB_LOST_RPC_DT` | `60000` |
| `USERMAP_REFRESH_DT` | `10000` |
| `DENSITY_MAP_DT` | `5` |
| `SESSION_DENSITY_DT` | `10` |
| `PLAYERS_NEAR_DT` | `5` |
| `P2PFR_TIMEOUT_START_SCAN` | `10000` |
| `P2PFR_TIMEOUT_ELECT` | `15000` |
| `P2PFR_TIMEOUT_INITIAL_CORJ` | `5000` |
| `P2PFR_TIMEOUT_ALONE` | `15000` |
| `P2PFR_ACTIVE` | `1` |
| `P2PFR_STRENGTH_RADIUS` | `1500` |
| `PROXY_HEARTBEAT` | `8.0` |
| `LOG_ROADS` | `1` |
| `INSTANT_CHALLENGES` | `1` |
| `ONLINE_FREERIDE` | `1` |
| `GAMESTAT_REPORT_INCREMENT` | `30.0` |
| `MAX_SESSIONS_QUERY` | `10` |
| `LOG_GAMEMODE_CHANGES` | `0` |
| `LOG_RACE_RESULTS` | `1` |
| `P2PGAMESERVICE_TIMEOUT` | `11000` |
| `P2PGAMESERVICE_INITIALTIMEOUT` | `20000` |
| `P2PGAMESERVICE_KEEPALIVE_DT` | `3000` |
| `P2PGAMESERVICE_SESSION_KEEPALIVE_DT` | `3000` |
| `P2PGAMESERVICE_CONSISTENCY_DT` | `22000` |
| `LAIR_HEALTH_CHECK_FREQ` | `0.0` |
| `EDNETACTIONCACHE_TIMEOUT` | `5000` |
| `EDNETACTION_DEFAULT_TIMEOUT` | `0.250` |
| `EDNETACTION_DEFAULT_MAX_RETRY` | `20` |
| `MAX_MASTER_REJECTIONS` | `6` |

Successful nonempty netconfig download callback `0x461e40` calls this parser,
sets completion flag `0xa9bf60`, and installs the action-cache timeout,
default action timeout and retry count from the resulting settings. Generating
the explicit defaults preserves the client's compiled policy on a fresh client;
it neither forces login success nor suppresses requests to unimplemented services.

### Prize schema and no-awards policy

Parser `0x461720` selects **root `PRIZES`**, enters it, and iterates direct
**`PRIZE` children**. Each entry has nonempty **`id`, `type`, `value` attributes**
(strings at `0x539fa2`..`0x539fc1`). `id` and `value` are converted to integers.
An entry is actionable only when its ID exceeds the player's saved prize
high-water mark. `type` is compared against `car` (first three bytes) or `money`
(first five bytes); unknown types are ignored. The parser does not require any
entry to exist. Reconstructing a populated grant service needs the inventory,
car validation and high-water-mark rules, so the runtime emits no grant entries.

For car awards, the parser looks up an inventory destination and returns without
a grant if that check fails; otherwise it advances the selected high-water mark
and invokes the car-award path `0x3b3310`. Money entries advance the corresponding
mark and invoke `0x437e20`. The global configuration download invokes the parser
with selector 1, using high-water field `+0xb8`; the other prize-download path
uses `+0xbc`. These are player-state mutations, not configuration booleans.

A generated `<PRIZES></PRIZES>` means **the server has no pending awards**:
no PRIZE iteration occurs, both high-water marks remain unchanged, and no money
or car is granted. Completion callback `0x4615d0` invokes the parser on a successful
nonempty download and then sets `0xa9bf70`. Failure callback `0x461580` also sets
that flag; this does not make an invented grant or malformed XML valid.

The generator schema and semantics are established by static client code.
Tests verify correct roots/attributes, all 39 numeric defaults, buffer limits,
deterministic independent output and zero mutating prize entries. Retail-client
acceptance of generated files remains unverified.

Tests cover exact synthetic framing bytes, truncations and trailing data,
directory order and unavailable endpoints, open/get reconstruction across three
chunks, repeated transactions, missing resources, upload rejection, input
ownership and configuration bounds. These are codec/service checks; actual retail
online entry remains a separate validation milestone.

Runtime integration tests additionally perform a complete real UDP native
handshake and activation through the Quetzal TDU adapter, then fetch directory
entries and all three configured synthetic resources through encrypted packets. It checks
that downloaded `p2pkey` exactly matches persisted `state/p2p.key`, repeats a
transaction, and verifies missing-file failure. Its minimal synthetic XML
fixtures test byte transport only, not retail configuration acceptance.

### Native-executed resource fixtures

[`ednet/testdata/native-resources.json`](../../../internal/games/tdu/ednet/testdata/native-resources.json)
records actual Open/download `0x15bfd0`, Open/upload `0x15be00` and Get
`0x15bb20` request output. The full reply consumer `0x15b5d0` reads synthetic
Open/Get replies using actual stream primitives. It confirms full-width file
IDs/offsets, counted filenames without a NUL, u16 waits and counted binary
buffers, including embedded zeroes. Recorded outcomes cover Open success and
failure, partial/final Get, error 1 and error 2's delayed reopen. The Go test
compares emitted service responses with those native-consumed bytes.

The MIPS64 verifier supplies lifecycle, clock, registration, diagnostics,
memory/string and reliable-submit boundaries; it does not replace field
writers/readers. No original file asset is included. See the
[fixture README](../../../internal/games/tdu/ednet/testdata/README.md) for
reproduction and limitations. This establishes codec compatibility within the
executed routines, not completion of retail online startup.

## Shipped package configuration check

A targeted, read-only inspection of the user's US ISO recovered its package
index without extracting the multi-gigabyte archives. The files use `MHPF`
version 1 headers and little-endian fields. The observed index layout is:

| Header offset | Observed meaning |
| --- | --- |
| `0x08` | Package byte length |
| `0x0c` | Entry count |
| `0x14` | Main index offset |
| `0x18` | Main index byte length: entry count × 12 |
| `0x24` | Filename-offset table offset |
| `0x28` | Filename-offset table byte length: entry count × 4 |
| `0x2c` | Filename blob offset |
| `0x30` | Filename blob byte length |

A main index row is `(identifier u32, package-relative byte offset u32, size u32)`;
the identifier algorithm is not established here. Its corresponding filename
is a NUL-terminated string at the matching u32 offset in the filename blob.
Every row's payload bound was checked against the package length. `TDUPS2A.PCK`
contains 666 entries, `TDUPS2B.PCK` 4614 and `TDUIOP.PCK` 342. No large package
payload was copied into the repository.

The direct script callback `0x38db80`, registered through pointer `0x5029e0`,
reaches `GServiceOnline.SetORB` (`0x3a0cb0`) for the `ORB` command. Its existence
does not establish a shipped active override. In `TDUPS2A.PCK`:

| Entry | Package offset | Bytes | SHA-256 |
| --- | --- | --- | --- |
| `gameps2.ini` | `0x3d3ff000` | 17181 | `80a243896f2babfe9b5db3929845f60d4eb69ad9acc4232d95a0fe27f32f417d` |
| `skinny/gameps2.ini` | `0x3cae3800` | 16225 | `60c3c551bd3666b89d085d71a859708afc471b46b51d46d86b686d5d4cf5ead8` |

Both files' `[Online]` sections leave their development `ORB` examples commented
out. Their active Server, Lobbying and GPSTracker choices are `none`. Thus these
INI files do not seed a nonzero stock directory IPv4. No explicit ORB/SetORB
endpoint strings were found in the targeted frontend general, boot, login and
US/EU title banks or the startup `boot.ige`/mission metadata. This is a bounded
asset inspection, not proof that no other binary/script path can supply it.
Private indexes and the two small INI files are in ignored
`work/world-sessions/assets/`; game files are not tracked or embedded in the server.


## Superseded directory investigation and retail observation

The earlier bounded scan found no writer in ORB setters, transport control 30,
INI parsing or directory completion. Its private negative evidence remains in
`work/gameplay/ps2/directory-stock-bootstrap-negative-evidence.json`; that scan
missed the positive writer in `0x377b80`. It is historical evidence, not a
current startup blocker. The master reply's second IPv4 and the write to
`0x5eb5cc` have since been native-executed; see
[startup-handoff.md](startup-handoff.md).

A fresh retail trace should still confirm `0x5eb5cc` receives the configured
directory IPv4, then capture the first `0xc7bc` request and the three resource
download callbacks. This is interoperability validation of the recovered path,
not a search for a still-unknown initializer. Session/discovery and Quazal
introductions are implemented separately; see [sessionmaster.md](sessionmaster.md),
[presence.md](presence.md), [peer-connectivity.md](peer-connectivity.md) and
[title-services.md](title-services.md). Shared cruising and races remain untested.
