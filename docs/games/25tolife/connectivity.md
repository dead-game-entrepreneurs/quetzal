# Connectivity and relay investigation — SLUS-210.16

The initial two-PC Quick Game capture found a room: 101/2 returned one
TTLSession, and 21/16 returned its published host StationURL. The following
request, protocol 3 method 1, failed because it was unsupported. This was a
connection-establishment failure after discovery.

## Round-start stalls and backend retry grace

A last-second join was accepted into both the staging gathering and its round,
but the player reported staying at a static match-in-progress screen rather than
entering gameplay. Backend participation therefore cannot establish native game
entry. A later reported lobby countdown continued beyond 100 while the host's
new TTLRound stayed at state 0, without publishing its native endpoint or
requesting the normal state-2 transition. The native start did not complete;
the counter was not evidence that an active round's timer was advancing.

The original participant's subsequent control disconnect was traced to a
53-byte `101/8` alert reply. Its checksums were valid and its application payload
matched the preceding acknowledged reply. No acknowledgement arrived across
six sends, while the client kept retrying its own request. The shared transport
default exhausted five retransmissions in roughly three seconds. In the later
stalled lobby, a browse-triggered `3/2` fanout similarly left nine connections
with an unacknowledged packet and expired all nine on that short budget.
The captures do not establish why the clients stopped acknowledging, nor that
the short budget caused the initial native round-start stall.

Retail configuration registration `0x0037c7c0` exposes
`prudp_max_retransmission` (data `0x0059f89c`, default 20) and `prudp_InitialRtt`
(`0x0059f8c4`, default 750 ms). The 25 to Life backend now opts into 20 retries
at 750 ms, a bounded approximately 15.75-second fixed-interval window. This is
a replacement grace policy informed by retail defaults, not an implementation
of the original RTT estimator/backoff algorithm. Other adapters retain their
existing defaults. Connection replacement and explicit disconnect still retire
an exact connection immediately; permanent packet loss still expires it.

Disconnect diagnostics now include the pending sequence/type/fragment, retry
count, age since first transmission and age since the last incoming packet.
They contain no RPC arguments or ticket material. Regression coverage exercises
a 4.25-second delayed acknowledgement and successful subsequent request, the
old three-second failure as a control, and eventual expiry on permanent loss.

The native round start, clock and ready-state traffic are not synthesized or
forced by this change. The growing lobby countdown still needs a fresh stock
client test after the transport correction; it is not classified as ordinary
late-join waiting behavior.

See [native clock synchronization](clock-synchronization.md) for the measured
relay forwarding delay, retail correction handlers and remaining constraints
on server-driven resynchronization.

## Secure-service keepalives

A subsequent failure was reported on a real PS2, independently of the laptop
clock-slowdown example. The affected secure connection had no activated native
relay route in that run. Its last successful and first unacknowledged routine
alert replies had the same application data, consistent stream/session/cookie
headers and valid checksums. The client continued transmitting, but no
acknowledgement of the failed reply reached the server across 20 retries.
Increasing the retry budget therefore did not establish a cure.

Retail `0x002e5430` starts periodic PING requests after CONNECT handling on
either endpoint. Builder `0x002e61c0` constructs type 4 with NEED_ACK, without
reliable DATA ordering. The replacement previously answered client PINGs but
never initiated this counterpart traffic.

The 25 to Life secure service now opts into a five-second server PING interval.
This is a replacement cadence, not a claim to reproduce every retail timing
setting. Authentication endpoints and other title adapters retain their
existing behavior. PING sequences are independent of reliable DATA; even a
matching numeric PING acknowledgement cannot release a pending DATA reply.
Outgoing PINGs do not refresh last-receive time or prevent bounded idle expiry.

Debug events record PING sends and matched acknowledgements with RTT. Disconnect
metadata includes sent/acknowledged totals and the last confirmed acknowledgement
timestamps, without cookies, ticket data or RPC arguments. This distinguishes
continued client transmission from demonstrated receipt of server traffic.
The change restores missing endpoint behavior and improves the next diagnostic;
retail verification is still needed before attributing the original disconnect
to absent server keepalives. It does not change native gameplay clocks.

## Native NAT traversal

The retail NATTraversal protocol uses these RMC operations:

| Direction | Operation | Arguments | Reply |
| --- | --- | --- | --- |
| Joiner → backend | 3/1, request probe initiation | u32 count, SDK StationURL Strings | Empty successful RMC |
| Backend → host | 3/2, initiate probe | One SDK StationURL String for the joiner | Empty successful RMC |

