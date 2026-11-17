# Native peer gameplay and the edNet tunnel

Research provenance: these findings were produced in the sibling `tdu-server`
workspace. Runtime package paths below refer to Quetzal; `tools/`, private
`work/` evidence and Ghidra projects refer to that original research workspace.
Keep those private files there; Quetzal has no runtime dependency on them.
See [running.md](running.md) for current Quetzal operation.

This map targets the verified US `SLUS-214.90` executable and MainGame overlay.
Wire shapes below are recovered from static code. The native proxy/ticket crypto
now has a tested Go implementation with independent OpenSSL/hashlib vectors. No
live PS2/PCSX2 exchange has validated interoperability yet. Addresses are main ELF virtual addresses
unless explicitly identified as MainGame overlay addresses.

## What actually connects the drivers

The client contains several separate networking layers:

| Layer | Responsibility and evidence |
| --- | --- |
| Quazal Rendez-Vous | Account/session identity and NAT traversal adapter; distinct PRUDP transport |
| Native master assignment | An eight-byte UDP request to port 8124 selects the native proxy |
| edNet proxy tunnel | Authenticated/encrypted control and forwarded data over UDP 8122 |
| edNet services | Session/directory/configuration actions carried above the selected transport |
| Peer association | Quazal-assisted endpoint lookup, an `EncKey` ticket and a separate native association handshake |
| CP2P gameplay | Peer login, eight player slots, car/position updates, game-master/start synchronization and social driving actions |

`0x136ec0` selects direct UDP send or the wrapper at `0x153810`.
The latter distinguishes plain addresses, peer associations and the proxy path.
The peer and proxy formats must not be treated as the same encryption scheme.
`0x136b50` receives at most `0x578` (1400) bytes per UDP datagram and dispatches
through the optional wrapper before passing actions to service handlers.

Low-level send/receive are `0x142c60` → SCE `sendto` (`0x26fd08`) and
`0x142bb0` → SCE `recvfrom` (`0x26fe78`). `0x142e10` opens an IPv4 UDP socket
and binds it to the requested port on any local address. A zero-port path at
`0x142d90` opens a datagram socket and sets a socket option via `0x1dfbd0`;
that helper is not a proxy registration operation.

The local client descriptor constructed during peer-login completion
(`0x41a650`) uses port `0x22b9` (**8889**). This is a client endpoint field,
not evidence that a central server should listen on 8889. It starts with a
loopback address and substitutes the NAT adapter's address string where the
Quazal connection is active. SessionMaster descriptors separately use 8122;
see [world-and-sessions.md](world-and-sessions.md).

An optional receive flag at `edNetServiceUDP + 0x6b0` interprets the last six
bytes of a datagram as a replacement IPv4 address/port and removes that footer
(`0x136b50`). Its activation conditions need a trace; it is a separate path
from the ten-byte proxy envelope below.

## Native master and proxy bootstrap

The bootstrap IPv4 supplied by the higher layer is a master assignment address.
`0x1553b0` initializes tunnel state 1 and sends **two big-endian u32 values**
from object fields `+0x98/+0x9c` to that address on UDP **8124** (`0x1fbc`).
The fields are region/language tag and build revision. `0x377b80` assembles the
region from bytes `0x5ea9de/df`; `0x465970` parses revision **4392** (`0x1128`)
from the GOLD build string at `0x53b6a0`. For `us`, the eight request bytes are
`00 00 75 73 00 00 11 28`. `0x153fe0` retries the request.

`0x154410` in state 1 accepts a reply of at least eight bytes from the selected
master address. It copies the first four bytes to the proxy address field
`+0x24` and the next four to `+0x20`. A zero second field is an `OLD VERSION`
failure. The second field is the **directory IPv4**, not an opaque compatibility
scalar: `0x377b80` loads native connection `+0x20`, converts its byte order via
`0x13ac50` and writes manager address `0x5eb5cc`. This positive path and the
master parser have been independently native-executed; see
[startup-handoff.md](startup-handoff.md). It is not a cipher key, HMAC transcript
field or account grant. The parser then creates a 32-byte challenge, enters
state 2 and sends proxy control command 1 to the assigned address on UDP **8122** (`0x1fba`).

The sender (`0x154f00`) constructs this plaintext envelope before signing or
encrypting it:

