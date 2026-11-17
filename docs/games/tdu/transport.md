# Quazal PRUDP transport: US SLUS-214.90

Research provenance: these findings were produced in the sibling `tdu-server`
workspace. Runtime package paths below refer to Quetzal; `tools/`, private
`work/` evidence and Ghidra projects refer to that original research workspace.
Keep those private files there; Quetzal has no runtime dependency on them.
See [running.md](running.md) for current Quetzal operation.

These layouts were recovered from the client's actual reader and writer. They
are separate from native edNet actions and peer gameplay. The standalone Go
server now implements the supported RPC transport, tested with raw UDP clients.
A retail PS2 or PCSX2 exchange has not yet validated it end to end.

## Frame layout

`PacketIn` reader `0x22db30` and `PacketOut` writer `0x22e310` agree on the
following packed order. Multibyte scalar fields are little-endian: the byte
stream helpers `0x2362f0` / `0x236320` delegate to raw memory-copy wrappers
`0x236200` / `0x236230` on the little-endian EE.

| Offset | Size | Field |
| --- | --- | --- |
| 0 | 1 | Source virtual stream: high nibble type, low nibble stream ID |
| 1 | 1 | Destination virtual stream: high nibble type, low nibble stream ID |
| 2 | 1 | Low three bits packet type, upper five bits flags |
| 3 | 1 | Connection session byte |
| 4 | 4 | Transport signature |
| 8 | 2 | Packet sequence |
| 10 | 4, conditional | SYN or CONNECT connection signature |
| 10 | 1, conditional | DATA fragment ID |
| After type extension | 2, conditional | Payload byte count when flag `0x40` is set |
| After header/extensions | variable | Encoded payload |
| Last byte | 1 | Stream-seeded checksum |

The fixed prefix is 10 bytes, with at least one checksum byte following it.
SYN/CONNECT have a 14-byte prefix; DATA has an 11-byte prefix, before the optional
size field. `0x22d7e0` returning `0x12` is an internal overhead budget, not the
literal size of every wire header. Fields in the C++ packet object are not
packed at wire offsets; do not copy that object as a packet structure.

The source/destination construction in `0x219b90` uses `(streamType << 4) |
streamID`. The login URL configures stream type 3 and ID 1. The client uses the
source stream type's high nibble to select decoding settings; receiver
`0x22db30` checks that it is below 8. These nibbles are distinct from UDP ports.

## Types and flags

The logging switch in `0x225ff0` ties the numeric types to diagnostic names:

| Value | Type |
| --- | --- |
| 0 | SYN |
| 1 | CONNECT |
| 2 | DATA |
| 3 | DISCONNECT |
| 4 | PING |
| 5 | USER |
| 6 | ACK |
| 7 | RAW, internal bypass path |

The serializer's RAW branch copies its buffer directly, without constructing
the standard prefix or trailer. A standard-header parser cannot recognize all
RAW traffic by inspecting its first byte.

| Mask | Observed behavior |
| --- | --- |
| `0x08` | Acknowledgement. Logging marks it `A`; reply construction sets it. |
| `0x10` | Reliable delivery/window processing. Logging marks it `R`. |
| `0x20` | Request an acknowledgement. Logging marks it `W`; receiver constructs a reply. |
| `0x40` | Include explicit u16 payload size after the type-specific extension. |
| `0x80` | Logging marks it `V`; semantic meaning remains unresolved. |

ACK status can be a flag on the original packet type. Do not handle only the
standalone type-6 ACK value. SYN, CONNECT, DATA and PING paths explicitly test
`0x08`.

## Checksum and transport signature are different

`0x235340` appends one checksum byte. `0x2353f0` verifies it and `0x2353b0`
removes it before payload extraction. The arithmetic routine is `0x235210`:

1. Sum complete little-endian u32 words, wrapping the accumulator to 32 bits.
2. Add the four bytes of that accumulator, any remaining input bytes, and the
   stream's seed byte; keep the low eight bits.

The seed setter `0x219e80` sums the bytes of an SDK String modulo 256.
Stream setup `0x1ec870` obtains that String from the title access-key setting
at `0x5aaa00`, with an optional formatted variant. Title initialization
`0x1e0630` → `0x1e75b0` → `0x1ecce0` supplies the literal `yh64s`, giving
initial seed **`0xbe`** before that optional formatting. The nearby literal
`h7fyctiuucf` belongs to the signup guest credential, not the title access key.
This seed is not a player's
password or the password-derived ticket key.

Transport signature generation `0x216dd0` serializes four raw address bytes
and two raw port bytes, runs the checksum object created by `0x2348b0`, and
returns its first four result bytes. That object is HMAC-MD5: pad initialization
`0x234970` and digest implementation `0x234a90`. Its key is initialized by
`0x216b50` from a time-derived value. The signature is therefore an endpoint
cookie, not the one-byte checksum and not a per-payload MAC.

