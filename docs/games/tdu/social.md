# TDU read-only friend roster

`internal/games/tdu/social` implements the linked FriendsProtocol query,
**protocol 20 / method 10**, for US PS2 SLUS-214.90. This is distinct from
25 to Life's title-specific protocol-101 friendship methods. No shared TTL
method mapping or friendship enum is assumed.

## Request and response

The application request has exactly two bytes: an opaque filter byte and an SDK
bool byte, restricted by the replacement server to 0 or 1. There is no caller
PID, name or list in the request. The authenticated registered Quazal peer
supplies the principal; guest and unknown-account calls fail.

The application response is a little-endian u32 row count, followed by:

| Field | Encoding |
| --- | --- |
| Friend account PID | u32 |
| Account name | SDK String |
| Relationship value | byte |
| Details value | u32 |
| Status text | SDK String |

SDK Strings have a little-endian u16 byte count including the trailing NUL.
The row has no AnyData wrapper. There is no leading application bool or qResult;
the ordinary outer RMC success/error framing remains. The first filter, request
flag, relationship byte and details word retain their observed positions;
complete enums and selection semantics are not established here.

## Source and current policy

`social.New(accounts, source)` requires an explicit tenant account lookup and
relationship source. `Source(callerPID, Query)` receives both request selectors
and returns the rows selected by its actual relationship state or explicit
policy. Source failures propagate as errors rather than becoming empty success.
The service resolves each returned PID against the tenant's accounts to obtain
the canonical name. Foreign, unknown, guest, duplicate and self PIDs fail.

The current TDU tenant has **no supported friendship mutations or stored
relationships**, so its explicit source policy returns a true empty roster.
That response is exactly four zero bytes. This does not claim that invitations,
acceptance, blocking or presence subscription are implemented. Every operation
except 20/10 is rejected by this module. No friend writes or unused persistence
schema are introduced.

The server bounds rows to 128, matching the projection limit in the native
client. Account names obey the existing 15-byte tenant account bound; status
text has a separate 255-byte replacement-server limit. Text remains SDK bytes
and is not required to be UTF-8; embedded NULs are rejected. These lower server
bounds do not change the SDK's u16 string-length encoding. The shared GameRPC
gate must enforce registered, ticket-proven named-account context before calling
this module. Native connection IDs or source addresses are not account proof.

## Native code and startup behavior

The executable hash is recorded in [analysis.md](analysis.md). Relevant main-ELF
virtual addresses are:

| Address | Established behavior |
| --- | --- |
| `0x1f8c70` | Constructs the FriendsProtocol wrapper; registers protocol `0x14` |
| `0x1e5cb0` | Starts a roster refresh, using manager filter/flag bytes and an output list |
| `0x1f8f40` → `0x1f9750` | Writes method selector 10, one filter byte and one bool byte |
| `0x1f9940` case `0x800a` | Dispatches the response to one list output slot |
| `0x1f9f60` | Reads the u32 list count and each friend record |
| `0x1f8f80` | Reads the row fields in the order above |
| `0x1e4f40` → `0x1e4dc0` | Applies completed list output to the local roster |

The completion projection copies names, status text and PIDs into up to 128
local entries, updates its changed/count fields and clears refresh-in-flight.
The manager refreshes multiple local lists through the same method. This is a
background roster operation: the mapped native online-entry state waits for the
104/1 bootstrap result and native transport/configuration, not this list's
contents. A rejected friend query therefore should be diagnosed separately
from a stalled native master/proxy connection.

Private static evidence remains in the sibling research workspace under
`work/authentication/ps2/friends20-*.log`. No decompiled source or tester data is
included here.

## Native fixtures and tests

[`social/testdata/native-friends.json`](../../../internal/games/tdu/social/testdata/native-friends.json)
contains only synthetic data. Actual request-builder instructions execute with
real scalar/bool and buffer writers; the captured prefix is method selector
plus arguments, not the complete outer RMC header. A synthetic populated row is
consumed by the actual row reader and real scalar/SDK String primitives. High
bytes in IDs, details and status text check widths and byte preservation. The
empty-list reader also executes and consumes its four-byte count successfully.
Synthetic relationship/filter values exercise byte handling, not inferred enums.

The limited MIPS64 harness supplies job/stream lifecycle, scheduling, allocation
and system memory boundaries. Scoped EE stack-save/low-register adaptations and
two equivalent count-multiply substitutions allow those routines to execute;
this is not a full EE emulator or an entire retail RPC exchange. Reproduction
and limits are in the [fixture README](../../../internal/games/tdu/social/testdata/README.md).
Go tests compare response bytes with native-consumed records and verify exact
request bounds, account isolation, source failures, unsupported operations,
duplicate IDs and output limits. A stock social-menu session remains untested.