| Offset | Width | Meaning |
| --- | --- | --- |
| 0 | u16 BE | Flags: control starts at `4`; forwarded data starts at `2`; encrypted packets add bit `1` |
| 2 | u16 | Session identifier, retained in its wire representation |
| 4 | u16 BE | Control command when destination IP is zero; destination port otherwise |
| 6 | u32 BE | Destination IPv4; zero for proxy control |
| 10 | variable | Control or forwarded payload |

After decoding an incoming forwarded message, `0x154410` replaces its source
address/port from envelope offsets 6/4, removes the envelope and returns the
inner payload to edNet. This permits a central proxy to carry traffic while
preserving logical sender endpoints.

### Handshake and control stages

| Client state / operation | Static behavior |
| --- | --- |
| 1: master assignment | Raw eight-byte request/reply on UDP 8124; no proxy envelope |
| 2: proxy connection | Client command 1: u32 region tag from `+0x98`, u32 protocol value `4`, then 32 challenge bytes |
| Receive command 2 in state 2 | Requires a 140-byte payload, caches server public material/session and enters state 3 |
| 3: key exchange | Client command 3 sends a 140-byte public block; retry logic is in `0x153fe0` |
| Receive command 8 in state 3 | Validates the signed exchange context, derives traffic keys from the cached public material and enters state 4 |
| 4: authentication/activation | Sends command 29 (`0x1d`) with no payload |
| Receive command 30 in state 4 | Enters state 5; timeout logic subsequently permits state 6 |
| Receive commands 32/33 | Both set state 6 but use different callbacks; command 32 also sets byte `+0x96` |
| 6: established | Keepalive command 201 (`0xc9`), identity update 38, and NAT probes/control run independently |

The numeric state alone does not distinguish every success/rejection case;
byte flags and callbacks matter. Do not implement every transition to 6 as
successful authentication.

`0x154410` also handles NAT probe commands 51/53/55/56 and related replies.
It collects six-byte observed endpoints from multiple addresses and compares
these in `0x153fe0`, reporting a one-byte classification using command 58.
The full meaning of each classification and its effect on endpoint selection
still needs retail validation. This is not DemonWare STUN.

## Proxy cryptography is in MainGame

`OVERLAYS/MAINGAME.BIN` is an `MWo3` mapped image. Mapping the **complete file**
at header load address `0xaab080` gives the exact crypto entry points called by
the main executable: file offset `0x80` → VA `0xaab100`, offset `0x190` →
`0xaab210`, offset `0x260` → `0xaab2e0`. Its end is `0xbf1e00`, matching the
main ELF heap start. Skipping the header shifts the code incorrectly.

`tools/ghidra/GameplayOverlay.java` verifies the overlay SHA-256 and maps it for
an ephemeral read-only research session. The helper exposes this reproducibly:

Run these historical research commands from the sibling `tdu-server` root:

```sh
python3 tools/research.py --log work/ps2/overlay-query.log \
  --gameplay-overlay work/ps2/OVERLAYS/MAINGAME.BIN decompile \
  define:0xaab560 define:0xaab2e0
```

The map and temporary function definitions are discarded when the query ends.
This also makes the gameplay physics calls around `0xb9xxxx` accessible for later investigation. `DNASSCEE.BIN`
occupies the same runtime overlay address but is a different image; its SSL
strings are not evidence about the MainGame tunnel cipher.

| Overlay VA | Recovered operation |
| --- | --- |
| `0xaaba30` | Crypto context: cipher lookup `aes`, hash `sha1` for mode 1 (`md5` in mode 0), `sha256`, RNG `fortuna` |
| `0xaab100` / `0xaab210` | Verify/create handshake HMAC; a 32-byte supplied key selects SHA-256 |
| `0xaab8e0` / `0xaab910` | Export client public material and its length |
| `0xaab730` | Import the remote public block, compute DH shared secret and derive directional keys |
| `0xaab560` | Authenticate, append sequence and encrypt established traffic |
| `0xaab2e0` | Decrypt, check sequence and verify the truncated authenticator |
| `0xab2720` | HMAC initialization; inner pad XOR `0x36` and hash descriptors are visible |
| `0xab2a70` / `0xab2d50` | CTR setup/transform; assertion strings name `ctr_start.c` / `ctr_encrypt.c` |
| `0xab30c0` | DH shared-secret modular exponentiation; assertion string `dh.c` |
| `0xaadaf0` / `0xaad980` / `0xaad7b0` | SHA-1 initialization/update/finalization |

