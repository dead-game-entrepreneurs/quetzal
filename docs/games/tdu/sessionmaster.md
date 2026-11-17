# TDU FreeRide SessionMaster

This implements the recovered US PS2 SLUS-214.90 FreeRide registry in
`internal/games/tdu/sessionmaster`. Wire layouts below come from the documented
[main executable](analysis.md). Retained query output is private in the sibling
`tdu-server/work/world-sessions/ps2/sessionmaster-new*.log`; no game code or
retail captures are included. Tests use synthetic inputs, including independently executed retail serializer
fixtures. Retail cruising and
races remain unverified and require more than a session listing.

## Ownership and integration

`New(Options{Port, Store, Admit})` creates a service without sockets or goroutines.
`BatchHandlers()` returns the native proxy's local batch-handler map.
`HandleFrames` returns separate edNet frames; the proxy encrypts and sends each
as its own datagram with a fresh transmit counter. `SupportedActions()` lists
implemented discovery registrations.

`Owner{Remote, Generation}` is the exact activated native transport generation.
Native SessionClient.ConnectionID and descriptor Connection[3] are connection
registration CIDs/RVCIDs, **not account PIDs**. They remain unverified at this
transport layer. Default
admission allows that transport to maintain only its own ephemeral FreeRide
record/claims. Another generation cannot update/delete it by repeating a CID,
source IP or session ID. Duplicate live claimed CIDs are rejected. The optional
`Admit(context, SessionInfo, claimedCID)` hook can impose independently recovered
CID-registration/admission checks; this service changes no account, car, money, progression
or statistics state.

Wire descriptor reference IPv4 is canonical big endian and is overwritten with
the actual native transport source for an owner update, matching `0x131330`'s
copy of source reference fields into the stored session. Initial creation uses
the same explicit server policy. The four connection scalars are preserved;
contact fields are not invented from another protocol's port.

Root integration must invoke `OnDisconnect(SessionInfo)` or `DropOwner(Owner)`
on native expiry/disconnect. `Snapshot()` returns independent, sorted session
records for presence/density; it does not create a second membership database.
`Membership(callerCID,targetCID)` returns a same-session claim relationship with
both transport owners and an optional published host descriptor. This relationship
is not account authority. A connectivity registry must independently check its
verified Quazal registrations and admitted endpoints before issuing introductions.
A joining driver's SessionClient port is never treated as a published peer port.

The active race browser probes a listed host before it has a joined session.
`BrowseIntroduction(callerOwner,targetCID)` therefore provides a separate,
short-lived discovery capability. Search/filter records only the exact published
generic rows it actually returns: maximum three per request, 32 remembered targets
per caller and at most the configured MaxOwners caller contexts. Each grant expires
after ten seconds and binds session ID, exact target owner/generation, profile
identity, reference, all connection values, NAT, game/name, restriction fields and
opaque advertised metadata. Contact/name/classification changes,
deletion, session expiry or either owner's disconnect invalidate it. It creates no
caller CID claim, room reservation, or membership. Root connectivity must still
validate independent authenticated CID registration and source compatibility.


## Actions and asynchronous correlation

