# TDU native public endpoint bootstrap

The first retail test exposed a missing bootstrap operation after successful
Quazal registration and before native master/proxy connection. The engine UDP
socket sent an identical16-byte public-URL probe to the secure backend port.
This operation is separate from RMC login, peer introductions and later
NAT-engine kind0/1 timestamp probes.

## Request: PRUDP USER, stream7

The sanitized probe was:

`71710500000000000000cd8000000236`

| Offset | Field | Value |
| --- | --- | --- |
| 0 | Source stream | `71` (stream type7, index1) |
| 1 | Destination stream | `71` |
| 2 | Type/flags | `05`, USER with no flags |
| 3 | Session | zero |
| 4 | LEu32 signature | zero |
| 8 | LEu16 sequence | zero |
| 10 | Custom adapter prefix | `cd800000` |
| 14 | Public-URL operation | `02` |
| 15 | PRUDP checksum | `36`, using seed0 |

The observed constant validates with checksum seed0; the ordinary TDU RMC
seedBE would produceF4. This USER setting must not change the ordinary stream3
RMC checksum, payload transforms or account checks.

Native request construction is grounded in0x21b970: append the adapter header,
then byte2; call relay interface+0x0c. Concrete relay0x246790 calls0x2479c0,
which constructs PacketOut with literal arguments0,5,0 and sets both stream
bytes to71 before packet serialization. The destination is the secure backend's
address/port converted to a UDP StationURL by0x246800. This accounts for the
probe arriving on the secure physical port from the native engine socket.

## Reply: raw UDP, not a PRUDP frame

The response is exactly:

`cd800000` + `02` + ASCII `udp:/address=<observed IPv4>;port=<observed UDP port>` + `00`

There is **no PRUDP header or checksum**, and the string has **no SDK length
prefix**. Reply only to the actual UDP sender, using its observed endpoint in
the URL. The request contains no arbitrary forwarding destination. Reflection
neither authenticates an account nor advances a Quazal login.

Native demultiplexer0x244750 accepts a first little-endian u32 of000080CD or
0000CD80, so byte prefixes `cd800000` and `80cd0000` both work. It strips four
bytes and passes the remaining body through0x21cf30 to0x21bb60. The chosen
server reply uses `cd800000`, matching the request's custom adapter prefix.

Parser0x21bb60 reads the operation byte. For operation2 it calls raw reader
0x236230 with a256-byte cap, then constructs a StationURL from the resulting
C string. The raw reader copies remaining bytes; it does not decode a counted
String. Always include the terminating NUL and keep the reply inside that cap.
The client inserts its own current RVCID from engine+4, copies the URL into
adapter+4, and invokes the public-URL job callback. No PID/CID/RVCID needs to be
invented in the server's reflected URL.

## Lifecycle and verification

JobGetPublicURL0x21f670 sends a probe every250ms and allows ten attempts.
Callback0x21f7c0 sets job+0x40 when its context still exists. A subsequent job
iteration completes successfully. The observed roughly266ms retry interval is
consistent with that250ms scheduling plus normal processing time.

`internal/games/tdu/natprobe/testdata/native-reply.json` independently executes
the retail raw demux, parser and actual callback using synthetic endpoints.
Both adapter header byte orders reach the completion flag and retain the
observed endpoint URL; a PRUDP USER envelope at the raw receive boundary does
not. The fixture README records framework substitutions and reproduction steps.
This is offline native routine validation, not a full console run.

Private evidence is retained in the sibling research checkout under
`work/gameplay/ps2/retail-nat-{probe1,stream2,stream3,stream4,discovery5,framing6,relay7,relay8,receive9,envelope10,user11}.log`.
No authentication capture, game executable or decompilation is included here.
The next decisive check is the tester receiving this raw reply and proceeding
to native master8124/proxy8122 traffic; later peer traversal remains separate.
