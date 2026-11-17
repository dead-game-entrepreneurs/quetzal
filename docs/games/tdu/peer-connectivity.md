# Peer connectivity and admission

The inspected US PS2 client sends ordinary peer associations and gameplay by
**direct UDP**, with Quazal-assisted endpoint discovery. Its native backend
proxy is a separate path. These are static findings from the build identified
in [analysis.md](analysis.md); a stock two-client session remains untested.
See [peer-security.md](peer-security.md) for exact EncKey and packet codecs.

## Selecting a transport

`0x136ec0` delegates wrapped sends to `0x153810`. Its decision depends on the
runtime edNet client descriptor, not a universal port or one global encryption
scheme:

| Runtime descriptor condition | Send path |
| --- | --- |
| Extension-used length `+0x3c` is zero | Plain UDP to advertised address `+0x68`, port `+8` |
| Extension kind at `+0x10` is greater than 1, CID `+0x14` is zero | Plain UDP to advertised address/port |
| Extension-used length is exactly 2 and native proxy is ready | Backend tunnel `0x154f00` |
| Nonempty extension with kind greater than 1 and CID | Existing or newly allocated native peer association |

The two-byte case is the compiled backend directory/service placeholder.
Normal Quazal-aware peer setup calls `0x153d40`, which fills sixteen meaningful
runtime extension bytes: kind 2, CID, LAN IPv4, LAN port and a NAT byte. **Sixteen
is not the serialized extension length.** Writer `0x135c00` writes the fixed
44-byte extension whenever any extension is present; reader `0x135b20` stores
the received count. The 56-byte serialized descriptor and 130-byte EncKey
fixture remain unchanged.

Peer associations are allocated by `0x153290`, initialized by `0x1527a0`, and
advanced by `0x151c40`. The eventual peer packet sender `0x152c80` always calls
UDP sender `0x142c60` directly. It selects either the association's advertised
endpoint or the resolved alternate endpoint; it does not call proxy sender
`0x154f00`. Native peer command 0 contains the client-generated EncKey; commands
1/2 handle its result, and established commands 3/4 use the downloaded p2pkey.

The proxy's incoming forwarded envelope carries a logical source address/port.
`0x154410` restores those fields and returns its application body through
`0x1535d0` before the separate peer decoder `0x1520e0` runs. Blindly forwarding
peer-protected bytes through this tunnel would therefore not implement the
proven direct association path. No arbitrary destination forwarding is required
by the recovered baseline or justified by this investigation.

## Endpoints introduced by sessions

Join callback `0x4138e0` installs the returned host endpoint and connection
fields into runtime descriptor `game + 0xdb58`:

| Session value | Client use |
| --- | --- |
| Nested endpoint's resolved IPv4, runtime session `+0x274` | Host advertised IPv4 in descriptor `+0x68` |
| Low sixteen bits of `Connection[0]` | Host advertised **UDP port**, descriptor `+8` |
| `Connection[1]` | LAN IPv4 in the type-2 extension |
| Low sixteen bits of `Connection[2]` | LAN port in the extension |
| `Connection[3]` | Claimed target Quazal CID in the extension |
| Session NAT byte `+0x264` | NAT-related extension byte |

The first connection scalar is not a native proxy session identifier. The
assembly stores it at `0x413c60` into descriptor `+8`, then calls `0x153d40`
with the LAN address, port, CID and NAT byte at `0x413c78`.

The nested IPv4 is serialized by `0x137420` through `0x1353c0(session+0x268)`;
that helper writes the resolved value at nested `+0x0c`, i.e. session `+0x274`.
The wire codec calls this value `FreeRide.Reference`. Its wire position must not
be confused with the runtime object offset. The original server-side update
branch in `0x131330` copies the actual source descriptor into that nested
endpoint. This supports the server policy of publishing its observed live
owner address rather than accepting an unrelated claimed public destination.

An initial creation request has zero connection values. A session response
that names a host with zero advertised port or target CID is insufficient to
start the normal peer association. Publishing only a live owner's updated,
usable endpoint is a bounded revival policy; it is not proof that the original
unavailable session-creation hook used precisely the same policy.

## Quazal traversal dependency

`0x1527a0` looks up the target CID through `0x2445e0`. A missing result queues
up to eight associations for `0x153e70`. That routine builds target StationURLs
from the session-supplied advertised address, port and CID; when the advertised
address equals the local public address, it substitutes the supplied LAN
address/port. `0x244aa0` starts a five-second NAT-engine operation for the list.
Results are cached by CID for up to thirty seconds (`0x2445e0`, `0x244850`).
The association retry path can then replace its initial endpoint with the
resolved result before sending the EncKey.

This title supplies a custom `lrTraversalAdapter` rather than using the backend
PRUDP socket for native traversal traffic. The path is:

`0x245330` → vtable `0x567fa8`, slot `+8` → `0x3a51e0` → `0x142c60`.

The last callback takes the engine UDP socket from
`*(game+0x5c)+0x5c` and sends the NAT-engine bytes directly to the selected
StationURL address/port. Its adapter header accessor `0x245310` exposes four
bytes `cd 80 00 00`. The complete receive-demultiplexing path and all NAT-engine
message layouts still need closure; that four-byte observation alone is not a
complete server protocol.

Quetzal shares reliable Quazal transport and protocol-3 probe handlers with
25 to Life; TDU uses its own method-11/2 row layout. TDU supplies title callbacks to share those transports while resolving native
engine endpoints. Returning the
backend RMC connection's observed port as a native peer port has not been
validated. The callback must resolve endpoints from current title-owned records
and must not honor arbitrary addresses supplied in an RPC URL.

## Proxy NAT controls are separate