All scalars and lengths in this layer are big endian. Objects and strings have
a u16 byte length followed by their bytes. The action signature is part of the
counted object. See the [edNet envelope](native-services.md#action-envelope).

| Operation | Request | Body after request signature | Business reply |
| --- | --- | --- | --- |
| FreeRide create-or-join | `0xe2dd` | Client object, blacklist object, FreeRide object, opaque u32 | `0x5ce5`: result byte, optional FreeRide object |
| FreeRide update | `0xa518` | FreeRide object | `0x8d1c`: session ID u32, event byte, reference IPv4 u32, port u16 |
| Get session info | `0x6427` | Session ID u32 | `0x7848`: session ID u32; optional FreeRide object |
| Delete session | `0x586e` | Session ID u32 | Generic reliable completion only |
| Plain search | `0xecd1` | Session name string, limit u32, optional opaque trailing i32 | One `0x4fe2` per match: session ID u32, base SessionV3 object |

Constructor `0x130890` hashes the exact signature strings into globals used by
these builders. Create builder `0x12f820` serializes client, blacklist, FreeRide
and a final u32; update builder is `0x12fa70`, get builder `0x12f720`, delete
`0x12f330`, plain search `0x12f5e0`. The trailing create scalar is a u32 even though the game passes a byte-sized
NAT value (`0x410930`). Dispatcher `0x131330` reads and discards it before
calling the matching callback. Nonzero values are accepted and confer no
account/security meaning. The trailing search scalar is also consumed/ignored.

Create/update use `0x12b380`, producing **unreliable envelope `0xd572`**. Server
dispatcher `0x131330` also emits create, update-notification, get and search
business responses through `0x12b380`. They are not correlated by a second hidden
transaction field: create response uses its result/descriptor; get/search include
the session ID. Receiver `0x130f60` dispatches these to the pending client callback.

Get/delete/search builders use reliable `0x509a` requests. Generic wrapper
`0x12b520` completes those transactions with `0x984e`, while business responses
are independent `0xd572` datagrams. Therefore a reliable get returns **two packets**:
`0xd572` containing `0x7848`, followed by empty-object `0x984e` with the
original transaction ID. `0x12b520` calls the business dispatcher synchronously
before constructing and sending completion, establishing this order. They must not be concatenated or reduced to a single assumed wrapper.
ACK `0x08e7` produces no reply.

Create result 1 means created, 2 means join; result 0 enters the client's failure/
retry path (`0x4138e0`). Receiver `0x130f60` permits result-only failure without
an object. Capacity exhaustion uses result 0. Malformed, unauthorized and
unsupported operations return explicit server errors, never empty success.

Get missing session returns `0x7848` with ID zero and no descriptor; this invokes
the client's failure callback. Update event `0xfe` means unknown session,
`0xff` means an invalid owner/source and zero accompanies an accepted owner
update. The emitted u16 port is established by dispatcher writes even though
the diagnostic notify signature mentions only three fields.

## GAMEMASTER directory clock probe

The stock directory's slot 1 does not request E2DD directly. Refresh builder
`0x4610e0` hashes `coreresquests::r_gettime()` at `0x539c87` and stores that
**`0x7426`** discovery action; `0x460d60` installs the returned endpoint as
GAMEMASTER. The service therefore implements and registers GetTime alongside its
SessionMaster operations. It accepts no arguments and returns **`0x9f27`**
(`coreresquests::a_gettime(uint)`, string `0x539c68`) plus one BE u32 clock value.

`Options.Clock` can supply that clock; the default is actual Unix seconds as an
explicit replacement-server policy. No active retail reply reader was found to
establish the original epoch/units. Reliable calls use the core RPC convention:
`0x984e` with the same transaction ID and the counted clock action. Unreliable
calls receive a `0xd572` clock action. This is distinct from the separately
emitted asynchronous SessionMaster business responses above. The retained
`sessionmaster-time11.log` records string references and discovery initialization.
The clock handler is real read-only work, not an alias advertising an absent
operation; its legacy retail clock interpretation remains unverified.

## FreeRide V3 descriptor

Writer `0x137420` and reader `0x137220` establish this order:

| Field | Encoding |
| --- | --- |
| Version marker | u16 `0x70ce`, hash of `edNetSessionFreeRide::edNetSessionFreeRideV3()` |
| Profile identity | u64; initial builder reads the profile object field `+0x14` |
| Session ID | u32 |
| Reference IPv4 | u32, serialized from runtime nested reference `+0x268` value `+0x274` |
| Connection | Four u32 values from runtime `+0x10..+0x1c` |
| NAT-related value | u8 |
| Game/version string | Counted string, 1..32 bytes |
| Session name | Counted string, 1..32 bytes |
| Occupancy, capacity, flag | Three u8 values |
| Bounds | Four f32: min X, min Z, max X, max Z |
| Additional data | Counted buffer of exactly 128 bytes |

Initial builder `0x414b20` uses game/version `TestDriveV2.30`, a name derived from
`FreeRideV5`, maximum eight slots and bounds formed from player X/Z ± configured
strength radius (compiled default 1500). The service preserves names and opaque
additional data, checks finite ordered bounds and bounds capacity to 1..8.

Join callback `0x4138e0` proves contact semantics: reference is advertised IPv4;
low sixteen bits of Connection[0] are the peer port; Connection[1] is LAN IPv4;
low sixteen bits of Connection[2] are LAN port; Connection[3] is the self-claimed
RVCID. `0x153d40` builds the Quazal-aware endpoint extension from the latter fields.
The first create reply can have no usable peer endpoint; its owner must publish
an update before another driver is introduced to it. The service requires nonzero
reference/peer port/CID and consistency with the owner's initial CID claim.

## SessionClient and blacklist

SessionClient is **not** peercrypto's 56-byte edNetClient ticket descriptor.
Its marker is `0xfae0`, initialized at `0x5513d0` from the string at `0x50fb60`.
Writer `0x137da0`/`0x1380a0` and reader `0x137c20` establish:

1. Counted name, at most sixteen bytes, then a flag byte.
2. IPv4 u32, port u16, NAT byte and claimed connection CID u32.
3. Four opaque u32 values, then reference IPv4 u32.
4. Client port u16 and client flags u16.
5. Optional counted forty-four-byte raw PS2 extension.

The extension's bytes are preserved, not reinterpreted as account proof. The wire CID at native runtime `+0x134` is populated by `0x410930` using
`0x1e23d0`, which reads the connection context `+0x0c`. Account PID getter
`0x1e23a0` separately reads context `+0x08`. FreeRide identity `0x414b20` comes
from profile `+0x14` through `0x3b46e0` and its virtual profile accessor, so the
service does **not** require that u64 profile identity to equal either account PID
or CID. The independently tested profile/connection values differ.


Blacklist `0xdc69` is initialized by `0x1351d0`, built by `0x12f820` and read
by `0x134f50`: up to sixteen `(session ID u32, relative seconds f32)` pairs, with
no entry count. A positive relative timeout excludes that ID for this lookup.
Expired/zero entries do not exclude it. The parser rejects non-finite/negative
or policy-excessive timeouts and truncation rather than accepting partial pairs.

## Registry policies and bounds

These are deliberate replacement-server policies, not recovered original
central-service internals:

- At most 128 sessions and 1024 transport claims by default, configurable within
  bounded limits. No background worker or unbounded transaction cache is needed.
- Session expiry defaults to fifteen seconds, matching the SDK's session expiry
  loop at `0x130300`. Owner updates and idempotent create retries refresh it.
- Match a published session with equal game/version and session name, overlapping
  X/Z bounds, spare capacity and no live request blacklist entry. Choose the
  lowest matching ID deterministically; otherwise create a new monotonically
  allocated nonzero session ID.
- Reserve one slot per joining transport for five seconds. Repeated requests
  return the same result/session and do not consume another slot. Reported
  occupancy is the greater of owner-reported occupancy and active reservations
  plus the owner, bounded by capacity.
- A joined owner whose new request bounds no longer overlap its previous group
  can select/create another group without a blacklist. Equal-mode overlapping
  retries retain one reservation. A host can explicitly blacklist its own record
  or select a disjoint/different group; removal affects only its own abandoned
  record and its associated claims. Native merge/election messages remain peer
  operations; spatial reselection is explicit replacement-server policy.
- Retain each claimed CID/transport relationship while its host record is live,
  even after its temporary slot reservation expires. A new group selection,
  explicit owner disconnect or record expiry removes it. Expiry of a reservation
  alone cannot allow another transport to impersonate that live claim. An expired
  reservation no longer authorizes a connectivity introduction; an explicit new
  selection must renew it. Retaining a CID claim is not proof of peer admission.
- Only the record's exact owner can update or delete it. Deletion is idempotent
  for an already absent ID; host deletion/expiry removes associated claims.

Plain/filtered search uses the base `edNetSessionV3()` marker `0x7eca`, writer
`0x137950`, rather than FreeRide serialization. Results preserve actual legacy
metadata and exclude the requesting owner. Only published records appear, with
at most three results before reliable completion, fitting the native four-frame
batch limit. No result-count or empty result packet is invented.

## Generic race-host registry

The active hosting path `0x39e930`, installed through virtual table pointer
`0x567eac`, calls reliable create builder `0x12fec0`. Constructors `0x3f57a0` /
`0x39eff0` construct a base descriptor; `0x39d350` consumes its result and installs
the returned session ID. Native game-loop `0x423c80` calls update builder
`0x12fd90`. Race setting helpers `0x3f43d0` and `0x4b3190` modify this current
record through restriction builder `0x12fba0`. Race browser `0x39d3c0` chooses
plain/filtered search. This makes generic create/update real active client paths,
independent of the initially traced FreeRide lookup.

| Operation | Request/body | Reply |
| --- | --- | --- |
| Generic create | `20db`, ClientV2 object, BaseSessionV3 object | **Inside reliable `984e`**: `e72c`, result byte, ID u32, optional BaseSessionV3 object |
| Generic join-free | `eb6b`, ClientV2 object, BaseSessionV3 object, blacklist object, optional opaque u32 | **Inside reliable `984e`**: `d553`, result byte, ID u32; success adds FreeRideV3 object and source endpoint object |
| Generic update | `e133`, BaseSessionV3 object | Asynchronous `8d1c` notification as above |

Create/join result 1 succeeds; failure has result 0, ID zero and no objects.
Generic results use the native manager's reply stream, rather than FreeRide's
independent `d572` callback. Receiver `0x1300d0` expects the nested create result.
The shipped generic dispatcher implements `eb6b`, but no active TDU sender load
of its global was found in the main image/overlay: its full live-client use remains
unverified. Native peer admission and race starts are separate client operations.

`BaseSession` encodes: marker `7eca`, identity u64, reference IPv4 u32, four
connection u32 values, opaque u64, NAT byte, game/name counted strings, occupancy
and capacity bytes, five legacy u32 fields, ID u32, and an optional counted object.
`0x137b40` provides **128 bytes** for that extra object. Both u64 values and all
five full-width u32 fields are preserved. Identity is opaque in this format and
is not assumed to be an account PID or native CID. For ownership consistency, Client.ConnectionID and a host's
nonzero advertised Connection[3] must agree; either remains a self-claim.
A joiner's target descriptor Connection[3] identifies the host, not the caller.

`0x1359d0` proves the additional join source object: marker `269e`
(`edNetClient::edNetClientV2()`), observed host IPv4 u32, observed port u16,
endpoint type i16. The type-2 source endpoint contains no type-0 key/name fields
or optional extension. Its observed endpoint is distinct from the advertised peer
contact retained in the session descriptor.

Generic records share the same session/owner/CID namespace, limits, expiry and
cleanup as FreeRide. A transport replacing its own cruise record with a race
record removes its abandoned cruise membership. Reliable create/join retries
return the same record without consuming another slot. A caller cannot adopt,
update, delete, or claim another live transport's ID/CID. Unlike the embedded
SDK's permissive update/upsert/takeover branches, an unknown generic update gets
an explicit unknown-record notification; foreign ownership is never transferred.

The unused generic SDK join branch matches game/version, address family,
capacity and blacklist. This replacement also checks the requested name,
nonzero selected session ID and nonzero classification fields to prevent joining
an unrelated race. That tightening is server policy, not a recovered extra retail
wire requirement. No match returns the actual failure reply. Search remains
bounded to three rows per request.

## Restrictions and filtered queries

| Action | Fields after signature |
| --- | --- |
| `7ae3` filtered search V2 | Name string, limit u32, ID filter u16, signed filter i16, category u8, two reserved u32, optional opaque trailing u32 |
| `3e43` filtered search legacy | Name string, limit u32, ID filter u16, packed signed i8/category u8 in one u16, optional opaque u32 |
| `31e0` restriction update V2 | Session ID u32, capacity u16, opaque field u16, signed field i16, category u8, ID field u16, three reserved u32, optional counted buffer |
| `a9e5` restriction update legacy | Session ID u32, capacity u16, opaque field u16, packed signed i8/category u8 in one u16, ID field u16 |

Dispatcher `0x131330` consumes reserved scalars without applying them. It matches
filter ID/category zero as wildcard and signed -1 as wildcard; otherwise values
must equal the stored fields at `+78`, `+7c`, and `+74`. Owner restriction changes
update capacity and stored `+70/+74/+78/+7c`, refresh expiry, and produce no separate
business action. Browser results use `4fe2`, exactly as plain search. The optional
restriction buffer is bounded/preserved by the request codec but is not applied:
the shipped dispatcher does not read it into session state. Opaque selection
numbers are not labeled as particular game modes without further evidence.

Native stream offsets are checked against actual routines: `+24` byte,
`+28/+2c` i16/u16, `+30/+34` u32/i32, `+38` counted string, `+3c` counted
buffer, `+40` counted object, `+4c/+50` i64/u64, `+54` f32. Thus an argument
loaded from a byte or short register does not determine its wire width.

## Activity density

Actual sender `0x3bd9d0` emits `510b` plus **u32 client type 2**. Reply `1454`
contains i32 cell count, i32 total, then a counted byte buffer **only when both
counts are positive**. The receiver at `0x41f678` independently executes against
the exact zero/nonzero layouts in presence's native consumer fixture.

Native asset loaders establish 210 non-type-7 island spots plus 238 mission
records, total 448. The client appends the type-6 aggregate at index 448, giving
449 cells. This is an ordered activity catalog, not a geographic grid. The loader
and private catalog reproduction are documented with the presence package.

Drive-In initialization `0x362640` sets the exact name `DriveIn_PS2`; ordinary
challenge initialization `0x335be0` uses `<activityID>_PS2`. The server counts only
published generic records with those exact names. `DriveIn_PS2` maps to index 448;
optional `Options.ActivityCatalog` provides the independently generated ordinary
name/index mapping. `New` copies and validates it, so caller mutation cannot
change classification. Without a private catalog, ordinary activity slots stay
empty. FreeRide cruise groups never populate activity cells.

Counts represent actual published sessions, with cell bytes saturating at 255.
That aggregation is explicit replacement policy: the client image's generic
server has no `510b` handler from which the original central counting algorithm
could be recovered. It grants no race admission or progression rights.

## Peer-owned transitions and remaining limits

`0x410930` issues FreeRide backend create-or-join. Its result callback `0x410ad0`
feeds group transitions at `0x4138e0`. Group master election `0x415df0` exchanges
peer announcements through `0x422ee0`, chooses a peer/master locally and then
issues a backend selection where needed. Scan/merge control at `0x416a00` and
local lock/slot state are not separate recovered backend "merge" RPCs. The
registry supports explicit reselection, ownership and short introductions; it
does not pretend to complete the peer handshake or simulate a race.

Generic CreateOrJoin `113a` and uncalled older SDK update variants remain
unsupported. Native scene synchronization, host migration and full racing still
require the separate recovered peer protocols and a stock-client test. Successful
registry requests and synthetic two-client flows are not proof of retail cruising.

## Verification

```sh
scripts/go test ./internal/games/tdu/sessionmaster
scripts/go vet ./internal/games/tdu/sessionmaster
```

Tests cover independent Python-struct schema fixtures, exact signature hashes,
malformed/truncated descriptors, optional extension boundaries, non-finite
coordinates, blacklist limits, spatial/blacklist matching, simultaneous bounded
joins, idempotent retries, duplicate CID claims, generation ownership, publication,
reservation/session expiry, disconnect cleanup, admission rejection, separate
reliable/business responses and bounded base-session search results. Fixtures
contain synthetic data only. `testdata/verify_native.py` executes retail
`137420`/`137da0`/`137950`/`1359d0` serializers, `12fa70`/`12f820`/`12fba0`
request builders and the actual `d572` wrapper under the documented limited
EE-to-MIPS64 harness. Captured outputs round-trip byte-for-byte in Go, including
nonzero create mode and full-width u64 values. Native density consumer fixtures cover zero/nonzero
counts and the exact conditional buffer. Outer construction/reset and
transport append are mocked; no serializer is mocked. No retail connection
success is claimed.
