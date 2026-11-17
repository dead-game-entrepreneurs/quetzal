# TDU PS2 presence and GPS tracker

`internal/games/tdu/presence` implements the active US PS2 nearby-player query,
world density map and USERMAP position event. These are native edNet actions,
separate from Quazal/PRUDP and peer vehicle synchronization. They publish
short-lived, transport-owned presence; they grant no account permissions.

The supported executable is SLUS_214.90, SHA-256
`620ea714b1dfd2d003a9791e5fabc2e8c9f1f6ac5c908f5873cd7e54972c7bf5`.
Addresses below are executable virtual addresses. Wire recovery is static except
for the explicitly identified retail routine fixture. A stock console startup,
map screen and two-console nearby discovery still need capture validation.

## Active action IDs and routing

| Request | Reply | Client builder / receiver | Logical destination |
| --- | --- | --- | --- |
| `42bb` nearby v2 | `c4d5` | `3bd670` / `41f71c`, also queued completion `429c40` | DensityPort, default policy 9003 |
| `55ae` density map | `d768` | `3bd8d0` / `41f5f4` | DensityPort |
| `89ee` USERMAP update | none | `460630`, periodic caller `460f50` | UserMapPort, default policy 9004 |

These IDs come from the title's CRC32-to-u16 signature hash, initialized at
`55df80` into BSS globals `aa6140`, `aa6150`, `aa6158`, `aa6160`. Hash input ends
before the terminating NUL. The active strings are:

- `gpstracker::r_track_users(uint userID,uint gameid, uint type, uint nbusers,float PosX, float PosY, float PosZ, object pref = NULL)`
- `gpstracker::a_track_users(uint nbusers,{float PosX, float PosY, float PosZ, string name,uint gid,uint gid_type})`
- `gpstracker::r_get_density_map_by_level(byte level)`
- `gpstracker::a_get_density_map_by_level(buffer map)`

`89ee` is the hash of
`coreresquests::e_update_user(uint user_id_in, uint team_id_in, string gamertag_in, short client_type, float coord_x_in, float coord_z_in, int level_in, int car_type_in, uint game_id_in, int game_type, edU64 car_cat)`.
The misspelling is present in the executable. Signatures omit some real fields
and describe others imprecisely: the actual serializers/readers govern the codec.

The older `DensityMap::nearPlayers`/`Map` signatures computed by `3bdad0` are
not the active wire action IDs. Legacy GPSTracker `9edc`/`c1d4` is also not
implemented. Action IDs require service context; this service registers only
its three recovered requests. In particular, the startup directory request for
`55ae` can now resolve the GPSTracker destination.

The stock builders call `12b380`, which emits an **unreliable `d572` envelope**
with a counted action. Nearby and map requests receive a separate `d572`
carrying their actual reply action. USERMAP is a void event and receives no
reply. The implementation rejects reliable `509a` variants rather than inventing
completion behavior. It consumes an ACK without a reply. All multi-byte action
fields below are big endian; strings and buffers have a u16 byte length, with no
string NUL on the wire. All packets remain inside the native proxy's datagram
limit; a full map reply is 1365 bytes including the edNet envelope.

## Recovered nearby format

`42bb`, excluding its two-byte action ID:

| Field | Encoding | Observation |
| --- | --- | --- |
| Claimed user ID | u32 | Local user manager `+14`; no identity proof |
| Client type | u32 | Fresh edNet descriptor `+40`, observed value 2 |
| Game/session ID | u32 | Getter `4193c0` |
| Requested maximum | u32 | Retail sends 16 |
| X, Y, Z | three f32 | Query origin |
| Car/category mask | u64 | Getter `3ce550(4)`, retail zero-extends its u32 result |
| NAT classification | u32 | Byte-valued getter `3a0d90`, serialized through the u32 writer; default 3 |
| Online friend IDs | remaining u32s | No count; at most 100 in builder |

The entire action is `42 + 4*friendCount` bytes. The server accepts limits 1–16.
The original signature calls the second argument `gameid`, but the actual
builder obtains it from the descriptor's client-type field. The following
argument is the current game/session ID.

`c4d5` is a byte row count followed by each row:

| Field | Encoding |
| --- | --- |
| X, Y, Z | three f32 |
| Display name | counted string, server sends at most 16 bytes |
| Game/session ID | u32 |
| Game type | i32 |
| Client type | u16 |
| Level | byte |
| Car/category mask | u64 |
| Car type | byte |
| NAT classification | byte |

USERMAP retains full u32 level/car-type values; the nearby reply exposes only
their low byte because the native reply reader consumes one byte for each.

A trailing u32 total is supported by both native readers; the server always
includes it. Rows contain no PID. The server returns nearest X/Z positions of
other live transports with the requested client type, capped by the requested
limit, with deterministic tie ordering. It reports the number of matching
candidates before the limit. This is a replacement-server selection policy,
not a recovered Atari matchmaking algorithm. Friend IDs, category mask and
claimed game IDs are parsed but do not establish permission or silently filter
other players. Race grouping, skill/car restrictions and original friend
preference remain unproven.

