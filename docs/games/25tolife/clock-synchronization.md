# Native clock synchronization — SLUS-210.16

The reported failure occurs when either host or joiner runs at approximately
50% emulation speed. Returning to full speed appears to adjust the clock but
leaves a considerable offset. Persistent disagreement and stranded round
transitions remain unresolved. The backend retry-grace change is not a clock
correction.

## Relay audit

A private game-night capture was checked across the five-minute stalled-start
window. Incoming datagrams were matched against byte-identical outgoing copies
from the other socket of their allocated relay pair.

| Measurement | Result |
| --- | --- |
| Matched non-USER datagrams across audited routes | 9,151 |
| Median time inside the relay | 0.119 ms |
| 99th percentile time inside the relay | 0.276 ms |
| Maximum measured time inside the relay | 4.069 ms |
| Affected host/joiner route | 543 host-to-joiner and 539 joiner-to-host packets; every incoming packet had a matching outgoing copy |
| Maximum incoming traffic per pair in a rolling second | 69 packets, below the shared 256 packets/second limit |
| Reconstructed token-bucket drops | 0 |
| Datagrams exceeding the 2,048-byte relay limit | 0 |

Six packets on other routes lacked matching copies: one initial SYN and five
packets to an already departed participant after its exit-drain window. These
are not evidence of lost clock traffic on the affected host/joiner route.

These measurements cover forwarding inside RackNerd, not WAN transit or receipt
by either client. The forwarding measurements alone do not identify individual
clock RPCs, and this is not a controlled reproduction of the half-speed test.
They establish no substantial relay queue in that particular failure window;
they do not establish that all timing failures are unrelated to the relay.

The relay can affect synchronization through added network transit, asymmetric
delay, packet loss, or its own limits. Active, fully proved routes have no
intentional steady-state queue: complete datagrams are forwarded unchanged.
The eight-packet buffer applies while endpoint proofs are pending. The shared
pair limiter can discard clock traffic along with other traffic if exhausted;
it has no clock-specific priority. Socket-write failures and limiter drops lack
per-reason counters in the current implementation, so aggregate forwarding
counts alone cannot exclude either problem in a future incident.

## Stock clock implementation

Evidence is from read-only analysis of the unmodified US executable, SHA-256
`52963efa0a12a44db01dbfad65d9bbb865c802c7ddf4513855913314b69cdba3`.

- Default high-precision time reaches `0x0025cc90` through `0x002b6600` and
  `0x002c80b0`. It reads PS2 RCNT2, including overflow state. Local elapsed time
  is derived by `0x002d27e0` / `0x002d2870`.
- `SessionClock::JobSendPulse` is created at `0x002bab68`. Callback `0x00566768`
  invokes `0x002bb9d8`, which sends a fresh local timestamp through SyncRequest.
  Defaults include ten initial samples, a fast initial polling interval and
  a nominal 2,000 ms interval afterward. Slower emulated time can stretch a
  nominal interval relative to wall time.
- Generated metadata `0x002bbeb0` registers **AdjustTime**, **SyncRequest** and
  **SyncResponse**. Dispatcher `0x002bbd30` routes them to `0x002bc038`,
  `0x002bc188` and `0x002bc310`. These are native NetZ object RPCs, not additional
  backend protocol-101 methods. Method-name registration assigns runtime IDs;
  a constant ID inferred from a different session is insufficient.
- SyncRequest handler `0x002bad90` responds only when the local station owns
  the clock object. It returns the echoed request timestamp, its session time
  and a processing-delay field. SyncResponse handler `0x002baee8` runs on a
  non-owner and compensates for elapsed time and receive/processing delay.
- Initialized correction `0x002ba7f8` smooths the estimated offset. With the
  retail default of ten samples, each update applies approximately 10% of the
  new estimate and retains 90% of the old offset. Constructor `0x002ba348`
  initializes the clock-rate multiplier to 1.0; no automatic rate estimator
  has been established by this trace.
- AdjustTime receiver `0x002bc038` reads a signed 64-bit delta. `0x002bba90`
  adds it to the current session time and calls `0x002ba460` to set the offset
  directly. This supplies a native immediate-adjustment mechanism, but its
  presence does not establish that the backend can legitimately invoke it.