Native proxy control requests also measure NAT behavior. The recovered client
accepts command 51 (`0x33`) with two four-byte probe addresses, then collects
six-byte observed endpoints in replies 53, 55 and 56. It compares observations
for stability in `0x153fe0`, records a numeric class 2, 3 or 4, and reports that
byte with command 58. Missing or unstable observations follow explicit class-4
branches. These class labels have not been equated to named NAT categories.
Real alternate probe addresses and response-source behavior must be recovered
before generating successful classification responses. This measurement is not
Quazal account proof and should not be synthesized merely to advance a flag.

## Identity and admission boundary

Account PID, registered connection CID/RVCID, native transport ID/generation and
profile identity are separate identifiers. Getter `0x1e23a0` reads account PID at
backend context `+8`; `0x1e23d0` reads registered connection ID at `+0x0c`.
SessionClient builder `0x410930` and published connection field three use the
latter. The FreeRide u64 identity comes from a profile object through `0x3b46e0`,
not either getter, and must not be forced equal to PID or CID. USERMAP uses the
account-PID getter. The wire has not changed; earlier PID labels were incorrect.

A native transport generation owns its ephemeral records. Its shared-key
handshake and claimed CID do not independently prove account ownership. Quazal
caller and target must each have registered ticket-proven named-account sessions.
Their current CIDs must agree with native record identifiers, and observed native
and RPC IPv4 addresses must be compatible. This is an endpoint consistency check,
not cryptographic native account proof. Account/statistics writes require the
separate verified Quazal context.

## Browser probing occurs before joining

The active race browser `0x39d8c0` collects up to eight discovered BaseSession
endpoints while `0x4193c0` reports no current session. It calls `0x244aa0` before
joining a peer game. That function uses StationURL mode 3 (`0x213f60`), selecting the literal
`udp:/`, then fills the published
address/port, with recovered LAN substitution when public addresses match.
`0x214550` writes **RVCID** from `Connection[3]`. These browser URLs contain no
account PID, CID or type property. Mode 3 is a constructor selector, not a
serialized type property. A membership-only probe policy would reject
this normal stock path.

SessionMaster therefore remembers only published generic records actually
returned to that native browser owner/generation. These grants expire after ten
seconds and are bounded to 32 targets per caller and the configured owner limit.
They capture the exact host generation, session identity and endpoint. Every use
rechecks the live record; deletion, expiry, endpoint replacement and disconnect
revoke permission. A grant creates no room membership or reservation.

The adapter ties a browser caller to exactly one fresh USERMAP account-PID claim,
then passes its observed native owner to the grant lookup. Duplicate live claims
are ambiguous and fail. The connectivity registry independently validates the
Quazal caller proof, target registration CID and both observed IPv4 addresses.
This permits a bounded prejoin introduction without granting arbitrary URL
forwarding. Fresh USERMAP availability and reverse native source-port reachability
still need stock-client validation.

## Implemented Quazal introduction policy

`internal/games/tdu/connectivity` supplies callbacks for live native membership
or the short-lived browser grant above. It neither sends raw NAT-engine packets
nor forwards gameplay. Stock clients retain direct UDP traversal and EncKey
association.

| RPC | TDU application shape | Backend path |
| --- | --- | --- |
| 3/1 | u32 count, StationURL strings; empty reply | Shared probe handler |
| Client-dispatched 3/2 | One StationURL; empty reply | Shared outbound instruction |
| 11/3 | Bool, u32 count, StationURL strings | Shared lookup with TDU callbacks |
| **11/2** | Request CID u32 then account PID u32; reply bool, u32 count, rows of StationURL then CID u32 | TDU `HandleRPC` |

Linked writer `0x1f61a0` calls method-2 builder `0x1f4b20`. Dispatcher `0x1f51a0`
uses reader `0x1f5880`/`0x1f5df0`; consumer `0x1f8240` applies the row scalar
using literal CID through `0x2144e0`. Caller `0x1f7bd0` extracts the independent
CID/PID selectors. CID-only lookup with PID zero is valid. Returned native StationURLs retain
RVCID equal to the target registered CID: `0x1f8240` preserves URL properties
while applying CID, and active NAT-result lookup `0x2445e0` reads RVCID through
`0x214590`. Protocol-3 writer
`0x246e90`, dispatcher `0x247290` and list reader `0x247490` match the shared
transport's shapes.

A browser RVCID selects the currently registered account by connection ID. If a
URL also supplies PID or CID, selectors must agree. Host address/port must equal
the currently admitted published endpoint. The registry queues an instruction to
the verified target naming the caller's observed **native engine** source port,
using `udp:/address=...;port=...;RVCID=callerCID`. Dispatcher `0x247290`
passes that URL unchanged, and adapter `0x245330` consumes its address/port for
raw native UDP. It suppresses the shared default instruction that
would publish the backend RMC port. Queue draining rechecks registrations,
generations, session identity, selected endpoints and a ten-second lifetime.
Stale native CIDs cannot authorize a replacement Quazal registration.

## Verification and remaining checks

Focused tests cover independent account/CID values, CID-only lookup, exact
RVCID-only browser URLs, native versus RMC ports, LAN substitution, prejoin grants,
all-or-nothing queuing, arbitrary-target rejection and stale registration/endpoint
revocation. Real-UDP integration exercises both FreeRide membership and discovery
before joining a Drive-In session. These tests do not establish completed retail
peer association or exchange of driving state.

Private evidence remains in sibling `tdu-server/work/authentication/ps2/peer-*`
and gameplay browser/getter queries. The remaining decisive checks are the custom
NAT receive path, a stock traversal request/result, fresh startup and two stock
clients completing EncKey/peer login using real NAT endpoints. Proxy NAT-class
controls remain distinct; guessed alternate probe sources or an arbitrary relay
are not implemented.
