# Commandos matchmaking and gathering wire map

This is a static implementation reference for the US PS2 build. It describes
the retail client's requests, not a completed server. See
[online-map.md](online-map.md) for feature scope and validation limits.

## Linked matchmaking request writers

MatchMakingClient binding at `0x8d1640` installs protocols **21** and **50**.
The table below covers the retained outgoing writers, rather than every method
in the much larger SDK reply dispatcher `0x8d3d90`.

`U` = u32, `H` = u16, `B` = one-byte bool, `S` = SDK String,
`L<T>` = u32 count plus elements, `Any` = typed gathering container.

| Pair | Writer | Arguments | Reply |
| --- | --- | --- | --- |
| 21/1 register | `0x8d26c0` | Any gathering | U assigned ID |
| 21/4 update | `0x8d2880` | Any gathering | B |
| 21/11 participate | `0x8d2a40` | U gathering ID, S message | B |
| 21/12 cancel participation | `0x8d2c10` | U gathering ID, S message | B |
| 21/13 participants | `0x8d2de0` | U gathering ID | L<U PID> |
| 21/16 URLs | `0x8d2f90` | U gathering ID | L<StationURL> |
| 21/19 description-regex search | `0x8d3140` | S regex, U range offset, U range count | L<Any gathering> |
| 21/26 register local URL | `0x8d3310` | U gathering ID, StationURL | B |
| 21/27 update host URL | `0x8d34e0` | U gathering ID, StationURL | B |
| 21/30 set state | `0x8d36b0` | U gathering ID, U state | B |
| 50/2 participant filter | `0x8d6960` | U gathering ID, B filter | L<U PID> |

The game-side host sequence at `0x464610` registers the typed object, participates
in it, publishes its local URL and sets state **2**, then installs a periodic
host helper. `0x462aa0` creates the object; `0x4650c0` updates metadata;
`0x466780` updates the player count in both an extension byte and the base
maximum-participants field. Do not overwrite that field with a guessed capacity.

Search goes through `0x4621b0`, with a range initialized to offset `0xffffffff`
and count **10**. Resolve that sentinel as the SDK/client's initial-page policy,
not a huge allocation or an unchecked negative SQL offset. Existing TTL search
expects different arguments and a title-specific protocol-101 query.

Join/leave, URL lookup and participant enumeration have direct game callers in
`0x462e20`–`0x463380`, `0x464eb0`, `0x464fe0` and the probe helper
`0x466a60`–`0x466e90`. The latter queries participant PIDs and URLs and runs
peer probes for latency/discovery.

## Commandos3DGameGathering

Type name: **`Commandos3DGameGathering`**. Native object size: **600 bytes**.
Factory `0x45fcc0`, field writer `0x45fb40`, field reader `0x45fc00`.
The type derives from the SDK gathering/game-session family. The object layout
is not its wire layout; omit the vtable and native String pointer storage.

| Native offset | Serialized type | Meaning / confidence |
| --- | --- | --- |
| `+0x04` | U | Gathering ID |
| `+0x08` | U | Owner PID |
| `+0x0c` | U | Host PID |
| `+0x10` | H | Base minimum participants |
| `+0x12` | H | Base maximum participants; Commandos updates it with player count |
| `+0x14` | U | Participation policy |
| `+0x18` | U | Policy argument |
| `+0x1c` | U | Flags; title host setup ORs `0x10` |
| `+0x20` | U | Gathering state |
| `+0x24` | S | Description; includes encoded search prefix/game name |
| `+0x30` | u8 | Game mode, from host/update callers |
| `+0x31` | u8 | Player count |
| `+0x32` | B | Title flag A; preserve pending full UI naming audit |
| `+0x33` | B | Title flag B; preserve pending full UI naming audit |
| `+0x34` | 32 × u8 | Fixed byte array; copied as a C string by game callers |
| `+0x54` | 512 × u8 | Fixed byte array; map/session detail string used by UI |
| `+0x254` | U | Title discriminator; compared with `0x3068b0()` during filtering |

Base writer/reader are `0x8d7810`/`0x8d78e0`. The derived writer adds exactly
four one-byte fields, 32 bytes, 512 bytes and a u32. These are fixed arrays,
not SDK Strings and not length-prefixed buffers.

The retail typed gathering contains the type String, a u32 outer byte count,
then a Data record: **u32 field-body length followed by the serialized fields**.
The field body itself begins with the gathering ID. The failed stock host
request captured on 2026-11-23 has outer length **600** and inner length **596**.
The earlier map incorrectly omitted that inner length after inspecting the
isolated field codecs and typed-object helpers `0x8c8870`/`0x8c8970`.
The adapter now validates both lengths and emits both when returning rooms.
TTL uses the same length framing but different concrete gathering fields.

`tools/verify_commandos_gathering.py` executes the retail field writer and reader
with low-word EE ABI translation and hooked stream primitives. The 559 calls
are: three U, two H, four U, one S, two u8, two B, 544 u8 and one U. A synthetic
object round-trips exactly. This does not verify the complete Any/RMC/PRUDP
stack, and the trace is not a real packet capture.
The separate retail-sized registration test reconstructs the 600/596-byte
container using synthetic metadata and rejects missing or mismatched lengths.

## Description-regex search

`0x456080` starts from the literal `^ABCDE` at `0x7de480` and changes its five
prefix characters according to filters. Helpers `0x455fa0`–`0x456060` and UI
filters `0x3171e0`/`0x3173c0` read the same prefix in room descriptions.

| Regex position (including `^`) | Values constructed |
| --- | --- |
| 1 | `L` LAN or `W` WAN |
| 2 | `.`, `P` or `A` for the corresponding open/private filter |
| 3 | `.`, `D` deathmatch (0), `T` team deathmatch (1), `S` sabotage (3) |
| 4 | `.`, `F` or `X` for the friendly-fire filter |
| 5 | `.` or `2` for the title/version filter |

There is no need to expose arbitrary SQL. Implement the observed anchored
pattern family with bounded input/result counts and preserve the description
prefix. `0x455fe0` also understands `P` as mode 2; that parser branch alone
does not establish a selectable additional PS2 mode.

## Peer connectivity and migration

Protocol 3 NAT traversal has retained writers at `0x8d91c0` (URL list) and
`0x8d93a0` (StationURL). Its dispatcher also receives server-initiated work.
Peer protocol 22 is registered by `0x8e2610`; distinguish these client/peer
operations from unrestricted backend lookups. Secure-connection 11/2 is linked
and must not be answered with the existing server's differently shaped 11/3
URL lookup response.

The stock browser calls 21/13 and 21/16 after search, then runs a five-second
URL probe (`0x466d80`/`0x466e90`) before keeping results. `0x8e1700` groups those
URLs using `0x66cf50`, which reads **RVCID**, not CID (`RVCID` at `0x7f0ff8`).
The initial adapter returned URLs with CID but omitted RVCID; a second-PC test
showed the room appearing during refresh and disappearing after probing while
the backend still returned one room. The adapter now shares TTL's RVCID metadata
helper, includes it on host/participant URLs and binds it to the proven
registration. After this correction, the tester confirmed two-PC LAN match
entry, leaving and Quick Match re-entry. Internet/NAT coverage remains open.

Host helper `0x6ec650` can call `0x8d1a60` -> 21/27 to update host URLs after a
Net-Z host change. TTL's host-election policy does not establish Commandos
migration behavior. The Commandos adapter still restricts URL updates to the
original host and deletes the room on host departure. Recover and validate its
member, host and owner transitions before enabling migration. Direct match entry
and relay joining have separate retail coverage in the [run guide](running.md).