The DH shared secret is SHA-1 hashed with four distinct ASCII suffixes:
`CEnc`, `CAuth`, `PEnc`, `PAuth` (`0xaab730`). The outbound client cipher uses
the first 16 bytes of the `CEnc` result, inbound uses `PEnc`; authentication
uses all 20 bytes of `CAuth`/`PAuth` respectively. The cipher is **AES-CTR**,
with a big-endian counter-increment mode (`0xab2d50`).

For established traffic (`0xaab560`), the client:

1. Appends an eight-byte HMAC-SHA1 authenticator over the plaintext envelope/payload.
2. Appends a four-byte big-endian sequence number.
3. Builds a 16-byte counter block from that sequence followed by zero bytes.
4. Encrypts from offset 4 through the authenticator, leaving the initial four
   envelope bytes and final four sequence bytes clear.

Thus a zero-payload established control packet occupies 22 bytes before UDP/IP.
`0xaab2e0` reverses this transform and applies a sequence window; `0x154410`
passes values 4/4 as the encryption-offset/window parameters. Reconstruct and
test the exact window behavior before implementing reordering or replay rules.

Handshake HMACs use executable-supplied 32-byte values transformed from
obfuscated constants at `0x4e6e40` / `0x4e6e60`. Both constants decode to the
same key in this US executable. The loop complements each byte and XORs the
previous complemented byte with the current complemented byte. The key is
recorded in the implementation; it is shared by retail copies and authenticates
neither an individual account nor a Quazal PID. **Command 1 carries a region,
not a PID.**

### Exact DH and handshake transcript

The sole group descriptor at main VA `0x544720` is labelled `DH-1024` and
advertises a 128-byte private exponent. Its radix-64 modulus string at `0x54474a`
is actually **1036 bits**: `p = 2^1036 - 0x0ff33ef9`, with generator `g = 4`.
Both `p` and `(p-1)/2` pass the implementation's probable-prime checks. Replacing
this group with an RFC 1024-bit group would derive the wrong secret.

The public block is exactly 140 bytes in proxy commands 2/3:

| Offset | Field |
| --- | --- |
| 0–3 | Library packet header `05 01 01 00` (`0xab3050`) |
| 4 | Public-key type `01` |
| 5 | Group selector `10` (128 advertised bytes divided by eight) |
| 6–9 | Little-endian u32 magnitude byte length |
| 10 onward | Unsigned big-endian public value |

The public integer usually occupies 130 bytes. A shorter minimal magnitude can
leave padding in the fixed 140-byte message; the native importer ignores those
remaining bytes. The handshake HMAC still covers the **original whole block**.
The Go emitter uses a fixed 130-byte, zero-padded magnitude to avoid transmitting
uninitialized padding, which the native unsigned-integer importer permits.
The shared secret used by the KDF is its minimal unsigned big-endian encoding.

Each pre-key proxy packet is `10-byte envelope || payload || HMAC-SHA256[32]`.
The HMAC input is **context first, then envelope and payload**:

| Command | Payload | HMAC context prefix |
| --- | --- | --- |
| 1 client hello | Region u32 BE, protocol 4 u32 BE, challenge[32] | Empty |
| 2 server public | Server public block[140] | Challenge[32] |
| 3 client public | Client public block[140] | Challenge + server public block (172 bytes) |
| 8 server finish | Empty in the canonical reply | Challenge + server public + client public (312 bytes) |

Initial command 1 uses session `ffff`; command 2 assigns a session which commands
3/8 must echo. Public-key import/export (`0xab3280` / `0xaab8e0`), DH
exponentiation (`0xab3880` / `0xab30c0`) and transcript signing/checking
(`0xaab210` / `0xaab100`) were all decompiled from the mapped overlay.

### Go implementation and limits

[`internal/games/tdu/proxycrypto`](../../../internal/games/tdu/proxycrypto/crypto.go) now provides:

- `MasterRequest` / `MasterReply` codecs for UDP 8124, exposing proxy IPv4,
  directory IPv4 and retail revision 4392.
- `BeginServerHandshake` → signed command 2; `Complete` → command 8 and directional
  traffic keys. Exact repeated client-key packets return the cached finish;
  a changed key after completion fails. Keep the handshake state for UDP retries.
- Native DH public import/export, shared-secret KDF, stateless `SealPacket` /
  `OpenPacket`, and connection-owned `Sender` / `Receiver` state.