## USERMAP update

`89ee`, excluding its action ID:

| Field | Encoding |
| --- | --- |
| Claimed user ID, team ID | two u32s |
| Display name | counted string, at most 31 bytes |
| Client type | u16 |
| X, Z | two f32s |
| Level, car type | two u32s |
| Game/session ID | u32 |
| Game type | i32 |
| Car/category mask | u64 |
| Additional identity metadata | u32 |
| Y | f32 |

The complete action is `54 + nameLength` bytes. The last u32 and Y are absent
from the diagnostic signature but explicitly written by `460630`. The additional field comes from local profile offset `+b4` through `3a1090`
and `460f50`; its meaning is not established and it is not a recovered CID
getter. The client periodic gate
`460f50` uses USERMAP_REFRESH_DT (default 10000 ms); it skips the event when both
X and Z are zero. The server accepts finite coordinates bounded to ±10,000,000,
rejects control characters/oversize names and requires the exact action length.
These input limits are explicit server policy.

One record belongs to `(native source endpoint, transport Generation)`, never
the claimed PID, team, game ID or recycled 16-bit wire session ID. Each owner
can update only its own record. An activated native shared-key transport is
required; that handshake does not authenticate an account. Distinct transports
claiming the same PID remain distinct records. Account admission can be added
only after an independently verified binding exists.

Default policy is at most 2048 records, expiring 30 seconds after the last valid
USERMAP event. Queries do not extend publication lifetime. Nearby queries may
update the owner's NAT metadata; they cannot publish a position. Expiry occurs
on every operation/snapshot and disconnect removes the exact generation.
Snapshot results are copies. All state operations are synchronized.

## Density map geometry and policy

The actual `55ae` body is **two bytes**: level (stock sends zero) and client type
(stock sends two). `d768` contains a counted byte buffer and trailing u32 total.
The native reader copies at most `0x549 = 1353` cells.

Geometry is grounded in executable construction, not an assumed island size:

- GMap manager `5f10b0`, level object pointer `5f11bc`.
- `3bcd50` constructs that level through `467810(this,9,11,8,8)`.
- Constructor fields `+40=9`, `+44=11`, `+18=8`, `+1c=8`.
- Renderer `3d4a00` uses `11*8*768 = 67584` X units and
  `9*8*768 = 55296` Z units, divided into **41 columns × 33 rows**.
- Buffer layout is **column-major: `column*33 + row`**.

The server emits exactly 1353 cells. Each cell is a count of live records of
the requested client type, saturating at 255; total counts all matching in-map
records. Coordinates use half-open bounds `[0,width) × [0,depth)` and ordinary
floor binning. Width/depth can be explicitly overridden through Options. Only
query level zero is supported. USERMAP's player `Level` is retained metadata;
its equivalence to the map request's level has not been established and the
server does not conflate them.

Cell intensity and binning are explicit replacement-server policy. The client
renderer also inserts nearby/offline markers with a threshold-loop calculation
that appears shifted one cell at nonnegative positions; this is not copied into
the server histogram without an original-server capture. Original normalization,
level aggregation and friend/category filtering are still unknown. The proven
world dimensions and byte layout do not establish those algorithms.

SessionMaster density `510b → 1454` is separate. Its actual request is one
u32 client-type field (`510b00000002` in the retail sender fixture), with no
requested cells/hash set. The reply is an i32 cell count, an i32 second scalar,
and a counted byte buffer **only when both scalars are positive**. The reader
permits at most 1400 bytes. `native-density-consumer.json` validates positive
and zero cases by executing both retail writers and consumer `41f678`.

The ordered activity catalog is now recovered:

| Part | Native proof | US record count |
| --- | --- | --- |
| Island spots | `466450` loads SPTA; `45f410` copies non-type7 32-byte records, preserving source order | 407 source minus 197 excluded = 210 |
| Mission activities | `3038e0` opens SCENARICS/AMHMissions.bin; `303510` reads its record count | 238 |
| Active CPSpots registry | overridden count `3033b0`, lookup `303210` appends mission records with runtime stride `0x458` | 448 |
| Extra type6 aggregate | renderer `3d2690` selects the active registry count as its index | index448, so 449 cells |

The active singleton is `607020`, constructed as the CPSpots member of
`5f47b0 + 0x12870`; its overriding vtable is `5658d0`, not the base `56a4e0`.
Ordinary cell indices are the 210 retained island records followed by the 238
mission records. Their identities and positions come from game metadata, not a
geographic 99-cell grid. The type6 locations are EDI_* entries; selecting them
changes game state to `0x0f` in `45d400`. The later session-name trace below identifies the aggregate as Drive-In
activity; **FreeRideV5 cruise groups are excluded**. Code exports
`AggregateDensityIndex` for the extra type6 slot.