The title's dispatcher is `0x005430e8`; reply decoder `0x00543328` accepts
`0x8001` and `0x8002` with no application fields. Backend implementation
`0x00543378` finds each target through its rendezvous connection ID, obtains
the requester's endpoint URL, adds its RVCID, and sends method 2 through the
target's existing connection. Method-2 writer `0x00542f48` and wrapper
`0x00544070` establish the direction and String layout. The receiving callback
`0x00543658` calls `0x00542338` to add a native probing attempt.

Native probing is separate from these RMC envelopes. Sender `0x00543750` builds
a 13-byte payload: one-byte request/reply selector, four-byte rendezvous
connection ID, and eight-byte timing value. Receiver `0x00543888` requires
exactly 13 bytes. At `0x00542840`, selector 0 triggers selector 1 with the
received timing value; both paths update an existing probe result when found.
The exact outer native transport framing is still being traced; do not send
these 13 bytes as an ordinary backend RMC packet.

`0x00543ce8` matches probing records on the StationURL's `address` and `RVCID`.
`0x002b1e18` reads `address`; `0x002b2148`/`0x002b2178` set/get `RVCID`.
Resolution `0x00543940` updates the port from the observed source and records
the round-trip timing when available. This allows NAT port changes; it does
not establish that an unsolicited reply from a different IP can replace the
expected address.

The deployed backend now implements 3/1 and sends reliable 3/2 instructions
to the live registered host. Instructions use the joiner's server-observed
address and assigned rendezvous ID. They require registered named-account
proof and a currently published, accessible room endpoint. Private rooms need
membership or an invitation. Queues and pending responses are bounded;
correlation is scoped to the target connection, and duplicate client calls
do not send duplicate instructions. Expired or replaced connections cannot
receive queued instructions. Tests cover these properties and reliable
cross-peer delivery/retransmission. Retail native NAT probing, session joining and match start are now confirmed.

Retail acknowledged the 3/2 instruction and subsequently requested SecureConnection
11/3 with CID 0 and the host PID. Dispatcher `0x00551498` and decoder
`0x00551900` confirm the reply is bool plus L<StationURL>, with no qResult.
This is implemented for live published room hosts, including private-room
membership/invitation and selector checks. The wire retains the host's native
SID/CID/RVCID. Method 11/2 uses bool plus L<StationURL,u32> records and remains
unsupported; the final u32's meaning needs confirmation. See the
[coverage inventory](protocol-inventory.md) for the wider SDK audit.

## Retry and room-exit behavior

Retail acknowledged the introduction, found the room, retrieved its host URL
and successfully participated. The subsequent native session join still
failed. On retry, the participant list contained the laptop itself; rejecting
that self-entry prevented another introduction even though search returned a
room. The handler now skips self-probes and admits other live participants in
accessible gatherings. Registered participant URLs include RVCID equal to the
assigned CID. Unknown/private-inaccessible endpoints remain denied.

MatchMakingProtocolExt **50** is implemented as a complete four-method table,
recovered from dispatcher `0x00540908`:

| Method | Request | Reply |
| --- | --- | --- |
| 1 EndParticipation | gathering U, message S | B |
| 2 GetParticipants | gathering U, filter B | L<U PID> |
| 3 GetDetailedParticipants | gathering U, filter B | L<ParticipantDetails> |
| 4 GetParticipantsURLs | L<U gathering> | L<GatheringURLs> |

`GatheringURLs` is gathering U and L<StationURL>, codecs
`0x00541bb0`/`0x00541c58`. Detailed participants use the recovered
PID/name/message fields. Every retained member is a live connection; the bool
filter does not add offline records. EndParticipation removes only the exact
requesting peer's membership. When a TTLSession host leaves with registered
participants remaining, the gathering survives for a bounded native host
handoff. Its old endpoint is withdrawn and the room is hidden from discovery
until an existing participant completes the stock `21/27` migration operation.
Another connection using the same PID has no migration authority. Empty rooms,
expired handoffs and explicit gathering deletion revoke their relay candidates.
See [host migration and stale sessions](host-migration.md) for the lifecycle,
connection-replacement policy and retail validation steps.

Tests cover every extension layout, strict booleans/trailing input, grouped
URLs, participant/host leave, player counts and recreation with a new room ID,
alongside self and non-host traversal. The subsequent native URL correction produced a successful two-PC join and
match start. Round-end continuity is a separate validation step.

## Native URL scheme correction

The next capture showed requests and replies for native stream-5 NAT probes
in both directions, with the expected host/guest RVCIDs. It showed no native
session CONNECT/SYN traffic afterward. Backend room lookup, connection lookup
and participation all succeeded. This isolated the original failure to session setup after successful probes.