The separate peer `EncKey` ticket cipher belongs to
[`internal/games/tdu/peercrypto`](../../../internal/games/tdu/peercrypto/ticket.go); see
[peer-security.md](peer-security.md) for its native fixtures and identity schema.

The stateless packet functions implement the recovered wire transform. `Sender`
starts at sequence zero and refuses 32-bit wrap. `Receiver` deliberately uses a
four-packet sliding replay window with remembered duplicates. The retail decoder
instead stores `lastAccepted+1`, rejects the immediate last duplicate and checks
`sequence+4` against that value; accepting an older packet can move its state
backward. The server policy retains ordinary reordering compatibility while
rejecting all already-seen packets in its window. It does **not** claim to copy
that retail replay weakness. Packet size is bounded to the client's 1400-byte
UDP receive limit.

Tests include independently generated OpenSSL AES-CTR and Python HMAC/SHA
fixtures, DH exchange and group checks, exact transcript contexts including
nonzero padding, malformed fields, every-byte tampering, directional key mixups,
replay/reordering, sequence exhaustion and handshake retransmission. DH uses Go
`math/big` for this legacy group; its exponentiation is not guaranteed constant
time by that API. These library checks do not establish retail connectivity or
application-level account ownership. Proxy activation, native service lifecycle
and discovery are now integrated, with separate retail validation still required.

## Peer association uses a different format

The client downloads the resource `p2pkey` through its edNet resource service.
The completion callback `0x4613c0` zero-fills and copies up to 16 bytes to
`0x4e6e30`. Those exact bytes are consumed by `0x152c80` / `0x1520e0`.
This connects the resource gate to actual peer traffic.

Peer association packets begin with **u16 BE flags, u16 BE command**, then
payload. Association states and NAT-assisted lookup are managed by
`0x1527a0`, `0x151c40`, `0x1520e0` and the Quazal NAT adapter.

| Command | Recovered behavior |
| --- | --- |
| 0 | 130-byte `EncKey` ticket; validated by `0x152460` |
| 1 | Establish/acknowledge association (state 2/3 → 4) |
| 2 | Failed ticket / reset path |
| 3 | Reset/close path; protected when already established |
| 4 | Application payload; established association passes its body onward |
| 5 | Reset for an unavailable association |

`0x152460` decrypts the 130-byte ticket via overlay helper `0xac52c0`, then
checks an `EncKey` object containing a 16-byte field, the local Quazal
connection ID (CID/RVCID), and a serialized edNet client descriptor. It checks the source address against
the descriptor before accepting the association. The ticket cipher is now resolved: `0xac52c0` selects AES and SHA-256
(strings `0x544b28` / `0x544b2c`). The 130-byte input consists of
`AES-256-CTR(plaintext[82] || HMAC-SHA256(key, plaintext)[32]) || IV[16]`, with
big-endian CTR increment. Its 32-byte key is decoded from the separate constant
at `0x4e6e80`. The implementation and native validation are maintained in
[`internal/games/tdu/peercrypto`](../../../internal/games/tdu/peercrypto/ticket.go), with the complete
EncKey object schema and identity/endpoint binding described in
[peer-security.md](peer-security.md). This ticket key and object field are distinct
from the downloaded `p2pkey`; use the peer package's codec for their verified roles.

For commands 3/4 in established state 4, the sender `0x152c80`:

- Sets flag bit 0, appends the XOR of every plaintext header/payload byte, then
  XORs bytes starting at offset 2 through that checksum using
  `p2pkey[byte_offset & 15]`.
- Leaves the two flag bytes clear.

The receiver `0x1520e0` reverses the XOR and checks the one-byte XOR checksum.
It rejects unprotected command 3/4 messages. This is repeating-key XOR
obfuscation/checking, **not** the proxy's AES/HMAC scheme. Peers must have
compatible downloaded keys to exchange this wrapper.

`0x152b90` sends application command 4 once associated, or queues the outgoing
payload while association is pending; its established path bounds the payload
below `0x546` bytes. The ordinary peer association path sends direct UDP,
separate from the backend proxy; see [peer-connectivity.md](peer-connectivity.md).
Actual endpoint/NAT behavior still needs a two-client trace.

## Native actions and identifiers

Inside the selected transport, edNet serializes an action as a **u16 BE action
identifier**, followed by that action's fields. Constructor `0x13a9c0` selects
byte-swapping on this little-endian PS2 build. Primitive writers/readers are
in `0x138xxx..0x13axxx`:

| Encoding | Evidence |
| --- | --- |
| Begin action: u16 BE identifier | `0x13a530`; reader/matcher `0x1397d0` |
| u8 | `0x13a4c0` / `0x139480` |
| i16 / u16 | `0x13a420` / `0x13a370`; readers `0x139640` / `0x1395a0` |
| u32 / i32 | `0x13a2b0` / `0x13a1f0`; readers `0x1393c0` / `0x1394e0` |
| u64 | `0x139b80` |
| f32 bit pattern in big-endian order | `0x139ad0` / `0x1384b0` |
| Nested object | u16 BE byte count, then serialized action bytes (`0x139f20` / `0x138900`) |
| Buffer | u16 BE byte count, then bytes (`0x13a000` / `0x138b80`) |
| String | u16 BE byte length, bytes excluding the NUL (`0x13a0f0`) |

Action IDs are computed by `0x13aa70` / `0x13ab70`: CRC-32 with reflected
polynomial `0xedb88320`, initial/final XOR `0xffffffff`, then XOR the upper
and lower 16 bits. `strlen` determines the input length, excluding the C-string
terminator. Tabs and spaces are significant. The physics signature contains
embedded tabs; the printable `.strings.tsv` rows do not preserve a whole
NUL-terminated signature as one row in that case.

The initializer `0x55cdd0` stores the resulting 16-bit IDs in globals; the peer
senders and receiver use those same globals. These are static-derived IDs,
not values observed in a retail packet:

| Action | ID | Signature VA |
| --- | --- | --- |
| Peer login request | `0x5ea4` | `0x536f10` |
| Peer login reply | `0x0002` | `0x536f72` |
| Player descriptor | `0x22b1` | `0x536fe0` |
| Full physics update | `0xee87` | `0x536b5e` |
| Minimal update | `0x37f3` | `0x536cac` |
| Declare player state | `0x9c7e` | `0x536d40` |
| Start synchronization | `0xcf5d` | `0x536d83` |
| Peer keepalive | `0xca67` | `0x536e88` |
| Start signal | `0x75d3` | `0x536ec2` |
| Car changed | `0x4606` | `0x53715a` |
| Weather synchronization | `0x8b95` | `0x53720e` |
| Player position | `0x4853` | `0x537285` |
| Honk | `0x7b71` | `0x5372bb` |
| Windows | `0x0cd3` | `0x5372e7` |
| Round-trip request/reply | `0x03c5` / `0x7232` | `0x537322` / `0x537359` |

The identifier is **not globally unique**. The core-directory reply signature at
`0x539ca2` also folds to `0xee87`, the same value as the physics action at
`0x536b5e`. This collision was independently recomputed from both exact C
strings. Dispatch must retain its service/connection role and expected action
context; a universal ID-only registry would misclassify valid messages.

Regenerate these from the verified executable without normalizing whitespace:

Run these historical research commands from the sibling `tdu-server` root:

```sh
python3 tools/ednet_action_ids.py --elf work/ps2/SLUS_214.90 > work/gameplay/action-ids.json
```

The output includes exact escaped signatures and belongs in ignored research
storage. The helper also accepts `--signature` for an exact ASCII signature.

## Joining a peer session

`0x41a420` serializes `r_p2plogin`:

| Order | Field |
| --- | --- |
| 1 | Action ID `0x5ea4` |
| 2 | Length-prefixed serialized session object |
| 3 | u32 game ID |
| 4 | u32 user ID |
| 5 | Length-prefixed prebuilt player descriptor |

`0x41b7a0` is the central gameplay action dispatcher. The login branch checks
that the receiving peer is in an appropriate state and acting as game master,
validates the session object and requested game ID, avoids duplicate users,
accounts for reserved slots and finds a free player slot. Its player arrays
are explicitly bounded to **eight** slots.

Failure replies contain `a_p2plogin` and i32 `-1`. Success replies write the
assigned i32 index, **u32 game ID**, u8 lobby status, i32 network time, then zero
or more `u8 existing-player-index + nested player object` entries. The debug
signature omits that game-ID field; the actual sender and completion reader both
include it, and the completion reader checks it against the current game. The runtime completion
callback `0x41a650` reads the reply, installs the player/endpoint descriptors,
updates online presence state and invokes game/UI callbacks. This is a real
native peer login after backend authentication, not a second password login.

The player object serializer/reader are `0x428e20` / `0x428a80`:

- Action/format ID `0x22b1`, u8 slot index, one packed network-state/NAT byte,
  u32 user ID and a length-prefixed name.
- A nested edNet client descriptor containing connectivity identity/endpoints.
- Signed 16-bit car ID (`-1` means absent). When a car is present, color/rim/
  interior bytes, a u64 appearance field and an optional extra object follow.
- Another state byte, six f32 values representing orientation/position, and an
  eight-bit lock mask.

The decoder explicitly separates the high two bits and low six bits of the
packed state/NAT byte; exact enum names still need validation against gameplay.
Do not copy this object into a universal profile structure: it mixes transient
connectivity, appearance and session membership.

## Cars, cruising and race synchronization

The client sends actual vehicle simulation updates. The primary sender is
`0x40b7a0`; receiver `0x41b7a0` queues per-player buffers, and `0x40c1e0` /
`0x40d320` consume them with interpolation/timing correction. `0x410290` also
handles time-indexed stored network/replay actions.

The full update is not the literal list of types suggested by its long debug
signature. Its observed serializer `0x40fc80` emits:

1. Action ID, u8 player index, u32 timestamp and u8 vehicle/state flags.
2. Twelve big-endian f32 values: position, three rotation/Euler values, and two
   three-component velocity vectors.
3. A byte with four two-bit control codes; each code means `0`, `1`, explicit
   f32, or `-1`. Explicit values follow only when needed.
4. A second byte carrying two more two-bit control codes and a four-bit gear
   value, followed by any remaining explicit f32 values.

The six controls correspond to driving/lean state in the recovered structure;
confirm the exact simulator field names during fixture work. This action is
58–82 bytes including its action ID before outer transport encapsulation.
The minimal update path also carries flags and rotation values in the sender;
its short textual signature is not a complete wire specification either.

The dispatcher has separate actions for readiness/player state, synchronization
start time, game-master start signal, weather/time, car changes, horn/windows,
keepalives and departure. It validates sender/player membership before several
state transitions. These are concrete evidence of shared live cruising and
race coordination, beyond merely fetching leaderboards.

No inspected code establishes that the central backend simulates cars. The
current evidence instead points to clients exchanging peer simulation state
while backend services discover sessions, supply configuration and arrange
connectivity. Host migration, every instant-challenge transition and race result
submission still need a full state/capture trace.

## Remaining validation and private evidence

The recovered transport and crypto now back implemented activation,
directory/resources, transport-owned session lifecycle and presence, and
bounded Quazal endpoint introductions. The master-to-directory address handoff
is native-executed, not an unresolved configuration dependency. See
[startup-handoff.md](startup-handoff.md), [native-services.md](native-services.md),
[sessionmaster.md](sessionmaster.md), [presence.md](presence.md),
[peer-connectivity.md](peer-connectivity.md) and
[title-services.md](title-services.md).

A successful stock connection remains untested. Capture two clients joining a
free ride, driving/honking, starting and finishing a race, and leaving/rejoining.
Exercise same-LAN and distinct public endpoints; validate strict-NAT behavior
and distinguish native CID claims from ticket-proven account PIDs. No current
implementation or fixture establishes completed shared cruising.

Private artifacts are under `work/gameplay/`:

- `ps2/login-wire.log`, `physics.log`, `native-buffer.log`, `native-nesting.log`:
  peer action handlers and serializers.
- `ps2/native-datagrams.log`, `nat-vtable.log`, `proxy-security.log`,
  `tunnel-state.log`, `peer-key.log`: transport/ticket/proxy state machinery.
- `ps2/overlay-crypto.log`, `overlay-primitives.log`, `handshake-details.log`:
  MainGame crypto entry points, exact DH serialization and ticket cipher.
- `ps2/native-identity.log`, `master-fields.log`, `master-unused-field.log`:
  region/revision argument chain and historical master-field use analysis;
  its former opaque-field conclusion is superseded by the `0x377b80` handoff
  in `work/authentication/ps2/startup-handoff-native.json`.
- `action-ids.json`: exact C-string signatures and computed IDs.
- `disc/DNASSCEE.BIN`: separately inspected DNAS image; not the gameplay overlay.

`GameplayOverlay.java` mapping was verified by decompiling all listed entry
points with `-readOnly`; the log confirms the changes were discarded. The
four synthetic action-ID/tooling tests pass, but these checks do not establish
retail interoperability.