`verify_catalog.py` executes the complete native island filtering/copy and the
mission parser through its header-count store, then executes the overriding
count routine. It verifies 210 + 238 = 448, including stable island record order.
The fixture records asset SHA-256 values and counts, without including assets.

Session-name routing is now linked to the active client. Normal activity
selection `335be0` copies the ID through `45d360`, formats `%s_PS2` and calls
setter `419680` for game field `+fac8`. Drive-In initializer `362640` instead
constructs **DriveIn_PS2** (or the separate debug NetMonkey_PS2 namespace).
Browser `3c2110`/`3c21e0`/`3c22a0` and host `3f57a0` copy that field unchanged;
`129e50` is ordinary bounded vsprintf, so its unused N37 argument is not a
suffix. Thus aggregate448 belongs to the Drive-In namespace, while FreeRideV5
cruise groups are excluded. The generic race host clamps capacity to eight.

`LoadActivityCatalog(path)` reads an optional private regular JSON file (no
symlink, no group/other access), capped at64KiB. It checks schema1, build
SLUS_214.90, activity_count448, unique names/indices and supported mission
indices210..447. A subset is permitted; absent entries are not guessed. The
238 mission names are printable ID strings, while binary non-race spot IDs at
indices0..209 are deliberately not decoded or advertised as multiplayer names.
The fixed DriveIn_PS2 alias does not require a catalog and cannot be overridden.

The sibling research tool `tools/generate_activity_catalog.py` verifies the
exact executable, island bank and mission-table hashes before generating only
protocol names/indices. It writes0600 JSON and includes no game code, display
text, coordinates or layouts. Private output for this environment is
`/home/logan/code/quetzal/work/tdu/activity-catalog.json`; configure it with
`QUETZAL_TDU_ACTIVITY_CATALOG`. The runtime has no ISO or research dependency.

The mission table parser is grounded in `303510` and primitives `3015e0`,
`301670`, `301710`, `3016f0`, `3016d0`, `301620`, `301650`: u32 record ID,
eight ID bytes, XYZ, four flags, two NUL strings individually aligned to four,
then two flags/u16/u64/f32/three u32 fields. The private US table has238 records
and is consumed exactly. Only ID names and their source-order indices leave the
private preprocessing step.

Fresh decompilation of generic SessionMaster server dispatcher `131330` has no
510B/1454 branch, so it does not establish the original server's density
selection/scaling or whether cells count sessions or occupancy. Client
`3d2690` uses byte/255 (threshold0.01); `3d44b0` also uses
`cell * 255 / secondScalar`. Those consumers establish representation, not the
original counting algorithm. SessionMaster's runtime counts of live published
generic session records are an explicit replacement policy. They do not count
player occupancy or unrelated FreeRideV5 groups.

## Verification and remaining capture work

Package tests cover retail-emitted complete D572 request datagrams and fixed
golden nearby/update bytes, malformed/truncated
requests, nonfinite positions, result ordering/limits, nonempty map placement,
wrong logical ports, unsupported wrappers, capacity, expiry, PID collisions and
recycled-generation cleanup.

`testdata/native-senders.json` captures retail senders `3bd670`, `3bd8d0`,
`3bd9d0` and `460630`, including the actual D572 wrapper `12b380` and its
transport append boundary `136880`. Cases include online-friend suffixes, full
64-bit masks, signed game type and full-width level/car values. Regenerate it
with `testdata/verify_senders.py`. The limited MIPS64 harness normalizes 128-bit
register saves and low64 PCPYLD moves. It supplies object construction/reset,
service/friend/NAT inputs, memory/string intrinsics, cleanup and transport I/O;
no scalar writer is mocked. This independently verifies the width audit:
USERMAP level/car/game-type and nearby NAT are 32-bit fields, while both map
query fields are bytes. Session-density request is `510b00000002`.

`testdata/native-map.json` was generated by executing the retail scalar/buffer
writers `13a420`, `13a000`, `13a1f0`, then reading it with retail `138b80` and
`1393c0`. The checked map includes nonzero cells 0, 33 and 1352. Go's complete
action encoding matches byte-for-byte. `testdata/verify_retail.py` reproduces
this using the sibling research checkout and the user's extracted ELF. Its
limited EE harness normalizes register-save and 32-bit arithmetic instructions;
only memcpy is substituted on these executed paths. It does not execute a full
client or validate retail matchmaking decisions.

Private, ignored evidence remains in the sibling research tree
`work/gameplay/ps2/presence-{initial,wire,replies,envelope,primitives2,map-grid,world-dimensions,update-callers,user-fields,native-primitives}.log`.
No game executable, assets or copied decompilation is included here.

Next retail observations: confirm startup resolves `55ae` to the advertised
logical service; publish USERMAP and inspect map totals/markers; run two clients
with different positions and compare nearby rows and NAT values; then verify
DriveIn_PS2 and a catalog-listed race populate their expected session-density
slots. Original filters and density scaling should be adjusted only with that
evidence.