Both retail 11/1 registration requests advertise **prudp** StationURLs. The
replacement had returned an observed **prudps** URL, which the host then copied
into its published session address. These schemes select different connection
factories: `0x0054dcd0` recognizes prudps for the secure ticket-manager extension.
Native `JobJoinSession` uses its own stream/SID and should retain the client's
native transport. Registration now returns an observed **prudp** public peer
URL. The authentication service's secure-backend URL remains prudps and its
ticket/proof validation is unchanged. Tests assert both URL roles explicitly.

After this correction, the 03:31 UTC capture contains stream-1 SYN/CONNECT
and reliable DATA/ACK in both directions. The user confirmed entering the lobby
and then the match after both players readied up. Related exports are
`native-url-scheme.c`, `peer-protocol-selection.c`, `session-join-native.c`
and `join-job-states.c` under `work/25tolife`.

## Angelware comparison

The sibling project's relevant implementation is
`internal/connectivity/stun/{router,relay,adaptive,stun}.go`, plus the title
observer/rewriter and transport tests. Its default direct window is one second.

| Angelware behavior | Quetzal application |
| --- | --- |
| Real endpoint discovery and direct introduction | Secure registration plus 3/1 → 3/2 native introduction |
| One virtual UDP endpoint per client | Candidate architecture for a Quazal relay; use a separate Quetzal port pool |
| Prove each client's NAT mapping toward its peer's virtual endpoint | Required before admitting either forwarding direction |
| Direct first, timed relay offer after a retry | Desired policy; candidate selection in this client still needs tracing |
| Forward opaque peer traffic between admitted pairs | Preserve native payloads unless captured evidence proves address rewriting is required |
| Bounded clients, pending probes, buffers, rate budgets and expiries | Apply equivalent limits and clean up on room/disconnect/restart |
| Native completion observer | Use actual peer-connection evidence to suppress fallback; an RMC ACK alone is insufficient |

Angelware's DemonWare traversal types, signed introduction packets and
authenticators do not apply to Quazal. Its adaptive offer works because the
DemonWare client can adopt the relay packet's source endpoint. Quazal's
address/RVCID matching means that behavior cannot simply be copied.

## Adaptive relay implementation

`QUETZAL_RELAY_MODE` accepts `direct`, `adaptive`, and `relay` (forced test mode).
The standalone binary defaults to direct; Compose defaults to adaptive. The
separate pool defaults to UDP **30200–30327**, configurable with
`QUETZAL_RELAY_FIRST_PORT` and `QUETZAL_RELAY_LAST_PORT`. The pool is bounded to
2–512 unprivileged ports, cannot overlap backend listeners, and allocates two
sockets per admitted connector/host pair. No Angelware ports are reused.

SecureConnection 11/3 supplies adaptive candidates. Adaptive participant URL
replies (21/16 and 50/4) now supply them before browse reachability checks too.
Forced mode also rewrites
the earlier participant/host URL replies (21/16, 21/28 and 50/4); leaving those
direct lets the title connect before the later lookup. Only authenticated, live,
accessible published-room lookups allocate a route. Private rooms require
membership or an invitation. Both alternatives retain the registered host’s
CID/RVCID and native prudp transport. Adaptive replies put the direct host first
and its virtual endpoint second; forced mode returns only the virtual endpoint.
Forced participant URL replies retain each player’s identity and return its
pair-specific virtual endpoint. Reverse lookups reuse the same pair. Forced
mode fails closed on exhausted capacity rather than silently returning direct.

### Browse reachability precedes joining

A hosted incident showed repeated `101/2` searches returning a public, non-full
room with no filter mismatches, followed by `21/16` returning eleven direct
participant URLs. The user reported that it was absent from browsing but could
be joined through Recent Players. That later path requested `11/3`, selected
the native alternate SYN, proved both relay mappings and entered the room.
The discovery response itself was not missing the gathering.

Stock browser `0x0034bb60` fetches participant URLs; `0x00350e90` starts their
probes through `0x00545360`. Poller `0x00350ee8` waits up to five seconds for
`0x00545710`. The latter groups a participant's alternatives, takes the minimum
successful latency within each group, and returns failure if any group has no
reachable endpoint. Result builder `0x0034c3f8` omits failed-ping rooms, and also
omits full rooms. Consequently, even a non-host participant can make a public
room disappear when all its advertised addresses are unreachable.