An isolated numerical model illustrates the smoother's effect: with initialized
clocks, zero network delay, regular pulses every two local seconds and a
120-second half-speed period, a slow follower retains roughly 17 seconds of
error when normal speed resumes and takes roughly 56 seconds to get below one
second. With a slow authority, the corresponding model gives roughly nine
seconds and 42 seconds. This is not a retail test or a complete NetZ simulation;
missed pulses, authority changes, processing delays and round-phase replication
are not represented. It explains a possible lingering offset without proving
the cause of the reported incidents.

## Decoded native timing exchanges

A subsequent offline decoder follows the title's native message pipeline:
`0x002b7350` adds an inner checksum, applies the codec installed by
`0x0037b5d8`, then invokes the cipher constructed by `0x0037da60`.
The receive path reverses the cipher through `0x00478da8`, reads the codec's
16-bit original-size prefix (`0` means uncompressed; otherwise LZO), and
verifies the checksum through the algorithm at `0x00274bb0`. Length-prefixed
messages then expose opcode 15 bundles and opcode 10 object RPCs. The latter
have a 17-byte header followed by typed arguments. This layer is separate from
backend plain/LZO wrapping and PRUDP frame checksums.

The initial complete-message audit decoded 100,680 distinct directional
native DATA payloads with valid inner checksums, decompression and message
boundaries. Fragmented application messages were excluded. Repeated forwarding
copies remain separate directions in that count; it is not a count of unique
game actions. Clock candidates match the generated SyncRequest's eight-byte
timestamp and SyncResponse's echoed timestamp, authority timestamp and
four-byte processing delay. Observed runtime method IDs are capture-specific.

In the previously audited five-minute stalled-lobby window, the affected
joiner's route supplied 110 fresh requests and 105 responses. All 105 responses
matched request timestamps. Relay-arrival request-to-response time was
210.892–229.752 ms (median 215.864 ms); this includes the authority-side network
leg and processing, and is not a full client RTT or relay residence time.
The response processing field ranged from zero to three milliseconds.

The requester's local timestamp advanced 218.264 seconds across 218.297 seconds
of server wall time. Authority timestamps advanced 208.275 seconds across
208.284 seconds. These observations show approximately normal clock progression
during that window. Requests use local elapsed time, not the requester's
corrected session time: their progression cannot establish agreement between
the displayed clocks or exclude an already accumulated offset or stalled game
phase. Five unmatched requests occurred at the end of the exchange; a capture
at the relay alone cannot identify why responses ceased.

A different route's requester advanced only 210.547 seconds across 217.085
seconds of wall time, while its authority advanced approximately normally.
This demonstrates a measurable client-side clock progression discrepancy in
the capture, without identifying its cause or equating it with either the
affected lobby or the separately reported real-console disconnect. No matching
AdjustTime invocation was observed in these complete-message audits.

Raw payloads, decoded game messages, the title's embedded cipher material and
player/endpoint mappings remain outside the public repository. The decoder
only reads private captures; it sends no packets and changes no client state.

## Resynchronization feasibility

The concrete native candidate is AdjustTime rather than replaying old sync
datagrams. Replays retain old timestamps and are not fresh timing measurements;
they may also be suppressed by transport sequencing. More frequent fresh
SyncRequest samples could shorten convergence, but the current trace exposes
client-side scheduling defaults, not a backend setting that changes them.

Before originating a correction, native object ownership, runtime method IDs,
target selection, transport state and the sender's authority must be verified.
A transparent relay currently owns none of those native clock objects.
Changing timestamps only for relayed traffic would also leave direct peer
traffic outside the correction and could introduce inconsistent timebases.
The lobby host and elected native clock owner must not be assumed identical
across migration without checking their actual ownership state.

The next distinguishing capture is a controlled full-speed, half-speed,
full-speed run, separately with a slow host and a slow joiner, compared over
direct and forced-relay paths. Decode fresh SyncRequest/SyncResponse pairs and
the elected clock owner, then compare their timestamp progression, correction
and round-phase deadlines. A protocol-correct native correction must be
demonstrated with stock clients before deployment. No client patch, timestamp
rewrite, synthetic round start or new clock authority is implemented here.