Stream receiver `0x225660` recomputes the endpoint signature. Ordinary types
must match it; SYN and USER are exceptions to this initial check. CONNECT
admission has additional state/callback validation. A server must enforce those
conditions rather than accepting packets solely because their checksum matches.
The client treats the server-generated cookie as opaque and echoes it in
subsequent packet signatures. The runtime uses a fresh random HMAC-MD5 secret
and a fixed IPv4/port serialization. It does not need to reproduce the client
SDK's time-derived secret: each endpoint independently verifies its own cookie.

## Encryption and compression

Packet output `0x22e310` obtains settings selected by stream type, optionally
compresses the payload through settings `+4`, then encrypts it through settings
`+8`. It writes the resulting payload length when `0x40` is present and finally
computes the frame checksum. Input `0x22db30` verifies/strips the checksum,
extracts the encoded payload, then decrypts and decompresses it in that order.

The linked network core constructs an RC4 object at `0x212930`; constructor
`0x2315e0` uses a five-byte default key assembled by `0x231770` (`CD&ML`).
Title initialization `0x1e0630` explicitly calls `0x212d90`, which sets the
otherwise-zero BSS encryption flag `0x56f9d4` and installs that RC4 object at
settings `+8` for stream types 2 and 3. Later core construction also honors the
flag. Compression is enabled by the backed default byte `0x56f55c = 1` and is
installed at settings `+4` for those same streams. The runtime enables only the
proven type-3 RPC stream.

RC4 transform `0x231820` calls `0x2319b0` before each operation, restoring its
saved S-box and zeroing both indices. Every packet starts from the beginning of
the RC4 keystream. This transport object is separate from temporary RC4 objects
created by ticket helpers `0x1eb970` / `0x1ebae0`; replacement of those helpers'
keys does not establish a transport key change. No startup path changing the
shared packet object's `CD&ML` key was found.

Compression wrapper `0x21fa00` selects the smaller representation: byte `0`
followed by raw bytes, or byte `1` followed by LZO1X bytes. Its decoder is
`0x21fbd0`, with LZO1X compressor `0x220640` and bounded decoder `0x220790`.
The runtime emits selector 0 and accepts selectors 0/1 with a strict expansion
bound. It rejects other selectors. Empty wire payloads skip both transforms;
ordinary empty ACKs and SYN packets therefore have no wrapper byte.

The RMC buffer itself begins with a **little-endian u32 length excluding those
four bytes** (`0x21a960`, `0x21aa70`, `0x21aa80`). It is inside the transformed
payload and may span reliable DATA fragments. Transport preserves that prefix;
`internal/protocol/quazal` parses it after ordered reassembly.

## Connection and delivery state

The relevant layers are stream `0x223a00`, endpoint `0x220cc0`, endpoint connect
`0x221240`, stream dispatch `0x225660` and endpoint dispatch `0x2226c0`.

The static client path is:

1. Endpoint construction allocates a SYN packet with ACK-request flag `0x20`;
   it starts a new nonzero session byte and clears sequence/connection state.
2. Connect `0x221240` schedules that SYN. A receiving stream can answer SYN with
   a SYN+ACK, echoing the sequence and returning its generated connection cookie.
3. Upon SYN+ACK, endpoint dispatch stores the returned connection signature and
   creates CONNECT with flags `0x30` (reliable + requests ACK). CONNECT can carry
   an application security payload.
4. Stream CONNECT admission `0x2253a0` calls the configured event/security
   handler with that payload. This is an additional acceptance boundary.
5. CONNECT+ACK processing copies any response payload to the waiting caller,
   acknowledges the outstanding send and changes endpoint state to connected.

`0x2226c0` emits requested acknowledgements, checks session bytes, processes
reliable delivery and dispatches DATA. `0x223140` removes acknowledged outgoing
packets using their sequence numbers and updates timing. Incoming reliable
packets go through `0x228070` and are drained in sequence; duplicate or old
sequence handling uses wrap-aware comparisons. Unreliable DATA has a separate
sequence/loss accounting path.

Data send `0x2218c0` maps its reliable option to flags `0x30`, can fragment
oversized reliable messages and writes fragment IDs starting at 1 for intermediate
pieces, wrapping past zero; the final fragment is 0. A single unfragmented DATA
packet also has fragment ID 0. Reliable reassembly and delivery buffers must be
connection-scoped. The server applies explicit memory/window/time limits,
described below, independently of retail tuning values.

PING creation `0x223460` requests an ACK; endpoint dispatch measures timing from
the matching reply. Retry/timeout failure processing `0x223270` can resend an
outstanding packet, or fail an unfinished SYN/CONNECT or disconnect operation.
The retry thresholds are obtained from stream settings; do not substitute
AngelWare's DemonWare timers.

## Runtime implementation and bounds

`internal/protocol/prudp` binds IPv4 UDP through `New`, exposes its actual bound address
through `Addr`, and runs until cancellation or `Close`. The application creates
separate auth/secure instances on their configured source ports. Peer IDs are
unique within each instance; application state keys include the local port.
All handler callbacks run on the owning server loop and are serialized.