Adaptive replies now retain direct-first, adjacent direct/virtual alternatives
for each live participant. A stock 16-player room fits the protocol-3 limit of
32 URLs. Larger synthetic gatherings retain all direct URLs within that budget;
the existing bounded relay pool can also fall back to direct-only publication
on exhausted capacity. Later connection lookup reuses the issued pair.

An inactive candidate accepts only the native USER reachability exchange from
its registered source address and CID, and challenges its observed UDP mapping.
These probes do not activate forwarding, introduce another virtual peer, or
forward DATA. A native alternate SYN still activates the route and obtains the
other peer's mapping proof before forwarding traffic. Thus browsing can verify
the fallback path without switching every reachable match to relay gameplay.
Logs distinguish participant URL count, native reachability probes, actual SYN
activation and forwarding. Retail browse visibility after this correction still
requires confirmation; backend search success alone does not establish it.

The endpoint state machine supplies the fallback timing. `0x002d34e8` groups
public addresses while preserving their order within that group;
`0x002c86d0` divides the connection budget across candidates, and
`0x002c9750` advances to the next URL after failure. Alternate type 2 avoids
advertising a different peer identity or relying on an unsolicited IP change.
In adaptive mode, a candidate alone sends no relay introduction or forwarding
traffic; a native stream-1 SYN selecting the virtual host activates the route.
Forced mode also accepts an authenticated 3/1 for a previously issued virtual
URL and instructs the target to probe the matching virtual peer endpoint. It
never introduces the caller’s real endpoint for that request. Current room
access is rechecked, and cached direct probe URLs cannot bypass forced mode. This is
connection-establishment fallback; migration of an already established direct
session after a mid-game failure is not implemented.

On activation, the server queues a reliable authenticated 3/2 instruction to
the host naming the connector’s virtual endpoint. Transport ticks drain these
asynchronous instructions even when no new RPC arrives. Each side must then
answer a random 64-bit native probe challenge from the exact virtual socket it
will use. The echo must carry that side’s registered RVCID, arrive from its
observed public IP, and match the unexpired nonce. This learns separate NAT
source ports for both directions, including clients sharing one public IP.
Proved mappings cannot be changed by unsolicited packets. Native USER probes
use the retail **checksum seed 0**; backend PRUDP separately uses 0xff. A
captured retail probe is a regression fixture.

After both proofs, forwarding preserves complete datagrams and emits them from
the sender’s virtual socket. Limits are 2,048 bytes per packet, eight buffered
packets per direction, 256 packets/second per pair with a 512-packet burst, a
three-second proof window, 30-second unused-candidate expiry and 180-second
active idle expiry. Host/participant exit and control disconnect immediately
withdraw introductions and retain an already active, fully proved route for a
fixed five-second drain. Only native DATA, DISCONNECT, PING and ACK frames from
the existing exact mappings pass during that window; it cannot admit a new
handshake, mapping or lookup. Repeated exits and traffic cannot extend the
deadline. Unproved candidates and shutdown release sockets immediately.
This preserves retail's reliable native leave packets sent after the backend
quit reply. The next retail test confirmed host quitting now worked; the joiner
sent its leave about 260 ms after the host quit, and route cleanup completed
after five seconds. Queues and route counts are bounded by the
pool. Addresses come from authenticated registrations and accessible rooms;
there is no caller-selected arbitrary UDP forwarding destination.

`GET /connectivity` reports mode, candidate/active counts, proved mappings and
forwarded packets/bytes without account fields. JSON events distinguish candidate
publication, actual alternate selection, proved mappings, forwarded traffic and
cleanup. Relay payloads are not written into backend RPC capture files.

Local UDP tests verify direct-path silence, opaque bidirectional forwarding,
source-port-dependent NAT, same-IP clients, wrong-source/wrong-nonce rejection,
private-room admission, connection identity, asynchronous control delivery,
capacity, expiry and cleanup. **Stock-client forced-relay joining, match start
and movement/shooting are now confirmed.** Both mappings were proved and the
capture contains bidirectional native SYN/CONNECT and DATA/ACK through the
relay; the user confirmed gameplay looked fine. Forced-relay round transition
and blocked-direct adaptive fallback remain to be checked. A stock-client
direct round transition already passed. Neither
an RMC ACK nor passing synthetic tests proves retail relay compatibility.

Local Ghidra exports under `work/25tolife` include `nat-traversal.c`,
`nat-methods.c`, `nat-callbacks.c`, `nat-probe.c`, `nat-start.c`,
`nat-resolution.c`, `nat-url.c`, `nat-address-learning.c` and
`connect-endpoint.c`. They use the executable provenance in
[analysis.md](analysis.md). Raw retail code/captures remain private.