SYN is stateless: its ACK echoes the sequence, uses zero session/signature, and
returns the server cookie in the SYN extension. CONNECT must echo that cookie
in its header signature, use reliable/ACK-request flags, and supply initial
reliable sequence 1 and a nonzero client session. Only then does `Handler.Connect`
validate the application ticket. Successful admission returns a CONNECT ACK
whose header signature is the client's connection-signature extension, and whose
session is a new nonzero server session. Its payload is the handler's response.
A byte-identical CONNECT retry reuses the cached ACK and does not run the
handler twice. A conflicting session cannot replace an active peer; it must
first disconnect or expire. Rejected CONNECTs receive no success response.

Client reliable sequence 1 is consumed by CONNECT; client DATA starts at 2.
Server reliable DATA starts at 1 because sending CONNECT ACK does not consume an
outgoing reliable sequence. Reliable DATA is buffered by sequence within a
bounded wrap-aware window, acknowledged with the original type/sequence/fragment,
and delivered once. Reliable PING consumes its sequence without dispatching RMC;
PING and DISCONNECT requests receive their type-specific ACK. Unreliable DATA,
USER, other stream types, and unresolved flag `0x80` are not dispatched as RPC.
Frames outside the configured receive window are not acknowledged. Explicitly
sized frames can be concatenated within a datagram, with a maximum of 128 frames
processed per datagram.

The zero-value runtime policy uses 128 peers, a 64-frame receive/send window,
a 65,536-byte logical-message and queued-receive budget per peer, 500 ms retry
spacing, five retransmissions, 10 seconds for CONNECT callbacks/incomplete
assemblies, and a two-minute idle timeout. These are deliberate server limits,
not claimed retail SDK defaults. Responses use independently encoded chunks of
at most 1,000 application bytes, intermediate fragment IDs 1..255 skipping zero,
and final fragment ID 0. Responses exceeding the available send window close
the peer instead of silently discarding an RPC result. ACKed packets leave the
retry set; unacknowledged packets retransmit identical datagrams and exhausted
retries close the peer. Reliable assembly, queued payloads and send windows are
connection-scoped and bounded. Callbacks must honor context cancellation.

Checks cover raw UDP SYN/CONNECT and cookie rejection, application CONNECT
rejection, session mismatch, idempotent CONNECT retries, out-of-order fragments,
duplicates, reliable PING, DISCONNECT, response ACK/retry exhaustion, peer/window
and message limits, idle expiration, checksum corruption and truncation. The
pure Go LZO1X decoder additionally matches nine independent `liblzo2`
`lzo1x_1_compress` fixtures, including long-distance matches and malformed/limited
expansion cases. The fixtures contain generated data, not extracted game assets. They also
match the actual retail decoder under Unicorn: the optional verifier
`internal/prudp/testdata/verify_retail_lzo.py` in the historical sibling workspace substitutes only two equivalent
DADDU-to-ADDU register moves (`0x2207a0`, `0x220c48`), and executes every
decompression operation. The private report is
`work/gameplay/ps2/prudp-native-lzo-validation.json`.

```sh
scripts/go test ./internal/protocol/prudp
scripts/go vet ./internal/protocol/prudp
```

This demonstrates local transport behavior. Retail capture validation must still
confirm the title's actual initial CONNECT payload, later secure CONNECT,
compression/encryption activation in the exchange, client retry timing, and any
use of unsupported flag `0x80`. Application-level login/ticket correctness is
owned by `internal/protocol/quazal`, not inferred from a successful transport handshake.

## Offline tools and validation

`tools/prudp_header.py` inspects one complete raw frame, leaving its payload
opaque. It validates fixed/type-specific header bounds and an explicit payload
length. A supplied seed enables checksum verification; without one, checksum
status is unknown. It does not authenticate endpoint signatures, decrypt payloads,
reassemble fragments, read PCAP containers or emulate a live server.

Run these historical research commands from the sibling `tdu-server` root:

```sh
python3 tools/prudp_header.py --hex 313f0427785634129a78a7
python3 tools/prudp_header.py --file work/one-udp-frame.bin --checksum-seed 0x55
python3 -m unittest discover -s tests -v
```

The example is synthetic. Checksum tests include carries across u32 words,
32-bit accumulator wrap, optional verification and corrupted input; header tests
include all extensions, flags, endianness, truncation and length disagreement.

`tools/verify_prudp_checksum.py` additionally executes the checksum routine from
the user's documented retail executable under Unicorn against 344 synthetic
length/seed combinations. All matched the offline implementation. The MIPS32
engine substitutes three DADDU-zero register moves with equivalent ADDU-zero
moves (`0x235220`, `0x235254`, `0x23528c`); no checksum arithmetic or memory reads
are mocked. This validates checksum arithmetic, not retail connectivity.

## Private evidence

Headless query logs live in ignored `work/ps2/transport-*.log`:
`initial`, `layout`, `packets`, `checksums`, `settings`, `config`, `stream-policy`
and `handshake`. `checksum-verification.json` records the Unicorn check. All
addresses above refer to the main executable identified in [build.json](build.json).

Additional runtime evidence is in ignored
`work/gameplay/ps2/prudp-settings-runtime.log`, `prudp-stream-settings.log`,
`prudp-compression.log`, `prudp-wrapper.log`, `prudp-lzo-and-flags.log`,
`prudp-final-wire.log`, `prudp-queue-final.log`, and `prudp-window-init.log`.
